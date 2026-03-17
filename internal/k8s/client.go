package k8s

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	metricsv1beta1 "k8s.io/metrics/pkg/client/clientset/versioned"
)

// Client wraps the Kubernetes client with convenience methods.
type Client struct {
	clientset      *kubernetes.Clientset
	metrics        *metricsv1beta1.Clientset
	context        string
	config         *api.Config
	kubeconfigPath string
}

// ConnectOptions configures how to connect to a cluster.
type ConnectOptions struct {
	Kubeconfig string // Path to kubeconfig file (empty = default)
	Context    string // Kubeconfig context (empty = current)
}

// Connect creates a new Kubernetes client from kubeconfig.
func Connect(opts ConnectOptions) (*Client, error) {
	kubeconfigPath := expandHome(opts.Kubeconfig)
	if kubeconfigPath == "" {
		kubeconfigPath = defaultKubeconfig()
	}

	loadingRules := &clientcmd.ClientConfigLoadingRules{
		ExplicitPath: kubeconfigPath,
	}

	overrides := &clientcmd.ConfigOverrides{}
	if opts.Context != "" {
		overrides.CurrentContext = opts.Context
	}

	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules,
		overrides,
	)

	rawConfig, err := clientConfig.RawConfig()
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig: %w", err)
	}

	restConfig, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("building rest config: %w", err)
	}

	// Reasonable timeouts for TUI use.
	restConfig.Timeout = 10 * time.Second

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("creating kubernetes client: %w", err)
	}

	metricsClient, err := metricsv1beta1.NewForConfig(restConfig)
	if err != nil {
		// Metrics API is optional — don't fail if not available.
		metricsClient = nil
	}

	activeContext := rawConfig.CurrentContext
	if opts.Context != "" {
		activeContext = opts.Context
	}

	return &Client{
		clientset:      clientset,
		metrics:        metricsClient,
		context:        activeContext,
		config:         &rawConfig,
		kubeconfigPath: kubeconfigPath,
	}, nil
}

// ActiveContext returns the name of the active kubeconfig context.
func (c *Client) ActiveContext() string {
	return c.context
}

// AvailableContexts returns all context names from the kubeconfig.
func (c *Client) AvailableContexts() []string {
	if c.config == nil {
		return nil
	}
	var contexts []string
	for name := range c.config.Contexts {
		contexts = append(contexts, name)
	}
	return contexts
}

// KubeconfigPath returns the path to the kubeconfig file used by this client.
func (c *Client) KubeconfigPath() string {
	return c.kubeconfigPath
}

// ServerVersion returns the Kubernetes server version string.
func (c *Client) ServerVersion() (string, error) {
	info, err := c.clientset.Discovery().ServerVersion()
	if err != nil {
		return "", err
	}
	return info.GitVersion, nil
}

// Ping checks if the cluster is reachable.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.clientset.Discovery().ServerVersion()
	return err
}

// Clientset returns the underlying Kubernetes clientset.
func (c *Client) Clientset() *kubernetes.Clientset {
	return c.clientset
}

// MetricsClient returns the metrics clientset (may be nil).
func (c *Client) MetricsClient() *metricsv1beta1.Clientset {
	return c.metrics
}

// HasMetrics returns true if the metrics API is available.
func (c *Client) HasMetrics(ctx context.Context) bool {
	if c.metrics == nil {
		return false
	}
	_, err := c.metrics.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{Limit: 1})
	return err == nil
}

func defaultKubeconfig() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("/Users", "walle", ".kube", "config")
	}
	return filepath.Join(home, ".kube", "config")
}

func expandHome(path string) string {
	if path == "" {
		return ""
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[1:])
	}
	return path
}
