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
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	kdopv1alpha1 "github.com/minhtri1612/kdop-operator/api/v1alpha1"
	"github.com/minhtri1612/kdop-operator/internal/docker"
)

// DockerHostReconciler reconciles a DockerHost object
type DockerHostReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=kdop.kdop.io.vn,resources=dockerhosts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kdop.kdop.io.vn,resources=dockerhosts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kdop.kdop.io.vn,resources=dockerhosts/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *DockerHostReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	host := &kdopv1alpha1.DockerHost{}
	if err := r.Get(ctx, req.NamespacedName, host); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	cli, err := docker.NewClientFromHost(ctx, r.Client, host)
	if err != nil {
		host.Status.Phase = "Error"
		host.Status.Message = err.Error()
		if updateErr := r.Status().Update(ctx, host); updateErr != nil {
			return ctrl.Result{}, updateErr
		}
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}
	defer func() { _ = cli.Close() }()

	phase := "Connected"
	msg := "Successfully connected to Docker daemon"
	if _, err := cli.Ping(ctx); err != nil {
		phase = "Error"
		msg = err.Error()
		log.Error(err, "docker ping failed", "hostURL", host.Spec.HostURL)
	}

	if host.Status.Phase != phase || host.Status.Message != msg {
		host.Status.Phase = phase
		host.Status.Message = msg
		if err := r.Status().Update(ctx, host); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *DockerHostReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kdopv1alpha1.DockerHost{}).
		Named("dockerhost").
		Complete(r)
}
