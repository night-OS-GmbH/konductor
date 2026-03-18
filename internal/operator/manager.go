package operator

import (
	"context"
	"fmt"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	konductorv1alpha1 "github.com/night-OS-GmbH/konductor/api/v1alpha1"
	"github.com/night-OS-GmbH/konductor/internal/operator/controller"
	"github.com/night-OS-GmbH/konductor/internal/operator/decision"
	"github.com/night-OS-GmbH/konductor/internal/provider"
	"github.com/night-OS-GmbH/konductor/internal/talos"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(konductorv1alpha1.AddToScheme(scheme))
	utilruntime.Must(metricsv1beta1.AddToScheme(scheme))
}

// Scheme returns the runtime scheme used by the operator, containing both
// built-in Kubernetes types and Konductor CRDs.
func Scheme() *runtime.Scheme {
	return scheme
}

// NewManager creates a controller-runtime manager with standard operator
// settings: leader election, health probes on :8080, and metrics on :8081.
func NewManager(opts manager.Options) (manager.Manager, error) {
	cfg := ctrl.GetConfigOrDie()

	// Apply defaults for options the caller did not set.
	if opts.Scheme == nil {
		opts.Scheme = scheme
	}

	if opts.Metrics.BindAddress == "" {
		opts.Metrics = metricsserver.Options{BindAddress: ":8081"}
	}

	if opts.HealthProbeBindAddress == "" {
		opts.HealthProbeBindAddress = ":8080"
	}

	mgr, err := ctrl.NewManager(cfg, opts)
	if err != nil {
		return nil, fmt.Errorf("creating controller manager: %w", err)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return nil, fmt.Errorf("registering healthz check: %w", err)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return nil, fmt.Errorf("registering readyz check: %w", err)
	}

	return mgr, nil
}

// OperatorConfig holds the external dependencies required to wire up the
// operator controllers.
type OperatorConfig struct {
	// Provider is the infrastructure provider for node lifecycle management.
	Provider provider.Provider

	// TalosClient handles Talos node reset and configuration.
	TalosClient *talos.Client

	// LeaderElection enables leader election for HA deployments.
	LeaderElection bool

	// LeaderElectionID is the lock identity used for leader election.
	LeaderElectionID string
}

// Run is the main entry point for the Konductor operator. It creates the
// manager, registers both controllers, and starts the reconciliation loop.
// It blocks until the context is cancelled or a fatal error occurs.
func Run(ctx context.Context, cfg OperatorConfig) error {
	zapOpts := zap.Options{Development: os.Getenv("KONDUCTOR_DEV") == "true"}
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOpts)))

	log := ctrl.Log.WithName("operator")
	log.Info("starting konductor operator")

	leaderElectionID := cfg.LeaderElectionID
	if leaderElectionID == "" {
		leaderElectionID = "konductor-operator-leader"
	}

	mgrOpts := manager.Options{
		Scheme:                  scheme,
		LeaderElection:          cfg.LeaderElection,
		LeaderElectionID:        leaderElectionID,
		LeaderElectionNamespace: leaderElectionNamespace(),
	}

	mgr, err := NewManager(mgrOpts)
	if err != nil {
		return fmt.Errorf("setting up manager: %w", err)
	}

	engine := decision.NewEngine()

	// Register controllers.
	if err := controller.SetupNodePoolReconciler(mgr, engine, cfg.Provider); err != nil {
		return fmt.Errorf("registering NodePool controller: %w", err)
	}
	if err := controller.SetupNodeClaimReconciler(mgr, cfg.Provider, cfg.TalosClient); err != nil {
		return fmt.Errorf("registering NodeClaim controller: %w", err)
	}

	log.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("running manager: %w", err)
	}

	return nil
}

// leaderElectionNamespace returns the namespace for leader election. In-cluster
// deployments use the pod's namespace; local development falls back to "konductor-system".
func leaderElectionNamespace() string {
	if ns := os.Getenv("POD_NAMESPACE"); ns != "" {
		return ns
	}
	return "konductor-system"
}
