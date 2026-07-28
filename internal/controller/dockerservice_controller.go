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
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	dockerclient "github.com/docker/docker/client"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	kdopv1alpha1 "github.com/minhtri1612/kdop-operator/api/v1alpha1"
	"github.com/minhtri1612/kdop-operator/internal/docker"
)

const tunnelWSPort int32 = 8081
const tunnelGatewayPort = 30000

// DockerServiceReconciler reconciles a DockerService object
type DockerServiceReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=kdop.kdop.io.vn,resources=dockerservices,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kdop.kdop.io.vn,resources=dockerservices/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kdop.kdop.io.vn,resources=dockerservices/finalizers,verbs=update
// +kubebuilder:rbac:groups=kdop.kdop.io.vn,resources=dockercontainers,verbs=get;list;watch
// +kubebuilder:rbac:groups=kdop.kdop.io.vn,resources=dockerhosts,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete

func (r *DockerServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	ds := &kdopv1alpha1.DockerService{}
	if err := r.Get(ctx, req.NamespacedName, ds); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	targets, result, err := r.resolveTargetContainers(ctx, ds)
	if err != nil {
		log.Error(err, "failed to resolve target containers")
		_ = r.setDockerServiceStatus(ctx, ds, "Error", 0, "", err.Error())
		return ctrl.Result{}, err
	}
	if result != nil {
		return *result, nil
	}

	if len(ds.Spec.Ports) == 0 {
		_ = r.setDockerServiceStatus(ctx, ds, "Error", 0, "", "spec.ports must not be empty")
		return ctrl.Result{}, nil
	}

	targetPort := ds.Spec.Ports[0].TargetPort
	targetIPs := buildTargetIPs(targets, targetPort)

	desiredReplicas := int32(1)
	if len(targetIPs) == 0 {
		desiredReplicas = 0
	}

	wsURL, err := r.reconcileTunnelServer(ctx, ds, desiredReplicas, targetIPs)
	if err != nil {
		log.Error(err, "failed to reconcile tunnel server")
		_ = r.setDockerServiceStatus(ctx, ds, "Error", 0, "", err.Error())
		return ctrl.Result{}, err
	}

	// 4.6 — SINGLE tunnel client on Docker host (first target = representative)
	if len(targets) > 0 && len(targetIPs) > 0 {
		first := &targets[0]
		cli, err := docker.NewClient(ctx, r.Client, first.Namespace, first.Spec.DockerHostRef)
		if err != nil {
			log.Error(err, "failed to get docker client for tunnel client")
			_ = r.setDockerServiceStatus(ctx, ds, "Error", 0, wsURL, err.Error())
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}
		defer cli.Close()

		if err := r.reconcileTunnelClient(ctx, cli, ds, first, wsURL); err != nil {
			log.Error(err, "failed to reconcile tunnel client")
			_ = r.setDockerServiceStatus(ctx, ds, "Error", 0, wsURL, err.Error())
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}

		_ = r.setDockerServiceStatus(ctx, ds, "Active", 1, wsURL,
			fmt.Sprintf("tunnel client ready; targetIPs=%v", targetIPs))
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	_ = r.setDockerServiceStatus(ctx, ds, "Pending", 0, wsURL,
		fmt.Sprintf("waiting for target IPv4; containers=%d", len(targets)))
	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

func buildTargetIPs(containers []kdopv1alpha1.DockerContainer, targetPort int32) []string {
	var ips []string
	for _, tc := range containers {
		if tc.Status.IPv4 != "" {
			ips = append(ips, fmt.Sprintf("%s:%d", tc.Status.IPv4, targetPort))
		}
	}
	return ips
}

func (r *DockerServiceReconciler) reconcileTunnelServer(
	ctx context.Context,
	ds *kdopv1alpha1.DockerService,
	replicas int32,
	targetIPs []string,
) (string, error) {
	name := "tunnel-" + ds.Name
	namespace := ds.Namespace
	log := logf.FromContext(ctx)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{"app": name},
		},
	}

	opResult, err := ctrl.CreateOrUpdate(ctx, r.Client, svc, func() error {
		svc.Spec.Selector = map[string]string{"app": name}
		ports := []corev1.ServicePort{
			{Name: "ws", Port: tunnelWSPort, TargetPort: intstr.FromInt32(tunnelWSPort)},
		}
		for _, p := range ds.Spec.Ports {
			ports = append(ports, corev1.ServicePort{
				Name:       p.Name,
				Port:       p.Port,
				TargetPort: intstr.FromInt32(p.Port),
			})
		}
		svc.Spec.Ports = ports
		return ctrl.SetControllerReference(ds, svc, r.Scheme)
	})
	if err != nil {
		return "", err
	}
	if opResult != controllerutil.OperationResultNone {
		log.Info("tunnel Service reconciled", "operation", opResult)
	}

	authToken, err := r.ensureTunnelAuthSecret(ctx, ds)
	if err != nil {
		return "", err
	}

	cmName := name + "-targets"
	sort.Strings(targetIPs)
	targetsCSV := strings.Join(targetIPs, ",")

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: namespace,
		},
	}
	opResult, err = ctrl.CreateOrUpdate(ctx, r.Client, cm, func() error {
		if cm.Data == nil {
			cm.Data = map[string]string{}
		}
		cm.Data["targets"] = targetsCSV
		return ctrl.SetControllerReference(ds, cm, r.Scheme)
	})
	if err != nil {
		return "", err
	}
	if opResult != controllerutil.OperationResultNone {
		log.Info("tunnel ConfigMap reconciled", "operation", opResult, "targets", targetsCSV)
	}
	if opResult == controllerutil.OperationResultUpdated {
		r.triggerTunnelReload(ctx, namespace, name, targetsCSV)
	}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	opResult, err = ctrl.CreateOrUpdate(ctx, r.Client, dep, func() error {
		dep.Labels = map[string]string{"app": name}
		dep.Spec.Replicas = &replicas
		dep.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: map[string]string{"app": name},
		}

		args := []string{
			"tunnel",
			"-mode=server",
			fmt.Sprintf("-ws-addr=:%d", tunnelWSPort),
			"-auth-token=" + authToken,
			"-targets-file=/etc/tunnel/targets",
		}
		for _, p := range ds.Spec.Ports {
			args = append(args, fmt.Sprintf("-listen-addr=:%d", p.Port))
		}

		containerPorts := []corev1.ContainerPort{
			{Name: "ws", ContainerPort: tunnelWSPort},
		}
		for _, p := range ds.Spec.Ports {
			containerPorts = append(containerPorts, corev1.ContainerPort{
				Name:          p.Name,
				ContainerPort: p.Port,
			})
		}

		imageName := operatorImage()

		dep.Spec.Template = corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{"app": name},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Name:            "server",
					Image:           imageName,
					ImagePullPolicy: corev1.PullIfNotPresent,
					Args:            args,
					Ports:           containerPorts,
					VolumeMounts: []corev1.VolumeMount{{
						Name:      "targets",
						MountPath: "/etc/tunnel",
					}},
				}},
				Volumes: []corev1.Volume{{
					Name: "targets",
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: cmName},
							Items: []corev1.KeyToPath{{
								Key:  "targets",
								Path: "targets",
							}},
						},
					},
				}},
			},
		}
		return ctrl.SetControllerReference(ds, dep, r.Scheme)
	})
	if err != nil {
		return "", err
	}
	if opResult != controllerutil.OperationResultNone {
		log.Info("tunnel Deployment reconciled", "operation", opResult, "replicas", replicas)
	}

	return fmt.Sprintf("ws://%s.%s.svc:%d/ws", name, namespace, tunnelWSPort), nil
}

