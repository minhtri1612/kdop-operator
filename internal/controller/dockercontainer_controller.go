/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/registry"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	kdopv1alpha1 "github.com/minhtri1612/kdop-operator/api/v1alpha1"
	"github.com/minhtri1612/kdop-operator/internal/docker"
)

const dockerContainerFinalizer = "kdop.kdop.io.vn/finalizer"

// DockerContainerReconciler reconciles a DockerContainer object
type DockerContainerReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=kdop.kdop.io.vn,resources=dockercontainers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kdop.kdop.io.vn,resources=dockercontainers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kdop.kdop.io.vn,resources=dockercontainers/finalizers,verbs=update
// +kubebuilder:rbac:groups=kdop.kdop.io.vn,resources=dockerhosts,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *DockerContainerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	cr := &kdopv1alpha1.DockerContainer{}
	if err := r.Get(ctx, req.NamespacedName, cr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	name := cr.Spec.ContainerName
	if name == "" {
		name = cr.Name
	}

	if !cr.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(cr, dockerContainerFinalizer) {
			cli, err := docker.NewClient(ctx, r.Client, cr.Namespace, cr.Spec.DockerHostRef)
			if err == nil {
				_ = r.deleteExternalResources(ctx, cli, cr, name)
				_ = cli.Close()
			}
			controllerutil.RemoveFinalizer(cr, dockerContainerFinalizer)
			return ctrl.Result{}, r.Update(ctx, cr)
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(cr, dockerContainerFinalizer) {
		controllerutil.AddFinalizer(cr, dockerContainerFinalizer)
		return ctrl.Result{}, r.Update(ctx, cr)
	}

	cli, err := docker.NewClient(ctx, r.Client, cr.Namespace, cr.Spec.DockerHostRef)
	if err != nil {
		log.Error(err, "docker client")
		return ctrl.Result{}, err
	}
	defer func() { _ = cli.Close() }()

	if err := r.sync(ctx, cli, cr, name); err != nil {
		log.Error(err, "sync container")
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: time.Minute}, nil
}

func (r *DockerContainerReconciler) sync(ctx context.Context, cli dockerclient.APIClient, cr *kdopv1alpha1.DockerContainer, name string) error {
	want := "/" + name

	list, err := cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return err
	}

	var match *types.Container
	for i := range list {
		if slices.Contains(list[i].Names, want) {
			match = &list[i]
			break
		}
	}

	if match == nil {
		return r.create(ctx, cli, cr, name)
	}

	inspect, err := cli.ContainerInspect(ctx, match.ID)
	if err != nil {
		return err
	}

	if needsRecreate(inspect, &cr.Spec) {
		timeout := 10
		_ = cli.ContainerStop(ctx, match.ID, container.StopOptions{Timeout: &timeout})
		if err := cli.ContainerRemove(ctx, match.ID, container.RemoveOptions{Force: true}); err != nil {
			return err
		}
		return r.create(ctx, cli, cr, name)
	}

	state := match.State
	if state != "running" {
		if err := cli.ContainerStart(ctx, match.ID, container.StartOptions{}); err != nil {
			return err
		}
		state = "running"
	}

	return r.writeStatus(ctx, cr, match.ID, state, inspect)
}

func (r *DockerContainerReconciler) create(ctx context.Context, cli dockerclient.APIClient, cr *kdopv1alpha1.DockerContainer, name string) error {
	pullOpts := image.PullOptions{}
	if cr.Spec.ImagePullSecret != "" {
		authConfig, err := r.getAuthConfig(ctx, cr.Namespace, cr.Spec.ImagePullSecret)
		if err != nil {
			return err
		}
		encodedJSON, err := json.Marshal(authConfig)
		if err != nil {
			return err
		}
		pullOpts.RegistryAuth = base64.URLEncoding.EncodeToString(encodedJSON)
	}

	_, _, err := cli.ImageInspectWithRaw(ctx, cr.Spec.Image)
	if err != nil {
		out, pullErr := cli.ImagePull(ctx, cr.Spec.Image, pullOpts)
		if pullErr != nil {
			return pullErr
		}
		defer func() { _ = out.Close() }()
		_, _ = io.Copy(io.Discard, out)
	}

	policy := cr.Spec.RestartPolicy
	if policy == "" {
		policy = "always"
	}

	exposed, bindings, err := parsePorts(cr.Spec.Ports)
	if err != nil {
		return err
	}

	resolvedEnv, err := r.resolveEnv(ctx, cr)
	if err != nil {
		return err
	}

	var binds []string
	for _, v := range cr.Spec.VolumeMounts {
		bind := fmt.Sprintf("%s:%s", v.HostPath, v.ContainerPath)
		if v.ReadOnly {
			bind += ":ro"
		}
		binds = append(binds, bind)
	}

	resp, err := cli.ContainerCreate(ctx,
		&container.Config{
			Image:        cr.Spec.Image,
			Cmd:          cr.Spec.Command,
			Env:          resolvedEnv,
			ExposedPorts: exposed,
			Healthcheck:  buildDockerHealthCheck(cr.Spec.HealthCheck),
		},
		&container.HostConfig{
			RestartPolicy: container.RestartPolicy{
				Name: container.RestartPolicyMode(strings.ToLower(policy)),
			},
			PortBindings: bindings,
			Binds:        binds,
			Resources:    buildDockerResources(cr.Spec.Resources),
		},
		nil, nil, name,
	)
	if err != nil {
		return err
	}

	for _, sv := range cr.Spec.SecretVolumes {
		if err := r.uploadSecretToContainer(ctx, cli, cr.Namespace, sv, resp.ID); err != nil {
			return err
		}
	}

	if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return err
	}

	inspect, _ := cli.ContainerInspect(ctx, resp.ID)
	return r.writeStatus(ctx, cr, resp.ID, "running", inspect)
}

