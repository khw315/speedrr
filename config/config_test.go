package config

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
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

func TestLoadConfigClientShares(t *testing.T) {
	tmpDir := t.TempDir()
	clientYaml := `
units: Mbit
clients:
  - type: qbittorrent
    url: http://localhost:8080
    download_shares: 0
    upload_shares: 0
`
	file := filepath.Join(tmpDir, "shares.yaml")
	if err := os.WriteFile(file, []byte(clientYaml), 0644); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}

	cfg, err := LoadConfig(file)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.Clients[0].DownloadShares != 1 || cfg.Clients[0].UploadShares != 1 {
		t.Errorf("Expected default shares to be 1, got down=%d up=%d", cfg.Clients[0].DownloadShares, cfg.Clients[0].UploadShares)
	}
}

func TestLoadConfigErrors(t *testing.T) {
	// Non-existent file
	if _, err := LoadConfig("non_existent_file.yaml"); err == nil {
		t.Errorf("Expected error for non-existent file, got nil")
	}

	// Invalid YAML content
	tmpDir := t.TempDir()
	invalidFile := filepath.Join(tmpDir, "invalid.yaml")
	if err := os.WriteFile(invalidFile, []byte("invalid: yaml: [unclosed"), 0644); err != nil {
		t.Fatalf("Failed to write invalid temp file: %v", err)
	}

	if _, err := LoadConfig(invalidFile); err == nil {
		t.Errorf("Expected error for invalid YAML syntax, got nil")
	}
}

func TestStreamBasedSpeedsUnmarshalErrors(t *testing.T) {
	tmpDir := t.TempDir()
	badKeyFile := filepath.Join(tmpDir, "bad_key.yaml")
	badYaml := `
modules:
  media_servers:
    - type: plex
      stream_based_speeds:
        enabled: true
        speeds:
          not_an_int: "10Mbit"
`
	if err := os.WriteFile(badKeyFile, []byte(badYaml), 0644); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}

	if _, err := LoadConfig(badKeyFile); err == nil {
		t.Errorf("Expected error for non-integer key in speeds map, got nil")
	}

	var s StreamBasedSpeedsConfig
	node := &yaml.Node{Kind: yaml.ScalarNode, Value: "invalid"}
	if err := s.UnmarshalYAML(node); err == nil {
		t.Errorf("Expected decode error for invalid node, got nil")
	}

	badValYaml := `
modules:
  media_servers:
    - type: plex
      stream_based_speeds:
        enabled: true
        speeds:
          1: [unclosed_list
`
	badValFile := filepath.Join(tmpDir, "bad_val.yaml")
	_ = os.WriteFile(badValFile, []byte(badValYaml), 0644)
	if _, err := LoadConfig(badValFile); err == nil {
		t.Errorf("Expected error for invalid value in speeds map, got nil")
	}
}
