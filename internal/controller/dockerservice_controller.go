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
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	kdopv1alpha1 "github.com/minhtri1612/kdop-operator/api/v1alpha1"
)

// DockerServiceReconciler reconciles a DockerService object
type DockerServiceReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=kdop.kdop.io.vn,resources=dockerservices,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kdop.kdop.io.vn,resources=dockerservices/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kdop.kdop.io.vn,resources=dockerservices/finalizers,verbs=update
// +kubebuilder:rbac:groups=kdop.kdop.io.vn,resources=dockercontainers,verbs=get;list;watch

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
		_ = r.setDockerServiceStatus(ctx, ds, "Error", err.Error())
		return ctrl.Result{}, err
	}
	if result != nil {
		return *result, nil
	}

	if len(ds.Spec.Ports) == 0 {
		_ = r.setDockerServiceStatus(ctx, ds, "Error", "spec.ports must not be empty")
		return ctrl.Result{}, nil
	}

	targetPort := ds.Spec.Ports[0].TargetPort
	targetIPs := buildTargetIPs(targets, targetPort)

	names := make([]string, 0, len(targets))
	for _, tc := range targets {
		names = append(names, tc.Name)
	}

	msg := fmt.Sprintf(
		"resolved %d target container(s): %s; targetIPs=%v",
		len(targets),
		strings.Join(names, ", "),
		targetIPs,
	)
	log.Info(msg)
	_ = r.setDockerServiceStatus(ctx, ds, "Pending", msg)

	// 4.4+ tunnel server/client logic sẽ dùng targetIPs
	_ = targetIPs

	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
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

func (r *DockerServiceReconciler) resolveTargetContainers(
	ctx context.Context,
	ds *kdopv1alpha1.DockerService,
) ([]kdopv1alpha1.DockerContainer, *ctrl.Result, error) {
	log := logf.FromContext(ctx)

	hasRef := ds.Spec.ContainerRef != ""
	hasSelector := ds.Spec.Selector != nil

	if hasRef && hasSelector {
		msg := "spec must set either containerRef or selector, not both"
		_ = r.setDockerServiceStatus(ctx, ds, "Error", msg)
		return nil, &ctrl.Result{}, nil
	}
	if !hasRef && !hasSelector {
		msg := "spec must set containerRef or selector"
		_ = r.setDockerServiceStatus(ctx, ds, "Error", msg)
		return nil, &ctrl.Result{}, nil
	}

	if hasSelector {
		selector, err := metav1.LabelSelectorAsSelector(ds.Spec.Selector)
		if err != nil {
			msg := fmt.Sprintf("invalid selector: %v", err)
			_ = r.setDockerServiceStatus(ctx, ds, "Error", msg)
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
			_ = r.setDockerServiceStatus(ctx, ds, "Pending", msg)
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
			_ = r.setDockerServiceStatus(ctx, ds, "Pending", msg)
			return nil, &ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}
		return nil, nil, err
	}

	return []kdopv1alpha1.DockerContainer{*dc}, nil, nil
}

func (r *DockerServiceReconciler) setDockerServiceStatus(
	ctx context.Context,
	ds *kdopv1alpha1.DockerService,
	phase, message string,
) error {
	patch := client.MergeFrom(ds.DeepCopy())
	ds.Status.Phase = phase
	ds.Status.Message = message
	return r.Status().Patch(ctx, ds, patch)
}

func (r *DockerServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kdopv1alpha1.DockerService{}).
		Complete(r)
}
