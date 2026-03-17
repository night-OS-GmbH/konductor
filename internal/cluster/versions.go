package cluster

// RecommendedVersions maps component names to their recommended versions.
// These are the versions that Konductor will install or suggest updating to.
var RecommendedVersions = map[string]string{
	"metrics-server":     "v0.8.1",
	"hetzner-ccm":        "v1.22.0",
	"cert-manager":       "v1.17.2",
	"konductor-operator": "latest",
}
