package config

import (
	"testing"
)

func TestLoadConfigExample(t *testing.T) {
	cfg, err := LoadConfig("../config.stream_based.example.yaml")
	if err != nil {
		t.Fatalf("Failed to load stream_based example config: %v", err)
	}

	if cfg.Units != "Mbit" {
		t.Errorf("Expected units 'Mbit', got '%s'", cfg.Units)
	}

	if len(cfg.Modules.MediaServers) == 0 {
		t.Fatalf("Expected media servers in config")
	}

	ms := cfg.Modules.MediaServers[0]
	if ms.StreamBasedSpeeds == nil || !ms.StreamBasedSpeeds.Enabled {
		t.Errorf("Expected stream_based_speeds to be enabled")
	}

	if val, ok := ms.StreamBasedSpeeds.Speeds[0]; !ok || val != "unlimited" {
		t.Errorf("Expected speed[0] == 'unlimited', got %v", val)
	}
}
