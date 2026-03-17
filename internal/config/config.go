package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Clusters []ClusterConfig `yaml:"clusters"`
}

type ClusterConfig struct {
	Name       string          `yaml:"name"`
	Kubeconfig string          `yaml:"kubeconfig"`
	Hetzner    HetznerConfig   `yaml:"hetzner"`
	Talos      TalosConfig     `yaml:"talos"`
	Scaling    ScalingConfig   `yaml:"scaling"`
	Dashboard  DashboardConfig `yaml:"dashboard"`
}

type DashboardConfig struct {
	// WatchNamespaces lists namespaces whose deployments are shown on the dashboard.
	// Empty means all namespaces.
	WatchNamespaces []string `yaml:"watchNamespaces"`
}

type HetznerConfig struct {
	Token          string `yaml:"token"`
	TokenFromEnv   string `yaml:"tokenFromEnv"`
	Network        string `yaml:"network"`
	SSHKey         string `yaml:"sshKey"`
	PlacementGroup string `yaml:"placementGroup"`
	Location       string `yaml:"location"`
}

type TalosConfig struct {
	ConfigPath string `yaml:"configPath"`
	Endpoint   string `yaml:"endpoint"`
}

type ScalingConfig struct {
	MinNodes       int    `yaml:"minNodes"`
	MaxNodes       int    `yaml:"maxNodes"`
	DefaultType    string `yaml:"defaultType"`
	CooldownMinutes int   `yaml:"cooldownMinutes"`
}

func (h HetznerConfig) GetToken() string {
	if h.Token != "" {
		return h.Token
	}
	if h.TokenFromEnv != "" {
		return os.Getenv(h.TokenFromEnv)
	}
	return os.Getenv("HCLOUD_TOKEN")
}

func Load() (*Config, error) {
	path := configPath()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultConfig(), nil
		}
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	return &cfg, nil
}

func configPath() string {
	if p := os.Getenv("KONDUCTOR_CONFIG"); p != "" {
		return p
	}

	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".konductor", "config.yaml")
}

func defaultConfig() *Config {
	home, _ := os.UserHomeDir()
	return &Config{
		Clusters: []ClusterConfig{
			{
				Name:       "default",
				Kubeconfig: filepath.Join(home, ".kube", "config"),
				Talos: TalosConfig{
					ConfigPath: filepath.Join(home, ".talos", "config"),
				},
				Scaling: ScalingConfig{
					MinNodes:        3,
					MaxNodes:        20,
					DefaultType:     "cpx31",
					CooldownMinutes: 10,
				},
			},
		},
	}
}
