package k8s

import (
	"context"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DeploymentInfo holds deployment status for display.
type DeploymentInfo struct {
	Name      string
	Namespace string
	Age       time.Duration

	// Replica counts.
	DesiredReplicas   int32
	ReadyReplicas     int32
	AvailableReplicas int32
	UpdatedReplicas   int32

	// Derived status.
	Status       DeploymentStatus
	StatusReason string
}

type DeploymentStatus int

const (
	DeployStatusHealthy     DeploymentStatus = iota
	DeployStatusProgressing
	DeployStatusDegraded
	DeployStatusFailed
)

// GetDeployments fetches deployments from the given namespaces.
// If namespaces is empty, fetches from all namespaces.
func (c *Client) GetDeployments(ctx context.Context, namespaces []string) ([]DeploymentInfo, error) {
	var result []DeploymentInfo

	if len(namespaces) == 0 {
		deps, err := c.clientset.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		for _, d := range deps.Items {
			result = append(result, buildDeploymentInfo(d))
		}
	} else {
		for _, ns := range namespaces {
			deps, err := c.clientset.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
			if err != nil {
				continue
			}
			for _, d := range deps.Items {
				result = append(result, buildDeploymentInfo(d))
			}
		}
	}

	return result, nil
}

func buildDeploymentInfo(d appsv1.Deployment) DeploymentInfo {
	desired := int32(1)
	if d.Spec.Replicas != nil {
		desired = *d.Spec.Replicas
	}

	info := DeploymentInfo{
		Name:              d.Name,
		Namespace:         d.Namespace,
		Age:               time.Since(d.CreationTimestamp.Time),
		DesiredReplicas:   desired,
		ReadyReplicas:     d.Status.ReadyReplicas,
		AvailableReplicas: d.Status.AvailableReplicas,
		UpdatedReplicas:   d.Status.UpdatedReplicas,
	}

	// Determine status from conditions and replica counts.
	info.Status = DeployStatusHealthy

	if desired == 0 {
		// Scaled to zero — not a problem.
		info.Status = DeployStatusHealthy
		info.StatusReason = "scaled to 0"
		return info
	}

	if info.ReadyReplicas == 0 && desired > 0 {
		info.Status = DeployStatusFailed
		info.StatusReason = "no ready replicas"
	} else if info.ReadyReplicas < desired {
		info.Status = DeployStatusDegraded
		info.StatusReason = "partial availability"
	} else if info.UpdatedReplicas < desired {
		info.Status = DeployStatusProgressing
		info.StatusReason = "rollout in progress"
	}

	// Check conditions for more detail.
	for _, c := range d.Status.Conditions {
		if c.Type == appsv1.DeploymentProgressing && c.Status == "False" {
			info.Status = DeployStatusFailed
			info.StatusReason = c.Message
		}
		if c.Type == appsv1.DeploymentReplicaFailure && c.Status == "True" {
			info.Status = DeployStatusFailed
			info.StatusReason = c.Message
		}
	}

	return info
}
