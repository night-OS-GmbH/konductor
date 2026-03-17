package k8s

import (
	"context"
	"io"

	corev1 "k8s.io/api/core/v1"
)

// PodLogOptions configures what logs to fetch.
type PodLogOptions struct {
	Namespace string
	PodName   string
	Container string // empty = default container
	TailLines int64
	Previous  bool
}

// GetPodLogs fetches logs for a pod/container.
func (c *Client) GetPodLogs(ctx context.Context, opts PodLogOptions) (string, error) {
	k8sOpts := &corev1.PodLogOptions{
		Container: opts.Container,
		Previous:  opts.Previous,
	}
	if opts.TailLines > 0 {
		k8sOpts.TailLines = &opts.TailLines
	}

	req := c.clientset.CoreV1().Pods(opts.Namespace).GetLogs(opts.PodName, k8sOpts)
	stream, err := req.Stream(ctx)
	if err != nil {
		return "", err
	}
	defer stream.Close()

	data, err := io.ReadAll(stream)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
