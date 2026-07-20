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
	"io"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	dockerclient "github.com/docker/docker/client"
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

	// Deleting
	if !cr.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(cr, dockerContainerFinalizer) {
			cli, err := docker.NewClient(ctx, r.Client, cr.Namespace, cr.Spec.DockerHostRef)
			if err == nil {
				_ = removeByName(ctx, cli, name)
				cli.Close()
			}
			controllerutil.RemoveFinalizer(cr, dockerContainerFinalizer)
			return ctrl.Result{}, r.Update(ctx, cr)
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer (first pass)
	if !controllerutil.ContainsFinalizer(cr, dockerContainerFinalizer) {
		controllerutil.AddFinalizer(cr, dockerContainerFinalizer)
		return ctrl.Result{}, r.Update(ctx, cr)
	}

	cli, err := docker.NewClient(ctx, r.Client, cr.Namespace, cr.Spec.DockerHostRef)
	if err != nil {
		log.Error(err, "docker client")
		return ctrl.Result{}, err
	}
	defer cli.Close()

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
		for _, n := range list[i].Names {
			if n == want {
				match = &list[i]
				break
			}
		}
	}

	if match == nil {
		return r.create(ctx, cli, cr, name)
	}

	inspect, err := cli.ContainerInspect(ctx, match.ID)
	if err != nil {
		return err
	}

	if inspect.Config != nil && !strings.Contains(inspect.Config.Image, cr.Spec.Image) {
		_ = cli.ContainerRemove(ctx, match.ID, container.RemoveOptions{Force: true})
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
	out, err := cli.ImagePull(ctx, cr.Spec.Image, image.PullOptions{})
	if err != nil {
		return err
	}
	defer out.Close()
	_, _ = io.Copy(io.Discard, out)

	policy := cr.Spec.RestartPolicy
	if policy == "" {
		policy = "always"
	}

	resp, err := cli.ContainerCreate(ctx,
		&container.Config{Image: cr.Spec.Image},
		&container.HostConfig{
			RestartPolicy: container.RestartPolicy{
				Name: container.RestartPolicyMode(strings.ToLower(policy)),
			},
		},
		nil, nil, name,
	)
	if err != nil {
		return err
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
	if inspect.NetworkSettings != nil {
		for _, n := range inspect.NetworkSettings.Networks {
			if n.IPAddress != "" {
				cr.Status.IPv4 = n.IPAddress
				break
			}
		}
	}
	return r.Status().Update(ctx, cr)
}

func removeByName(ctx context.Context, cli dockerclient.APIClient, name string) error {
	want := "/" + name
	list, err := cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return err
	}
	for _, c := range list {
		for _, n := range c.Names {
			if n == want {
				return cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true})
			}
		}
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *DockerContainerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kdopv1alpha1.DockerContainer{}).
		Named("dockercontainer").
		Complete(r)
}