func operatorImage() string {
	if img := os.Getenv("OPERATOR_IMAGE"); img != "" {
		return img
	}
	return "kdop-operator:latest"
}

func (r *DockerServiceReconciler) reconcileTunnelClient(
	ctx context.Context,
	cli dockerclient.APIClient,
	ds *kdopv1alpha1.DockerService,
	rep *kdopv1alpha1.DockerContainer,
	wsURL string,
) error {
	log := logf.FromContext(ctx)

	mainContainer, err := cli.ContainerInspect(ctx, rep.Spec.ContainerName)
	if err != nil {
		if dockerclient.IsErrNotFound(err) {
			log.Info("representative container not found, skip tunnel client")
			return nil
		}
		return err
	}

	targetNetworkID := ""
	for _, netConf := range mainContainer.NetworkSettings.Networks {
		targetNetworkID = netConf.NetworkID
		break
	}

	gatewayHost := "localhost"
	netMode := ds.Spec.NetworkMode
	if netMode == "kind" {
		nodes := &corev1.NodeList{}
		if err := r.List(ctx, nodes, client.MatchingLabels{"node-role.kubernetes.io/control-plane": ""}); err == nil && len(nodes.Items) > 0 {
			gatewayHost = nodes.Items[0].Name
		}
	}

	authToken, err := r.ensureTunnelAuthSecret(ctx, ds)
	if err != nil {
		return err
	}

	img := operatorImage()
	if _, _, err := cli.ImageInspectWithRaw(ctx, img); err != nil {
		reader, pullErr := cli.ImagePull(ctx, img, image.PullOptions{})
		if pullErr == nil {
			_, _ = io.Copy(io.Discard, reader)
			_ = reader.Close()
		} else {
			log.Error(pullErr, "failed to pull tunnel image (may be local only)")
		}
	}

	clientConnectURL := fmt.Sprintf(
		"ws://%s:%d/ws?target=%s&token=%s",
		gatewayHost,
		tunnelGatewayPort,
		url.QueryEscape(wsURL),
		url.QueryEscape(authToken),
	)

	tunnelName := fmt.Sprintf("tunnel-client-%s", ds.Name)

	existing, err := cli.ContainerInspect(ctx, tunnelName)
	if err == nil {
		if existing.Config.Image != img {
			log.Info("tunnel client image drift, recreating", "old", existing.Config.Image, "new", img)
			if err := cli.ContainerRemove(ctx, tunnelName, container.RemoveOptions{Force: true}); err != nil {
				return err
			}
		} else {
			if !existing.State.Running {
				if err := cli.ContainerStart(ctx, tunnelName, container.StartOptions{}); err != nil {
					return err
				}
			}
			log.Info("tunnel client already running", "name", tunnelName, "url", clientConnectURL)
			return nil
		}
	} else if !dockerclient.IsErrNotFound(err) {
		return err
	}

	log.Info("creating tunnel client", "name", tunnelName, "url", clientConnectURL)

	cfg := &container.Config{
		Image: img,
		Cmd: []string{
			"tunnel",
			"-mode=client",
			"-server-url=" + clientConnectURL,
			"-auth-token=" + authToken,
		},
	}
	hostCfg := &container.HostConfig{
		RestartPolicy: container.RestartPolicy{Name: "always"},
	}
	switch netMode {
	case "host":
		hostCfg.NetworkMode = "host"
	case "kind":
		hostCfg.NetworkMode = "kind"
	}

	if _, err := cli.ContainerCreate(ctx, cfg, hostCfg, nil, nil, tunnelName); err != nil {
		return err
	}
	if err := cli.ContainerStart(ctx, tunnelName, container.StartOptions{}); err != nil {
		return err
	}

	if targetNetworkID != "" && targetNetworkID != "host" && targetNetworkID != "none" {
		if err := cli.NetworkConnect(ctx, targetNetworkID, tunnelName, nil); err != nil {
			log.Info("network connect (may already exist)", "error", err)
		}
	}

	return nil
}