func (r *DockerContainerReconciler) writeStatus(ctx context.Context, cr *kdopv1alpha1.DockerContainer, id, state string, inspect types.ContainerJSON) error {
	cr.Status.ID = id
	cr.Status.State = state
	cr.Status.IPv4 = ""
	cr.Status.Health = ""
	if inspect.NetworkSettings != nil {
		for _, n := range inspect.NetworkSettings.Networks {
			if n.IPAddress != "" {
				cr.Status.IPv4 = n.IPAddress
				break
			}
		}
	}
	if inspect.State != nil && inspect.State.Health != nil {
		cr.Status.Health = inspect.State.Health.Status
	}
	return r.Status().Update(ctx, cr)
}

func (r *DockerContainerReconciler) deleteExternalResources(
	ctx context.Context,
	cli dockerclient.APIClient,
	cr *kdopv1alpha1.DockerContainer,
	name string,
) error {
	log := logf.FromContext(ctx)

	// 1. Main container
	if err := cli.ContainerRemove(ctx, name, container.RemoveOptions{Force: true}); err != nil && !dockerclient.IsErrNotFound(err) {
		log.Error(err, "remove container", "name", name)
		return err
	}

	// 2. Tunnel containers (Phase 4 patterns; no-op if none exist)
	for i := range 10 {
		for _, tn := range []string{
			fmt.Sprintf("%s-tunnel-%d", name, i),
			fmt.Sprintf("%s-tunnel-%d", cr.Name, i),
		} {
			if err := cli.ContainerRemove(ctx, tn, container.RemoveOptions{Force: true}); err != nil && !dockerclient.IsErrNotFound(err) {
				log.Error(err, "remove tunnel container", "name", tn)
			}
		}
	}

	// 3. Legacy single tunnel name
	legacy := name + "-tunnel"
	if err := cli.ContainerRemove(ctx, legacy, container.RemoveOptions{Force: true}); err != nil && !dockerclient.IsErrNotFound(err) {
		log.Error(err, "remove legacy tunnel", "name", legacy)
	}

	return nil
}

func parsePorts(ports []string) (nat.PortSet, nat.PortMap, error) {
	if len(ports) == 0 {
		return nil, nil, nil
	}

	exposed := nat.PortSet{}
	bindings := nat.PortMap{}

	for _, p := range ports {
		hostPort, containerPort, ok := strings.Cut(p, ":")
		if !ok {
			return nil, nil, fmt.Errorf("invalid port mapping %q, want \"hostPort:containerPort\"", p)
		}
		key, err := nat.NewPort("tcp", containerPort)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid port mapping %q: %w", p, err)
		}
		exposed[key] = struct{}{}
		bindings[key] = append(bindings[key], nat.PortBinding{HostIP: "0.0.0.0", HostPort: hostPort})
	}

	return exposed, bindings, nil
}

func (r *DockerContainerReconciler) resolveEnv(ctx context.Context, cr *kdopv1alpha1.DockerContainer) ([]string, error) {
	resolved := append([]string{}, cr.Spec.Env...)
	for _, ev := range cr.Spec.EnvVars {
		val := ev.Value
		if ev.ValueFrom != nil && ev.ValueFrom.SecretKeyRef != nil {
			ref := ev.ValueFrom.SecretKeyRef
			secretVal, err := r.getSecretValue(ctx, cr.Namespace, ref.Name, ref.Key)
			if err != nil {
				return nil, fmt.Errorf("env %s: %w", ev.Name, err)
			}
			val = secretVal
		}
		resolved = append(resolved, fmt.Sprintf("%s=%s", ev.Name, val))
	}
	return resolved, nil
}

