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
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	kdopv1alpha1 "github.com/minhtri1612/kdop-operator/api/v1alpha1"
	"github.com/minhtri1612/kdop-operator/internal/docker"
)

const (
	jobRequeueInterval = 5 * time.Second
	dockerJobFinalizer = "dockerjob.kdop.kdop.io.vn/finalizer"
)

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

	// 5.7 — Finalizer
	if job.ObjectMeta.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(job, dockerJobFinalizer) {
			controllerutil.AddFinalizer(job, dockerJobFinalizer)
			if err := r.Update(ctx, job); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		if controllerutil.ContainsFinalizer(job, dockerJobFinalizer) {
			name := r.containerName(job)
			if err := cli.ContainerRemove(ctx, name, container.RemoveOptions{Force: true}); err != nil && !dockerclient.IsErrNotFound(err) {
				l.Error(err, "Failed to remove job container on deletion", "Container", name)
				return ctrl.Result{}, err
			}
			l.Info("Removed job container on CR deletion", "Container", name)
			controllerutil.RemoveFinalizer(job, dockerJobFinalizer)
			if err := r.Update(ctx, job); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	if job.Status.Phase == kdopv1alpha1.JobPhaseSucceeded || job.Status.Phase == kdopv1alpha1.JobPhaseFailed {
		return r.handleTTLCleanup(ctx, cli, job)
	}

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
				return r.handleTTLCleanup(ctx, cli, job)
			}
		}

		if job.Status.Phase != kdopv1alpha1.JobPhaseRunning {
			job.Status.Phase = kdopv1alpha1.JobPhaseRunning
		}
		job.Status.ContainerID = inspected.ID
		job.Status.Message = "Container running"
		_ = r.Status().Update(ctx, job)
		return ctrl.Result{RequeueAfter: jobRequeueInterval}, nil

	case inspected.State.Status == "exited":
		exitCode := int32(inspected.State.ExitCode)
		now := metav1.Now()
		job.Status.ContainerID = inspected.ID
		job.Status.ExitCode = &exitCode
		job.Status.CompletionTime = &now

		if exitCode == 0 {
			l.Info("Job completed successfully", "Container", containerName)
			job.Status.Phase = kdopv1alpha1.JobPhaseSucceeded
			job.Status.Message = "Job completed successfully"
			_ = r.Status().Update(ctx, job)
			return r.handleTTLCleanup(ctx, cli, job)
		}

		l.Info("Job container exited with error", "Container", containerName, "ExitCode", exitCode)
		if job.Status.Attempts <= job.Spec.BackoffLimit {
			l.Info("Retrying job", "Attempt", job.Status.Attempts+1, "BackoffLimit", job.Spec.BackoffLimit)
			_ = cli.ContainerRemove(ctx, inspected.ID, container.RemoveOptions{Force: true})

			containerID, err := r.createAndStartContainer(ctx, cli, job)
			if err != nil {
				l.Error(err, "Retry failed")
				job.Status.Phase = kdopv1alpha1.JobPhaseFailed
				job.Status.Message = fmt.Sprintf("Retry failed: %v", err)
				_ = r.Status().Update(ctx, job)
				return ctrl.Result{}, err
			}

			job.Status.Attempts++
			job.Status.Phase = kdopv1alpha1.JobPhaseRunning
			job.Status.ContainerID = containerID
			job.Status.Message = fmt.Sprintf("Retrying (attempt %d/%d)", job.Status.Attempts, job.Spec.BackoffLimit+1)
			job.Status.ExitCode = nil
			job.Status.CompletionTime = nil
			_ = r.Status().Update(ctx, job)
			return ctrl.Result{RequeueAfter: jobRequeueInterval}, nil
		}

		job.Status.Phase = kdopv1alpha1.JobPhaseFailed
		job.Status.Message = fmt.Sprintf("Job failed with exit code %d after %d attempt(s)", exitCode, job.Status.Attempts)
		_ = r.Status().Update(ctx, job)
		return r.handleTTLCleanup(ctx, cli, job)

	default:
		return ctrl.Result{RequeueAfter: jobRequeueInterval}, nil
	}
}

func (r *DockerJobReconciler) handleTTLCleanup(ctx context.Context, cli dockerclient.APIClient, job *kdopv1alpha1.DockerJob) (ctrl.Result, error) {
	if job.Spec.TTLSecondsAfterFinished == nil || job.Status.CompletionTime == nil {
		return ctrl.Result{}, nil
	}

	ttl := time.Duration(*job.Spec.TTLSecondsAfterFinished) * time.Second
	elapsed := time.Since(job.Status.CompletionTime.Time)
	if elapsed < ttl {
		return ctrl.Result{RequeueAfter: ttl - elapsed}, nil
	}

	l := log.FromContext(ctx)
	name := r.containerName(job)
	if err := cli.ContainerRemove(ctx, name, container.RemoveOptions{Force: true}); err != nil && !dockerclient.IsErrNotFound(err) {
		l.Error(err, "Failed to remove job container after TTL", "Container", name)
		return ctrl.Result{}, err
	}
	l.Info("Removed job container after TTL", "Container", name)
	return ctrl.Result{}, nil
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
