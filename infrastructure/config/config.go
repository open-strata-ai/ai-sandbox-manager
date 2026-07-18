// Package config loads the sandbox-manager configuration. The repository is
// stdlib-only, so (following the OpenStrata convention) the loader reads an
// optional JSON overlay whose keys mirror infrastructure/config/config.yaml;
// sensible SPECS §11.5 defaults are applied otherwise.
package config

import (
	"encoding/json"
	"os"
)

// Config is the full sandbox-manager configuration (mirrors SPECS §11.5).
type Config struct {
	Enabled   bool            `json:"enabled"`
	Pool      PoolConfig      `json:"pool"`
	Defaults  DefaultsConfig  `json:"defaults"`
	Providers ProvidersConfig `json:"providers"`
	// Capacity is the max concurrent sandboxes per node (backpressure, RULE-SB-008).
	Capacity int `json:"capacity"`
}

type PoolConfig struct {
	MaxIdlePerSpec int `json:"maxIdlePerSpec"`
	TTLSeconds     int `json:"ttlSeconds"`
}

type DefaultsConfig struct {
	Runtime   string `json:"runtime"`
	CPU       string `json:"cpu"`
	Memory    string `json:"memory"`
	Network   string `json:"network"`
	TimeoutMs int    `json:"timeoutMs"`
}

type ProvidersConfig struct {
	Kata KataProvider `json:"kata"`
	E2B  E2BProvider  `json:"e2b"`
}

type KataProvider struct {
	Enabled      bool   `json:"enabled"`
	RuntimeClass string `json:"runtimeClass"`
}

type E2BProvider struct {
	Enabled    bool   `json:"enabled"`
	APIKeyFrom string `json:"apiKeyFrom"`
}

// Default returns the SPECS §11.5 defaults.
func Default() Config {
	return Config{
		Enabled:  true,
		Pool:     PoolConfig{MaxIdlePerSpec: 4, TTLSeconds: 300},
		Defaults: DefaultsConfig{Runtime: "kata", CPU: "1", Memory: "512Mi", Network: "deny-all", TimeoutMs: 30000},
		Providers: ProvidersConfig{
			Kata: KataProvider{Enabled: true, RuntimeClass: "kata"},
			E2B:  E2BProvider{Enabled: false, APIKeyFrom: "vault://e2b"},
		},
		Capacity: 64,
	}
}

// Load returns Default() merged with an optional JSON overlay at path.
func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}