func (r *DockerContainerReconciler) getSecretValue(ctx context.Context, namespace, name, key string) (string, error) {
	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, secret); err != nil {
		return "", err
	}
	val, ok := secret.Data[key]
	if !ok {
		return "", fmt.Errorf("key %q not found in secret %q", key, name)
	}
	return string(val), nil
}

func (r *DockerContainerReconciler) getAuthConfig(ctx context.Context, namespace, secretName string) (registry.AuthConfig, error) {
	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: secretName}, secret); err != nil {
		return registry.AuthConfig{}, err
	}

	username := string(secret.Data["username"])
	password := string(secret.Data["password"])
	server := string(secret.Data["server"])
	if username == "" {
		return registry.AuthConfig{}, fmt.Errorf("username not found in secret %q", secretName)
	}

	return registry.AuthConfig{
		Username:      username,
		Password:      password,
		ServerAddress: server,
	}, nil
}

func buildDockerHealthCheck(hc *kdopv1alpha1.HealthCheckConfig) *container.HealthConfig {
	if hc == nil {
		return nil
	}
	cfg := &container.HealthConfig{Test: hc.Test}
	if hc.Interval != "" {
		if d, err := time.ParseDuration(hc.Interval); err == nil {
			cfg.Interval = d
		}
	}
	if hc.Timeout != "" {
		if d, err := time.ParseDuration(hc.Timeout); err == nil {
			cfg.Timeout = d
		}
	}
	if hc.Retries > 0 {
		cfg.Retries = hc.Retries
	}
	return cfg
}

func buildDockerResources(r *kdopv1alpha1.ResourceRequirements) container.Resources {
	if r == nil {
		return container.Resources{}
	}
	res := container.Resources{}
	if r.CPULimit != "" {
		if cpuFloat, err := strconv.ParseFloat(r.CPULimit, 64); err == nil {
			res.NanoCPUs = int64(cpuFloat * 1e9)
		}
	}
	if r.MemoryLimit != "" {
		res.Memory = parseMemoryString(r.MemoryLimit)
	}
	return res
}

func parseMemoryString(s string) int64 {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0
	}

	multiplier := int64(1)
	switch {
	case strings.HasSuffix(s, "g"):
		multiplier = 1024 * 1024 * 1024
		s = strings.TrimSuffix(s, "g")
	case strings.HasSuffix(s, "m"):
		multiplier = 1024 * 1024
		s = strings.TrimSuffix(s, "m")
	case strings.HasSuffix(s, "k"):
		multiplier = 1024
		s = strings.TrimSuffix(s, "k")
	}

	val, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return val * multiplier
}

func needsRecreate(inspected types.ContainerJSON, spec *kdopv1alpha1.DockerContainerSpec) bool {
	if inspected.Config == nil {
		return false
	}

	if inspected.Config.Image != spec.Image {
		return true
	}

	if len(spec.Command) > 0 && !slices.Equal(inspected.Config.Cmd, spec.Command) {
		return true
	}

	for _, expected := range spec.Env {
		if !slices.Contains(inspected.Config.Env, expected) {
			return true
		}
	}

	if spec.RestartPolicy != "" && inspected.HostConfig != nil {
		if !strings.EqualFold(string(inspected.HostConfig.RestartPolicy.Name), spec.RestartPolicy) {
			return true
		}
	}

	if spec.Resources != nil && inspected.HostConfig != nil {
		desired := buildDockerResources(spec.Resources)
		if desired.NanoCPUs != inspected.HostConfig.NanoCPUs {
			return true
		}
		if desired.Memory != inspected.HostConfig.Memory {
			return true
		}
	}

	return false
}

func (r *DockerContainerReconciler) uploadSecretToContainer(
	ctx context.Context,
	cli dockerclient.APIClient,
	namespace string,
	sv kdopv1alpha1.SecretVolume,
	containerID string,
) error {
	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: sv.SecretName}, secret); err != nil {
		return err
	}

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	relPath := strings.TrimPrefix(sv.MountPath, "/")

	for name, data := range secret.Data {
		hdr := &tar.Header{
			Name: filepath.Join(relPath, name),
			Mode: 0444,
			Size: int64(len(data)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := tw.Write(data); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}

	return cli.CopyToContainer(ctx, containerID, "/", &buf, container.CopyToContainerOptions{})
}

// SetupWithManager sets up the controller with the Manager.
func (r *DockerContainerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kdopv1alpha1.DockerContainer{}).
		Named("dockercontainer").
		Complete(r)
}
