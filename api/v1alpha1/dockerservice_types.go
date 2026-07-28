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

// DockerServiceSpec defines the desired state of DockerService
type DockerServiceSpec struct {
	// ContainerRef names one DockerContainer (mutually exclusive with Selector)
	// +optional
	ContainerRef string `json:"containerRef,omitempty"`
	// Selector matches multiple DockerContainers for load balancing
	// +optional
	Selector *metav1.LabelSelector `json:"selector,omitempty"`
	// Ports maps K8s Service port -> container port
	// +kubebuilder:validation:MinItems=1
	Ports []ServicePort `json:"ports"`
	// NetworkMode for tunnel client (e.g. "kind", "bridge")
	// +optional
	NetworkMode string `json:"networkMode,omitempty"`
}

// ServicePort defines a port mapping
type ServicePort struct {
	// Port exposed on the Kubernetes Service
	Port int32 `json:"port"`
	// TargetPort on the Docker container
	TargetPort int32 `json:"targetPort"`
	// Name of the port
	// +optional
	Name string `json:"name,omitempty"`
}

// DockerServiceStatus defines the observed state of DockerService
type DockerServiceStatus struct {
	// Phase: Pending | Active | Error
	Phase string `json:"phase,omitempty"`
	// TunnelClients is the count of active tunnel client containers
	TunnelClients int `json:"tunnelClients,omitempty"`
	// TunnelServerURL is the internal WebSocket URL of the tunnel server
	TunnelServerURL string `json:"tunnelServerURL,omitempty"`
	// Message describes the last error or status detail
	Message string `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// DockerService is the Schema for the dockerservices API
type DockerService struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of DockerService
	// +required
	Spec DockerServiceSpec `json:"spec"`

	// status defines the observed state of DockerService
	// +optional
	Status DockerServiceStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// DockerServiceList contains a list of DockerService
type DockerServiceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []DockerService `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DockerService{}, &DockerServiceList{})
}
