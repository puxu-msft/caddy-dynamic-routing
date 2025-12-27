package caddyslb

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestReadmeMetricNamesExistInMetricsGo(t *testing.T) {
	readmeBytes, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	readme := string(readmeBytes)

	start := strings.Index(readme, "\n## Metrics\n")
	if start == -1 {
		// Allow Metrics to be at the beginning of the file.
		if strings.HasPrefix(readme, "## Metrics\n") {
			start = 0
		} else {
			t.Fatalf("README.md must contain a '## Metrics' section")
		}
	}

	section := readme[start:]
	// Trim to the Metrics section only.
	// Find the next level-2 heading after the current one.
	metricsHeaderVariants := []string{"\n## Metrics\n", "## Metrics\n"}
	metricsHeaderLen := 0
	for _, v := range metricsHeaderVariants {
		if strings.HasPrefix(section, v) {
			metricsHeaderLen = len(v)
			break
		}
	}
	if metricsHeaderLen == 0 {
		// Fallback: don't slice; still try to find the next heading after the first newline.
		if i := strings.Index(section[1:], "\n## "); i != -1 {
			section = section[:i+1]
		}
	} else {
		if idx := strings.Index(section[metricsHeaderLen:], "\n## "); idx != -1 {
			section = section[:metricsHeaderLen+idx]
		}
	}

	reMetric := regexp.MustCompile(`caddy_dynamic_lb_[a-z0-9_]+`)
	metricNames := reMetric.FindAllString(section, -1)
	if len(metricNames) == 0 {
		t.Fatalf("no caddy_dynamic_lb_* metric names found in README.md Metrics section")
	}

	uniq := make(map[string]struct{}, len(metricNames))
	for _, m := range metricNames {
		uniq[m] = struct{}{}
	}

	metricsGoBytes, err := os.ReadFile("metrics/metrics.go")
	if err != nil {
		t.Fatalf("read metrics/metrics.go: %v", err)
	}
	metricsGo := string(metricsGoBytes)

	for fullName := range uniq {
		if !strings.HasPrefix(fullName, "caddy_dynamic_lb_") {
			continue
		}
		suffix := strings.TrimPrefix(fullName, "caddy_dynamic_lb_")

		// The code defines Name (suffix) and hard-codes namespace/subsystem.
		// Validate suffix exists as a Name field in metrics.go.
		if !strings.Contains(metricsGo, `Name:      "`+suffix+`"`) && !strings.Contains(metricsGo, `Name: "`+suffix+`"`) {
			t.Fatalf("README metric %q not found as a Name in metrics/metrics.go (expected suffix %q)", fullName, suffix)
		}
	}
}