func (r *DockerServiceReconciler) triggerTunnelReload(ctx context.Context, namespace, appName, targetsCSV string) {
	log := logf.FromContext(ctx)
	log.Info("triggering tunnel reload", "app", appName, "targets", targetsCSV)

	podList := &corev1.PodList{}
	if err := r.List(ctx, podList, client.InNamespace(namespace), client.MatchingLabels{"app": appName}); err != nil {
		log.Error(err, "failed to list tunnel pods for reload")
		return
	}
	if len(podList.Items) == 0 {
		log.Info("no active tunnel pods for reload", "app", appName)
		return
	}

	for _, pod := range podList.Items {
		if pod.Status.Phase != corev1.PodRunning || pod.Status.PodIP == "" {
			continue
		}
		ip := pod.Status.PodIP
		go func(ip string) {
			u := fmt.Sprintf("http://%s:%d/reload?targets=%s", ip, tunnelWSPort, url.QueryEscape(targetsCSV))
			resp, err := http.Post(u, "application/json", nil)
			if err != nil {
				log.Error(err, "failed to trigger reload", "ip", ip)
				return
			}
			_ = resp.Body.Close()
			log.Info("triggered tunnel reload", "ip", ip)
		}(ip)
	}
}

func (r *DockerServiceReconciler) ensureTunnelAuthSecret(
	ctx context.Context,
	ds *kdopv1alpha1.DockerService,
) (string, error) {
	secretName := ds.Name + "-tunnel-auth"
	secret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Namespace: ds.Namespace, Name: secretName}, secret)
	if err == nil {
		if token, ok := secret.Data["token"]; ok {
			return string(token), nil
		}
	} else if !errors.IsNotFound(err) {
		return "", err
	}

	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", err
	}
	token := hex.EncodeToString(tokenBytes)

	secret = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: ds.Namespace,
		},
		Data: map[string][]byte{"token": []byte(token)},
	}
	if err := ctrl.SetControllerReference(ds, secret, r.Scheme); err != nil {
		return "", err
	}
	if err := r.Create(ctx, secret); err != nil {
		return "", err
	}
	return token, nil
}

