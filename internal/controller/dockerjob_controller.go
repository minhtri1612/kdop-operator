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
	"io"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	dockerclient "github.com/docker/docker/client"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	kdopv1alpha1 "github.com/minhtri1612/kdop-operator/api/v1alpha1"
	"github.com/minhtri1612/kdop-operator/internal/docker"
)

const jobRequeueInterval = 5 * time.Second

// DockerJobReconciler reconciles a DockerJob object
type DockerJobReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=kdop.kdop.io.vn,resources=dockerjobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kdop.kdop.io.vn,resources=dockerjobs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=kdop.kdop.io.vn,resources=dockerjobs/finalizers,verbs=update
// +kubebuilder:rbac:groups=kdop.kdop.io.vn,resources=dockerhosts,verbs=get;list;watch

func (r *DockerJobReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := log.FromContext(ctx)

	job := &kdopv1alpha1.DockerJob{}
	if err := r.Get(ctx, req.NamespacedName, job); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	cli, err := docker.NewClient(ctx, r.Client, job.Namespace, job.Spec.DockerHostRef)
	if err != nil {
		l.Error(err, "Failed to create docker client")
		return ctrl.Result{}, err
	}
	defer func() { _ = cli.Close() }()

	return r.syncJob(ctx, cli, job)
}

func (r *DockerJobReconciler) containerName(job *kdopv1alpha1.DockerJob) string {
	if job.Spec.ContainerName != "" {
		return job.Spec.ContainerName
	}
	return "job-" + job.Name
}

func (r *DockerJobReconciler) syncJob(ctx context.Context, cli dockerclient.APIClient, job *kdopv1alpha1.DockerJob) (ctrl.Result, error) {
	l := log.FromContext(ctx)
	containerName := r.containerName(job)

	inspected, err := cli.ContainerInspect(ctx, containerName)
	if err != nil {
		if !dockerclient.IsErrNotFound(err) {
			return ctrl.Result{}, err
		}

		l.Info("Job container not found, creating...", "Container", containerName)
		containerID, err := r.createAndStartContainer(ctx, cli, job)
		if err != nil {
			l.Error(err, "Failed to create job container")
			return ctrl.Result{}, err
		}

		now := metav1.Now()
		job.Status.Phase = kdopv1alpha1.JobPhaseRunning
		job.Status.StartTime = &now
		job.Status.Attempts++
		job.Status.ContainerID = containerID
		job.Status.Message = "Container started"
		if err := r.Status().Update(ctx, job); err != nil {
			l.Error(err, "Failed to update status to Running")
		}
		return ctrl.Result{RequeueAfter: jobRequeueInterval}, nil
	}

	if inspected.State == nil {
		return ctrl.Result{RequeueAfter: jobRequeueInterval}, nil
	}

	switch {
	case inspected.State.Running:
		if job.Spec.ActiveDeadlineSeconds != nil && job.Status.StartTime != nil {
			deadline := job.Status.StartTime.Add(time.Duration(*job.Spec.ActiveDeadlineSeconds) * time.Second)
			if time.Now().After(deadline) {
				l.Info("Job exceeded active deadline, terminating", "Container", containerName)
				timeout := 10
				_ = cli.ContainerStop(ctx, inspected.ID, container.StopOptions{Timeout: &timeout})
				_ = cli.ContainerRemove(ctx, inspected.ID, container.RemoveOptions{Force: true})

				now := metav1.Now()
				job.Status.Phase = kdopv1alpha1.JobPhaseFailed
				job.Status.ContainerID = inspected.ID
				job.Status.CompletionTime = &now
				job.Status.Message = "Job exceeded ActiveDeadlineSeconds"
				_ = r.Status().Update(ctx, job)
				return ctrl.Result{}, nil
			}
		}

		if job.Status.Phase != kdopv1alpha1.JobPhaseRunning {
			job.Status.Phase = kdopv1alpha1.JobPhaseRunning
		}
		job.Status.ContainerID = inspected.ID
		job.Status.Message = "Container running"
		_ = r.Status().Update(ctx, job)
		return ctrl.Result{RequeueAfter: jobRequeueInterval}, nil

	default:
		// exited / created / paused — xử lý ở 5.3+
		return ctrl.Result{RequeueAfter: jobRequeueInterval}, nil
	}
}

func (r *DockerJobReconciler) createAndStartContainer(ctx context.Context, cli dockerclient.APIClient, job *kdopv1alpha1.DockerJob) (string, error) {
	l := log.FromContext(ctx)
	containerName := r.containerName(job)

	reader, err := cli.ImagePull(ctx, job.Spec.Image, image.PullOptions{})
	if err != nil {
		l.Error(err, "Failed to pull image")
		return "", err
	}
	defer func() { _ = reader.Close() }()
	_, _ = io.Copy(io.Discard, reader)

	cmd := append([]string{}, job.Spec.Command...)
	if len(job.Spec.Args) > 0 {
		cmd = append(cmd, job.Spec.Args...)
	}

	restartPolicy := container.RestartPolicyMode("no")
	if job.Spec.RestartPolicy == "OnFailure" {
		restartPolicy = container.RestartPolicyOnFailure
	}

	config := &container.Config{
		Image: job.Spec.Image,
		Cmd:   cmd,
	}
	hostConfig := &container.HostConfig{
		RestartPolicy: container.RestartPolicy{Name: restartPolicy},
	}

	resp, err := cli.ContainerCreate(ctx, config, hostConfig, nil, nil, containerName)
	if err != nil {
		l.Error(err, "Failed to create job container")
		return "", err
	}

	if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		l.Error(err, "Failed to start job container")
		return "", err
	}

	l.Info("Job container created and started", "ID", resp.ID, "Container", containerName)
	return resp.ID, nil
}

func (r *DockerJobReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kdopv1alpha1.DockerJob{}).
		Named("dockerjob").
		Complete(r)
}
