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
	"fmt"
	"math/rand"
	"sort"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	kdopv1alpha1 "github.com/minhtri1612/kdop-operator/api/v1alpha1"
)

const deployRequeueInterval = 10 * time.Second

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyz0123456789")

// DockerDeploymentReconciler reconciles a DockerDeployment object
type DockerDeploymentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=kdop.kdop.io.vn,resources=dockerdeployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kdop.kdop.io.vn,resources=dockerdeployments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kdop.kdop.io.vn,resources=dockerdeployments/finalizers,verbs=update
// +kubebuilder:rbac:groups=kdop.kdop.io.vn,resources=dockercontainers,verbs=get;list;watch;create;update;patch;delete

func (r *DockerDeploymentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := log.FromContext(ctx)

	deployment := &kdopv1alpha1.DockerDeployment{}
	if err := r.Get(ctx, req.NamespacedName, deployment); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !deployment.ObjectMeta.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	replicas := int32(1)
	if deployment.Spec.Replicas != nil {
		replicas = *deployment.Spec.Replicas
	}

	owned, err := r.listOwnedContainers(ctx, deployment)
	if err != nil {
		l.Error(err, "Failed to list owned containers")
		return ctrl.Result{}, err
	}

	currentCount := int32(len(owned))
	diff := replicas - currentCount

	if diff > 0 {
		l.Info("Scaling up", "current", currentCount, "desired", replicas, "adding", diff)
		for i := int32(0); i < diff; i++ {
			newContainer, err := r.constructContainer(deployment)
			if err != nil {
				l.Error(err, "Failed to construct container")
				return ctrl.Result{}, err
			}
			if err := r.Create(ctx, newContainer); err != nil {
				l.Error(err, "Failed to create container", "name", newContainer.Name)
				return ctrl.Result{}, err
			}
			l.Info("Created child DockerContainer", "name", newContainer.Name, "containerName", newContainer.Spec.ContainerName)
		}
	} else if diff < 0 {
		// 6.4 — Scale down (newest first)
		removeCount := int(-diff)
		l.Info("Scaling down", "current", currentCount, "desired", replicas, "removing", removeCount)
		for i := 0; i < removeCount; i++ {
			victim := owned[len(owned)-1-i]
			if err := r.Delete(ctx, &victim); err != nil {
				l.Error(err, "Failed to delete container", "name", victim.Name)
				return ctrl.Result{}, err
			}
			l.Info("Deleted child DockerContainer", "name", victim.Name)
		}
	}

	owned, err = r.listOwnedContainers(ctx, deployment)
	if err != nil {
		return ctrl.Result{}, err
	}
	names := make([]string, 0, len(owned))
	for _, c := range owned {
		names = append(names, c.Name)
	}
	l.Info("listed owned DockerContainers", "count", len(owned), "names", names)

	return ctrl.Result{RequeueAfter: deployRequeueInterval}, nil
}

func (r *DockerDeploymentReconciler) listOwnedContainers(ctx context.Context, deployment *kdopv1alpha1.DockerDeployment) ([]kdopv1alpha1.DockerContainer, error) {
	var all kdopv1alpha1.DockerContainerList
	if err := r.List(ctx, &all, client.InNamespace(deployment.Namespace)); err != nil {
		return nil, err
	}

	var owned []kdopv1alpha1.DockerContainer
	for i := range all.Items {
		child := all.Items[i]
		if metav1.IsControlledBy(&child, deployment) {
			owned = append(owned, child)
		}
	}

	sort.Slice(owned, func(i, j int) bool {
		return owned[i].CreationTimestamp.Before(&owned[j].CreationTimestamp)
	})

	return owned, nil
}

func (r *DockerDeploymentReconciler) constructContainer(deploy *kdopv1alpha1.DockerDeployment) (*kdopv1alpha1.DockerContainer, error) {
	suffix := utilRandString(5)
	name := fmt.Sprintf("%s-%s", deploy.Name, suffix)

	labels := map[string]string{}
	for k, v := range deploy.Spec.Template.Metadata.Labels {
		labels[k] = v
	}

	container := &kdopv1alpha1.DockerContainer{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: deploy.Namespace,
			Labels:    labels,
		},
		Spec: deploy.Spec.Template.Spec,
	}

	if err := controllerutil.SetControllerReference(deploy, container, r.Scheme); err != nil {
		return nil, err
	}

	if container.Spec.ContainerName == "" {
		container.Spec.ContainerName = name
	} else {
		container.Spec.ContainerName = fmt.Sprintf("%s-%s", container.Spec.ContainerName, suffix)
	}

	return container, nil
}

func utilRandString(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}

func (r *DockerDeploymentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kdopv1alpha1.DockerDeployment{}).
		Named("dockerdeployment").
		Complete(r)
}