func (r *DockerServiceReconciler) resolveTargetContainers(
	ctx context.Context,
	ds *kdopv1alpha1.DockerService,
) ([]kdopv1alpha1.DockerContainer, *ctrl.Result, error) {
	log := logf.FromContext(ctx)

	hasRef := ds.Spec.ContainerRef != ""
	hasSelector := ds.Spec.Selector != nil

	if hasRef && hasSelector {
		msg := "spec must set either containerRef or selector, not both"
		_ = r.setDockerServiceStatus(ctx, ds, "Error", 0, "", msg)
		return nil, &ctrl.Result{}, nil
	}
	if !hasRef && !hasSelector {
		msg := "spec must set containerRef or selector"
		_ = r.setDockerServiceStatus(ctx, ds, "Error", 0, "", msg)
		return nil, &ctrl.Result{}, nil
	}

	if hasSelector {
		selector, err := metav1.LabelSelectorAsSelector(ds.Spec.Selector)
		if err != nil {
			msg := fmt.Sprintf("invalid selector: %v", err)
			_ = r.setDockerServiceStatus(ctx, ds, "Error", 0, "", msg)
			return nil, nil, err
		}

		var list kdopv1alpha1.DockerContainerList
		if err := r.List(ctx, &list,
			client.InNamespace(ds.Namespace),
			client.MatchingLabelsSelector{Selector: selector},
		); err != nil {
			return nil, nil, err
		}

		if len(list.Items) == 0 {
			msg := "no matching DockerContainers found for selector"
			log.Info(msg)
			_ = r.setDockerServiceStatus(ctx, ds, "Pending", 0, "", msg)
			return nil, &ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}

		return list.Items, nil, nil
	}

	dc := &kdopv1alpha1.DockerContainer{}
	key := types.NamespacedName{Namespace: ds.Namespace, Name: ds.Spec.ContainerRef}
	if err := r.Get(ctx, key, dc); err != nil {
		if errors.IsNotFound(err) {
			msg := fmt.Sprintf("DockerContainer %q not found", ds.Spec.ContainerRef)
			log.Info(msg)
			_ = r.setDockerServiceStatus(ctx, ds, "Pending", 0, "", msg)
			return nil, &ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}
		return nil, nil, err
	}

	return []kdopv1alpha1.DockerContainer{*dc}, nil, nil
}

func (r *DockerServiceReconciler) setDockerServiceStatus(
	ctx context.Context,
	ds *kdopv1alpha1.DockerService,
	phase string,
	clients int,
	wsURL, message string,
) error {
	patch := client.MergeFrom(ds.DeepCopy())
	ds.Status.Phase = phase
	ds.Status.TunnelClients = clients
	ds.Status.TunnelServerURL = wsURL
	ds.Status.Message = message
	return r.Status().Patch(ctx, ds, patch)
}

func (r *DockerServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kdopv1alpha1.DockerService{}).
		Complete(r)
}
