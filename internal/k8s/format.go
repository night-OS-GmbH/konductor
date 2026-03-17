package k8s

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/resource"
)

// FormatCPU formats a CPU quantity for display.
func FormatCPU(q resource.Quantity) string {
	millis := q.MilliValue()
	if millis == 0 {
		return "0"
	}
	if millis >= 1000 {
		return fmt.Sprintf("%.1f", float64(millis)/1000.0)
	}
	return fmt.Sprintf("%dm", millis)
}

// FormatMemory formats a memory quantity for display.
func FormatMemory(q resource.Quantity) string {
	bytes := q.Value()
	if bytes == 0 {
		return "0"
	}
	gi := float64(bytes) / (1024 * 1024 * 1024)
	if gi >= 1.0 {
		return fmt.Sprintf("%.1fGi", gi)
	}
	mi := float64(bytes) / (1024 * 1024)
	if mi >= 1.0 {
		return fmt.Sprintf("%.0fMi", mi)
	}
	ki := float64(bytes) / 1024
	return fmt.Sprintf("%.0fKi", ki)
}

// FormatResourcePair formats usage/total like "12m/100m".
func FormatResourcePair(usage, total resource.Quantity, fn func(resource.Quantity) string) string {
	if total.IsZero() {
		return fn(usage) + "/-"
	}
	return fn(usage) + "/" + fn(total)
}
