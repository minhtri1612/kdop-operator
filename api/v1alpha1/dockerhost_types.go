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

// DockerHostSpec defines the desired state of DockerHost
type DockerHostSpec struct {
	// HostURL is the Docker daemon endpoint.
	// Examples: unix:///var/run/docker.sock, tcp://host:2376
	// +kubebuilder:validation:MinLength=1
	HostURL string `json:"hostURL"`
	// TLSSecretName is a Secret (same namespace) with ca.pem, cert.pem, key.pem
	// +optional
	TLSSecretName string `json:"tlsSecretName,omitempty"`
}

// DockerHostStatus defines the observed state of DockerHost
type DockerHostStatus struct {
	// Phase: Connected | Error
	Phase string `json:"phase,omitempty"`
	// Message describes connection result or last error
	Message string `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Host",type=string,JSONPath=`.spec.hostURL`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type DockerHost struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of DockerHost
	// +required
	Spec DockerHostSpec `json:"spec"`

	// status defines the observed state of DockerHost
	// +optional
	Status DockerHostStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// DockerHostList contains a list of DockerHost
type DockerHostList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []DockerHost `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DockerHost{}, &DockerHostList{})
}
