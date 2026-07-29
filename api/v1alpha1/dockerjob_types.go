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

type DockerJobPhase string

const (
	JobPhasePending   DockerJobPhase = "Pending"
	JobPhaseRunning   DockerJobPhase = "Running"
	JobPhaseSucceeded DockerJobPhase = "Succeeded"
	JobPhaseFailed    DockerJobPhase = "Failed"
)

// DockerJobSpec defines the desired state of DockerJob
type DockerJobSpec struct {
	// Image is the Docker image to run
	// +kubebuilder:validation:MinLength=1
	Image string `json:"image"`

	// ContainerName on the Docker host (default: job-<metadata.name>)
	// +optional
	ContainerName string `json:"containerName,omitempty"`

	// Command overrides ENTRYPOINT
	// +optional
	Command []string `json:"command,omitempty"`

	// Args overrides CMD
	// +optional
	Args []string `json:"args,omitempty"`

	// Env literal KEY=VALUE
	// +optional
	Env []string `json:"env,omitempty"`

	// EnvVars with SecretKeyRef support
	// +optional
	EnvVars []EnvVar `json:"envVars,omitempty"`

	// VolumeMounts bind mounts
	// +optional
	VolumeMounts []VolumeMount `json:"volumeMounts,omitempty"`

	// SecretVolumes upload Secret files into container
	// +optional
	SecretVolumes []SecretVolume `json:"secretVolumes,omitempty"`

	// DockerHostRef names the DockerHost CR
	// +optional
	DockerHostRef string `json:"dockerHostRef,omitempty"`

	// ImagePullSecret for private registry
	// +optional
	ImagePullSecret string `json:"imagePullSecret,omitempty"`

	// RestartPolicy: Never | OnFailure (default Never)
	// +optional
	RestartPolicy string `json:"restartPolicy,omitempty"`

	// BackoffLimit retries before Failed (default 0)
	// +optional
	BackoffLimit int32 `json:"backoffLimit,omitempty"`

	// ActiveDeadlineSeconds job timeout
	// +optional
	ActiveDeadlineSeconds *int64 `json:"activeDeadlineSeconds,omitempty"`

	// TTLSecondsAfterFinished auto-remove Docker container after completion
	// +optional
	TTLSecondsAfterFinished *int32 `json:"ttlSecondsAfterFinished,omitempty"`

	// Resources CPU/Memory limits
	// +optional
	Resources *ResourceRequirements `json:"resources,omitempty"`
}

// DockerJobStatus defines the observed state of DockerJob
type DockerJobStatus struct {
	Phase          DockerJobPhase `json:"phase,omitempty"`
	ContainerID    string         `json:"containerID,omitempty"`
	StartTime      *metav1.Time   `json:"startTime,omitempty"`
	CompletionTime *metav1.Time   `json:"completionTime,omitempty"`
	ExitCode       *int32         `json:"exitCode,omitempty"`
	Attempts       int32          `json:"attempts,omitempty"`
	Message        string         `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Exit Code",type=integer,JSONPath=`.status.exitCode`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// DockerJob runs a one-off container on a Docker host
type DockerJob struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec DockerJobSpec `json:"spec"`

	// +optional
	Status DockerJobStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// DockerJobList contains a list of DockerJob
type DockerJobList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []DockerJob `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DockerJob{}, &DockerJobList{})
}
