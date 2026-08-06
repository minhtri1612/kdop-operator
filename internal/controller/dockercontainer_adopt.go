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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/go-connections/nat"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kdopv1alpha1 "github.com/minhtri1612/kdop-operator/api/v1alpha1"
)

const (
	adoptLabelKey            = "kdop.io/adopt"
	adoptLabelValue          = "true"
	adoptedFromAnnotationKey = "kdop.kdop.io.vn/adopted-from"
	managedByLabelKey        = "kdop.kdop.io.vn/managed-by"
	managedByAdoptValue      = "adopt"
	dockerContainerMissing   = "missing"
)

type runtimeContainerSnapshot struct {
	ID                string
	Name              string
	State             string
	Health            string
	IPv4              string
	Spec              kdopv1alpha1.DockerContainerSpec
	RuntimeIncomplete bool
}

func managementModeFor(cr *kdopv1alpha1.DockerContainer) kdopv1alpha1.DockerContainerManagementMode {
	if cr.Spec.ManagementMode == string(kdopv1alpha1.DockerContainerManagementModeObserve) {
		return kdopv1alpha1.DockerContainerManagementModeObserve
	}
	return kdopv1alpha1.DockerContainerManagementModeEnforce
}

func shouldSkipRuntimeDeletion(cr *kdopv1alpha1.DockerContainer) bool {
	return cr.Status.Adopted && managementModeFor(cr) == kdopv1alpha1.DockerContainerManagementModeObserve
}

func runtimeSnapshotFromInspect(hostName string, summary types.Container, inspect types.ContainerJSON) runtimeContainerSnapshot {
	name := strings.TrimPrefix(summary.Names[0], "/")
	if inspect.Name != "" {
		name = strings.TrimPrefix(inspect.Name, "/")
	}

	spec := kdopv1alpha1.DockerContainerSpec{
		Image:          inspect.Config.Image,
		ContainerName:  name,
		DockerHostRef:  hostName,
		ManagementMode: string(kdopv1alpha1.DockerContainerManagementModeObserve),
	}
	if inspect.HostConfig != nil {
		spec.RestartPolicy = string(inspect.HostConfig.RestartPolicy.Name)
		if inspect.HostConfig.NanoCPUs > 0 || inspect.HostConfig.Memory > 0 {
			spec.Resources = &kdopv1alpha1.ResourceRequirements{}
			if inspect.HostConfig.NanoCPUs > 0 {
				spec.Resources.CPULimit = formatNanoCPUs(inspect.HostConfig.NanoCPUs)
			}
			if inspect.HostConfig.Memory > 0 {
				spec.Resources.MemoryLimit = formatBytes(inspect.HostConfig.Memory)
			}
		}
	}
	if len(inspect.Config.Cmd) > 0 {
		spec.Command = slices.Clone(inspect.Config.Cmd)
	}
	if len(inspect.Config.Env) > 0 {
		spec.Env = slices.Clone(inspect.Config.Env)
		sort.Strings(spec.Env)
	}

	runtimeIncomplete := false
	if inspect.NetworkSettings != nil {
		spec.Ports = collectPortMappings(inspect.NetworkSettings.Ports)
		if len(inspect.NetworkSettings.Ports) == 0 && len(summary.Ports) > 0 {
			runtimeIncomplete = true
		}
	}
	if len(summary.Mounts) > 0 {
		runtimeIncomplete = true
	}

	snapshot := runtimeContainerSnapshot{
		ID:                summary.ID,
		Name:              name,
		State:             summary.State,
		Health:            dockerHealth(inspect),
		IPv4:              dockerIPv4(inspect),
		Spec:              spec,
		RuntimeIncomplete: runtimeIncomplete,
	}

	return snapshot
}

func collectPortMappings(ports nat.PortMap) []string {
	if len(ports) == 0 {
		return nil
	}

	var mappings []string
	for port, bindings := range ports {
		if len(bindings) == 0 {
			continue
		}
		for _, binding := range bindings {
			if binding.HostPort == "" {
				continue
			}
			mappings = append(mappings, fmt.Sprintf("%s:%s", binding.HostPort, port.Port()))
		}
	}
	sort.Strings(mappings)
	return mappings
}

func dockerHealth(inspect types.ContainerJSON) string {
	if inspect.State != nil && inspect.State.Health != nil {
		return inspect.State.Health.Status
	}
	return ""
}

func dockerIPv4(inspect types.ContainerJSON) string {
	if inspect.NetworkSettings == nil {
		return ""
	}
	for _, network := range inspect.NetworkSettings.Networks {
		if network.IPAddress != "" {
			return network.IPAddress
		}
	}
	return ""
}

func observedSpecHash(spec kdopv1alpha1.DockerContainerSpec) string {
	data, err := json.Marshal(spec)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func formatNanoCPUs(nanoCPUs int64) string {
	cores := float64(nanoCPUs) / 1e9
	if math.Mod(cores, 1) == 0 {
		return strconv.FormatInt(int64(cores), 10)
	}
	return strconv.FormatFloat(cores, 'f', -1, 64)
}

func formatBytes(memory int64) string {
	const mebibyte = 1024 * 1024
	if memory > 0 && memory%mebibyte == 0 {
		return fmt.Sprintf("%dm", memory/mebibyte)
	}
	return strconv.FormatInt(memory, 10)
}

func adoptedObjectName(containerName string) string {
	name := strings.ToLower(containerName)
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '-':
			if !lastDash {
				b.WriteRune('-')
				lastDash = true
			}
		default:
			if !lastDash {
				b.WriteRune('-')
				lastDash = true
			}
		}
	}
	result := strings.Trim(b.String(), "-")
	if result == "" {
		return "adopted-container"
	}
	return result
}

func buildAdoptedDockerContainer(namespace string, host *kdopv1alpha1.DockerHost, snapshot runtimeContainerSnapshot) *kdopv1alpha1.DockerContainer {
	return &kdopv1alpha1.DockerContainer{
		ObjectMeta: metav1.ObjectMeta{
			Name:      adoptedObjectName(snapshot.Name),
			Namespace: namespace,
			Labels: map[string]string{
				managedByLabelKey: managedByAdoptValue,
			},
			Annotations: map[string]string{
				adoptedFromAnnotationKey: fmt.Sprintf("%s/%s", host.Name, snapshot.ID),
			},
		},
		Spec: snapshot.Spec,
		Status: kdopv1alpha1.DockerContainerStatus{
			Adopted:          true,
			ObservedSpecHash: observedSpecHash(snapshot.Spec),
		},
	}
}
