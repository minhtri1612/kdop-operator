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
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
	"github.com/google/go-cmp/cmp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kdopv1alpha1 "github.com/minhtri1612/kdop-operator/api/v1alpha1"
)

func TestRuntimeSnapshotFromInspect(t *testing.T) {
	inspect := types.ContainerJSON{
		ContainerJSONBase: &types.ContainerJSONBase{
			Name: "my-app",
			State: &types.ContainerState{
				Health: &types.Health{
					Status: "healthy",
				},
			},
			HostConfig: &container.HostConfig{
				RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
				Resources: container.Resources{
					NanoCPUs: 500000000,
					Memory:   268435456,
				},
			},
		},
		Config: &container.Config{
			Image: "nginx:alpine",
			Cmd:   []string{"nginx", "-g", "daemon off;"},
			Env:   []string{"B=2", "A=1"},
		},
		NetworkSettings: &types.NetworkSettings{
			NetworkSettingsBase: types.NetworkSettingsBase{
				Ports: nat.PortMap{
					"80/tcp": []nat.PortBinding{{HostPort: "8080"}},
				},
			},
			Networks: map[string]*network.EndpointSettings{
				"bridge": {IPAddress: "172.17.0.2"},
			},
		},
	}
	summary := types.Container{
		ID:    "abc123",
		State: "running",
		Names: []string{"/my-app"},
	}

	got := runtimeSnapshotFromInspect("friend-vps", summary, inspect)

	if got.Name != "my-app" {
		t.Fatalf("expected name my-app, got %q", got.Name)
	}
	if got.Health != "healthy" {
		t.Fatalf("expected healthy status, got %q", got.Health)
	}
	if got.IPv4 != "172.17.0.2" {
		t.Fatalf("expected IPv4 172.17.0.2, got %q", got.IPv4)
	}

	wantSpec := kdopv1alpha1.DockerContainerSpec{
		Image:          "nginx:alpine",
		ContainerName:  "my-app",
		DockerHostRef:  "friend-vps",
		RestartPolicy:  "unless-stopped",
		Command:        []string{"nginx", "-g", "daemon off;"},
		Env:            []string{"A=1", "B=2"},
		Ports:          []string{"8080:80"},
		ManagementMode: string(kdopv1alpha1.DockerContainerManagementModeObserve),
		Resources: &kdopv1alpha1.ResourceRequirements{
			CPULimit:    "0.5",
			MemoryLimit: "256m",
		},
	}
	if diff := cmp.Diff(wantSpec, got.Spec); diff != "" {
		t.Fatalf("unexpected spec diff (-want +got):\n%s", diff)
	}
}

func TestObserveRuntimeMarksMissingWithoutCreating(t *testing.T) {
	scheme := newTestScheme(t)
	resource := &kdopv1alpha1.DockerContainer{
		ObjectMeta: metav1.ObjectMeta{Name: "legacy-app", Namespace: "default"},
		Spec: kdopv1alpha1.DockerContainerSpec{
			ContainerName:  "legacy-app",
			DockerHostRef:  "friend-vps",
			ManagementMode: string(kdopv1alpha1.DockerContainerManagementModeObserve),
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(resource).
		WithObjects(resource).
		Build()

	reconciler := &DockerContainerReconciler{Client: k8sClient, Scheme: scheme}
	if err := reconciler.observeRuntime(context.Background(), nil, resource, nil); err != nil {
		t.Fatalf("observeRuntime returned error: %v", err)
	}

	stored := &kdopv1alpha1.DockerContainer{}
	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(resource), stored); err != nil {
		t.Fatalf("get stored resource: %v", err)
	}
	if stored.Status.State != dockerContainerMissing {
		t.Fatalf("expected state %q, got %q", dockerContainerMissing, stored.Status.State)
	}
	if !stored.Status.Adopted {
		t.Fatalf("expected adopted status to be true")
	}
}

func TestUpsertAdoptedContainerCreatesOnceAndUpdatesStatus(t *testing.T) {
	scheme := newTestScheme(t)
	host := &kdopv1alpha1.DockerHost{
		ObjectMeta: metav1.ObjectMeta{Name: "friend-vps", Namespace: "system"},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&kdopv1alpha1.DockerContainer{}).
		WithObjects(host).
		Build()

	reconciler := &DockerHostReconciler{Client: k8sClient, Scheme: scheme}
	snapshot := runtimeContainerSnapshot{
		ID:    "cid-1",
		Name:  "legacy-app",
		State: "running",
		Spec: kdopv1alpha1.DockerContainerSpec{
			Image:          "nginx:alpine",
			ContainerName:  "legacy-app",
			DockerHostRef:  "friend-vps",
			ManagementMode: string(kdopv1alpha1.DockerContainerManagementModeObserve),
		},
	}

	if err := reconciler.upsertAdoptedContainer(context.Background(), host, snapshot); err != nil {
		t.Fatalf("first upsert failed: %v", err)
	}
	if err := reconciler.upsertAdoptedContainer(context.Background(), host, snapshot); err != nil {
		t.Fatalf("second upsert failed: %v", err)
	}

	var list kdopv1alpha1.DockerContainerList
	if err := k8sClient.List(context.Background(), &list, client.InNamespace("system")); err != nil {
		t.Fatalf("list adopted resources: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 adopted resource, got %d", len(list.Items))
	}

	adopted := list.Items[0]
	if adopted.Spec.ManagementMode != string(kdopv1alpha1.DockerContainerManagementModeObserve) {
		t.Fatalf("expected observe mode, got %q", adopted.Spec.ManagementMode)
	}
	if adopted.Labels[managedByLabelKey] != managedByAdoptValue {
		t.Fatalf("expected managed-by adopt label")
	}
	if adopted.Annotations[adoptedFromAnnotationKey] != "friend-vps/cid-1" {
		t.Fatalf("unexpected adopted-from annotation: %q", adopted.Annotations[adoptedFromAnnotationKey])
	}
	if !adopted.Status.Adopted {
		t.Fatalf("expected adopted status to be true")
	}
	if adopted.Status.ObservedSpecHash == "" {
		t.Fatalf("expected observed spec hash to be populated")
	}
}

func TestShouldSkipRuntimeDeletion(t *testing.T) {
	cr := &kdopv1alpha1.DockerContainer{
		Spec: kdopv1alpha1.DockerContainerSpec{
			ManagementMode: string(kdopv1alpha1.DockerContainerManagementModeObserve),
		},
		Status: kdopv1alpha1.DockerContainerStatus{
			Adopted: true,
		},
	}
	if !shouldSkipRuntimeDeletion(cr) {
		t.Fatalf("expected observe adopted resource to skip runtime deletion")
	}

	cr.Spec.ManagementMode = string(kdopv1alpha1.DockerContainerManagementModeEnforce)
	if shouldSkipRuntimeDeletion(cr) {
		t.Fatalf("expected enforce mode to allow runtime deletion")
	}
}

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := kdopv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	return scheme
}
