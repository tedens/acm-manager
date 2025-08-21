package controllers

import (
	"strings"
	"time"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// IngressConfig defines parsed annotation values for ACM management
type IngressConfig struct {
	Managed             bool
	DomainOverride      string
	ZoneID              string
	Wildcard            bool
	SANs                []string
	CertTTL             time.Duration
	ReuseExisting       bool
	DeleteCertOnIngress bool
	FallbackWildcard    bool
}

// DefaultCertTTL is used when no TTL is specified (1 year)
var DefaultCertTTL = 365 * 24 * time.Hour

// ParseIngressAnnotations parses acm.tedens.dev/* annotations into a config struct
func ParseIngressAnnotations(annotations map[string]string) IngressConfig {
	logger := logf.Log.WithName("annotations")

	rawWildcard := strings.ToLower(annotations["acm.tedens.dev/wildcard"])
	if rawWildcard == "true" {
		logger.Info("Annotation overrides default: wildcard enabled")
	}

	rawDelete := strings.ToLower(annotations["acm.tedens.dev/delete-cert-on-ingress-delete"])
	if rawDelete == "true" {
		logger.Info("Annotation overrides default: delete cert on ingress delete enabled")
	}

	cfg := IngressConfig{
		Managed:             annotations["acm.tedens.dev/managed"] == "true",
		DomainOverride:      annotations["acm.tedens.dev/domain"],
		ZoneID:              annotations["acm.tedens.dev/zone-id"],
		Wildcard:            rawWildcard == "true",
		ReuseExisting:       annotations["acm.tedens.dev/reuse-existing"] != "false",
		DeleteCertOnIngress: rawDelete == "true",
		FallbackWildcard:    annotations["acm.tedens.dev/fallback-wildcard"] == "true",
	}

	// Parse SANs
	if sanStr, ok := annotations["acm.tedens.dev/san"]; ok {
		cfg.SANs = strings.Split(sanStr, ",")
		for i := range cfg.SANs {
			cfg.SANs[i] = strings.TrimSpace(cfg.SANs[i])
		}
	}

	// Parse cert TTL
	if ttlStr, ok := annotations["acm.tedens.dev/cert-ttl"]; ok {
		if dur, err := time.ParseDuration(ttlStr); err == nil {
			cfg.CertTTL = dur
		} else {
			cfg.CertTTL = DefaultCertTTL
		}
	} else {
		cfg.CertTTL = DefaultCertTTL
	}

	return cfg
}
