package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigOptional_SimulatedCacheDefaults(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("port: 8317\n"), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := LoadConfigOptional(configPath, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}

	if !cfg.SimulatedCache.Enabled {
		t.Fatal("SimulatedCache.Enabled = false, want true")
	}
	if got := cfg.SimulatedCache.MissProbability; got != 0 {
		t.Fatalf("SimulatedCache.MissProbability = %v, want 0", got)
	}
	if got := cfg.SimulatedCache.TTLSeconds; got != 300 {
		t.Fatalf("SimulatedCache.TTLSeconds = %d, want 300", got)
	}
	if got := cfg.SimulatedCache.RetentionRatio; got != 0.7 {
		t.Fatalf("SimulatedCache.RetentionRatio = %v, want 0.7", got)
	}
}

func TestLoadConfigOptional_SimulatedCacheSanitized(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	configYAML := []byte(`
simulated-cache:
  enabled: true
  miss_probability: 1.5
  ttl_seconds: -1
  retention_ratio: -0.1
`)
	if err := os.WriteFile(configPath, configYAML, 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := LoadConfigOptional(configPath, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}

	if got := cfg.SimulatedCache.MissProbability; got != 1 {
		t.Fatalf("SimulatedCache.MissProbability = %v, want 1", got)
	}
	if got := cfg.SimulatedCache.TTLSeconds; got != 0 {
		t.Fatalf("SimulatedCache.TTLSeconds = %d, want 0", got)
	}
	if got := cfg.SimulatedCache.RetentionRatio; got != 0 {
		t.Fatalf("SimulatedCache.RetentionRatio = %v, want 0", got)
	}
}
