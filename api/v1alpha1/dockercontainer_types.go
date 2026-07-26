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
	// ImagePullSecret is a Secret with keys username, password, server
	// used to authenticate private registry pulls.
	// +optional
	ImagePullSecret string `json:"imagePullSecret,omitempty"`
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

	Ports []string `json:"ports,omitempty"`

	// Env is a list of literal "KEY=VALUE" entries.
	// +optional
	Env []string `json:"env,omitempty"`

	// EnvVars supports literal Value or ValueFrom.SecretKeyRef.
	// +optional
	EnvVars []EnvVar `json:"envVars,omitempty"`

	// VolumeMounts binds host paths into the container.
	// +optional
	VolumeMounts []VolumeMount `json:"volumeMounts,omitempty"`

	// SecretVolumes uploads K8s Secret keys as files into the container.
	// +optional
	SecretVolumes []SecretVolume `json:"secretVolumes,omitempty"`

	// HealthCheck configures Docker's native health check.
	// +optional
	HealthCheck *HealthCheckConfig `json:"healthCheck,omitempty"`

	// Resources sets CPU/Memory limits for the container.
	// +optional
	Resources *ResourceRequirements `json:"resources,omitempty"`

	// Command overrides the image ENTRYPOINT/CMD.
	// +optional
	Command []string `json:"command,omitempty"`
}

// DockerContainerStatus defines the observed state of DockerContainer
type DockerContainerStatus struct {
	// ID is the Docker container ID
	ID string `json:"id,omitempty"`
	// State: running | exited | created | ...
	State string `json:"state,omitempty"`
	// IPv4 on the Docker network (optional, phase sau tunnel)
	IPv4 string `json:"ipv4,omitempty"`
	// Health: healthy | unhealthy | starting | none
	Health string `json:"health,omitempty"`
}

// HealthCheckConfig defines Docker health check parameters.
type HealthCheckConfig struct {
	// Test is the check command, e.g. ["CMD-SHELL", "pidof nginx || exit 1"]
	Test []string `json:"test"`

	// Interval between checks (e.g. "5s")
	// +optional
	Interval string `json:"interval,omitempty"`

	// Timeout for one check (e.g. "3s")
	// +optional
	Timeout string `json:"timeout,omitempty"`

	// Retries before marking unhealthy
	// +optional
	Retries int `json:"retries,omitempty"`
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

// EnvVar defines an environment variable.
type EnvVar struct {
	// Name of the environment variable
	Name string `json:"name"`

	// Value is a literal value (optional if ValueFrom is set)
	// +optional
	Value string `json:"value,omitempty"`

	// ValueFrom reads the value from a K8s Secret
	// +optional
	ValueFrom *EnvVarSource `json:"valueFrom,omitempty"`
}

// VolumeMount binds a host path into the container.
type VolumeMount struct {
	// HostPath is the absolute path on the Docker host
	HostPath string `json:"hostPath"`
	// ContainerPath is the absolute path in the container
	ContainerPath string `json:"containerPath"`
	// ReadOnly mounts the volume read-only
	// +optional
	ReadOnly bool `json:"readOnly,omitempty"`
}

// EnvVarSource represents a source for the value of an EnvVar.
type EnvVarSource struct {
	// SecretKeyRef selects a key of a Secret in the same namespace
	// +optional
	SecretKeyRef *SecretKeySelector `json:"secretKeyRef,omitempty"`
}

// SecretKeySelector selects a key of a Secret.
type SecretKeySelector struct {
	// Name of the Secret
	Name string `json:"name"`
	// Key of the secret to select
	Key string `json:"key"`
}

// SecretVolume maps a K8s Secret into a directory inside the container.
type SecretVolume struct {
	// SecretName is the Secret in the same namespace
	SecretName string `json:"secretName"`
	// MountPath is the absolute path in the container (e.g. /etc/secrets)
	MountPath string `json:"mountPath"`
}

// ResourceRequirements defines CPU and Memory limits.
type ResourceRequirements struct {
	// CPULimit in cores, e.g. "0.5" or "2"
	// +optional
	CPULimit string `json:"cpuLimit,omitempty"`

	// MemoryLimit e.g. "256m", "1g"
	// +optional
	MemoryLimit string `json:"memoryLimit,omitempty"`
}

func init() {
	SchemeBuilder.Register(&DockerContainer{}, &DockerContainerList{})
}
