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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// DockerContainerSpec defines the desired state of DockerContainer
type DockerContainerSpec struct {
	// Image to run (required)
	// +kubebuilder:validation:MinLength=1
	Image string `json:"image"`
	// ContainerName on the Docker host. Defaults to metadata.name if empty.
	// +optional
	ContainerName string `json:"containerName,omitempty"`
	// DockerHostRef names a DockerHost in the same namespace.
	// Empty = local unix socket (/var/run/docker.sock)
	// +optional
	DockerHostRef string `json:"dockerHostRef,omitempty"`
	// RestartPolicy: no | on-failure | always | unless-stopped
	// +kubebuilder:validation:Enum=no;on-failure;always;unless-stopped
	// +kubebuilder:default=always
	// +optional
	RestartPolicy string `json:"restartPolicy,omitempty"`
}

// DockerContainerStatus defines the observed state of DockerContainer
type DockerContainerStatus struct {
	// ID is the Docker container ID
	ID string `json:"id,omitempty"`
	// State: running | exited | created | ...
	State string `json:"state,omitempty"`
	// IPv4 on the Docker network (optional, phase sau tunnel)
	IPv4 string `json:"ipv4,omitempty"`
}

// +kubebuilder:printcolumn:name="State",type=string,JSONPath=`.status.state`
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.spec.image`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type DockerContainer struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of DockerContainer
	// +required
	Spec DockerContainerSpec `json:"spec"`

	// status defines the observed state of DockerContainer
	// +optional
	Status DockerContainerStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// DockerContainerList contains a list of DockerContainer
type DockerContainerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []DockerContainer `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DockerContainer{}, &DockerContainerList{})
}
