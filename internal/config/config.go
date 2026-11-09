// Package config reads server settings from environment variables.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// Config holds settings read from environment variables.
type Config struct {
	Port    string // PORT, default 8080
	DataDir string // DATA_DIR, default "data"

	// AdminKey protects crawl, eval and stats endpoints (ADMIN_API_KEY).
	AdminKey string
	// GeneratedKey is true when ADMIN_API_KEY was unset and AdminKey was
	// generated for this run only.
	GeneratedKey bool

	// TrustProxyHeaders makes the rate limiter use X-Forwarded-For
	// (TRUST_PROXY_HEADERS). Enable it only behind an ingress that sets the
	// header (Azure Container Apps does); otherwise clients can forge it.
	TrustProxyHeaders bool

	// AllowPrivateTargets disables SSRF protection so you can crawl
	// localhost during development (ALLOW_PRIVATE_TARGETS). Never enable in
	// production.
	AllowPrivateTargets bool
}

// IndexDir is where the index is persisted.
func (c Config) IndexDir() string { return filepath.Join(c.DataDir, "index") }

// minKeyLen is the shortest accepted ADMIN_API_KEY.
const minKeyLen = 16

// Load reads the configuration from the environment.
func Load() (Config, error) {
	cfg := Config{
		Port:                envOr("PORT", "8080"),
		DataDir:             envOr("DATA_DIR", "data"),
		AdminKey:            os.Getenv("ADMIN_API_KEY"),
		TrustProxyHeaders:   envBool("TRUST_PROXY_HEADERS"),
		AllowPrivateTargets: envBool("ALLOW_PRIVATE_TARGETS"),
	}

	switch {
	case cfg.AdminKey == "":
		b := make([]byte, 24)
		if _, err := rand.Read(b); err != nil {
			return Config{}, fmt.Errorf("generate admin key: %w", err)
		}
		cfg.AdminKey = hex.EncodeToString(b)
		cfg.GeneratedKey = true
	case len(cfg.AdminKey) < minKeyLen:
		return Config{}, errors.New("ADMIN_API_KEY must be at least 16 characters")
	}
	return cfg, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string) bool {
	b, _ := strconv.ParseBool(os.Getenv(key))
	return b
}
