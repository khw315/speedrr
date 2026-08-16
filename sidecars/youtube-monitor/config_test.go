package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Interface != "eth0" {
		t.Errorf("expected Interface to be eth0, got %s", cfg.Interface)
	}
	if cfg.CooldownTimeout != 30*time.Second {
		t.Errorf("expected CooldownTimeout to be 30s, got %v", cfg.CooldownTimeout)
	}
	if !cfg.Promiscuous {
		t.Errorf("expected Promiscuous to be true")
	}
	if cfg.SnapLen != 65535 {
		t.Errorf("expected SnapLen to be 65535, got %d", cfg.SnapLen)
	}
	if cfg.Debug {
		t.Errorf("expected Debug to be false")
	}
}

func TestGetAllTargets(t *testing.T) {
	cfg := &Config{
		TargetIPs:     []string{"192.168.1.50"},
		TargetSubnets: []string{"192.168.1.0/24", "10.0.0.0/16"},
	}

	targets := cfg.GetAllTargets()
	if len(targets) != 3 {
		t.Fatalf("expected 3 targets, got %d", len(targets))
	}
}

func TestLoadConfigFile(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	yamlContent := `
interface: "br0"
target_ips:
  - "192.168.1.100"
target_subnets:
  - "192.168.2.0/24"
gateway_ip: "192.168.1.254"
webhook_url: "http://example.com/webhook"
cooldown_seconds: 45
promiscuous: false
snaplen: 1500
debug: true
`
	if err := os.WriteFile(configPath, []byte(yamlContent), 0600); err != nil {
		t.Fatalf("failed to write test config file: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.Interface != "br0" {
		t.Errorf("expected interface 'br0', got '%s'", cfg.Interface)
	}
	if len(cfg.TargetIPs) != 1 || cfg.TargetIPs[0] != "192.168.1.100" {
		t.Errorf("unexpected target_ips: %v", cfg.TargetIPs)
	}
	if len(cfg.TargetSubnets) != 1 || cfg.TargetSubnets[0] != "192.168.2.0/24" {
		t.Errorf("unexpected target_subnets: %v", cfg.TargetSubnets)
	}
	if cfg.GatewayIP != "192.168.1.254" {
		t.Errorf("expected gateway_ip '192.168.1.254', got '%s'", cfg.GatewayIP)
	}
	if cfg.WebhookURL != "http://example.com/webhook" {
		t.Errorf("expected webhook_url 'http://example.com/webhook', got '%s'", cfg.WebhookURL)
	}
	if cfg.CooldownTimeout != 45*time.Second {
		t.Errorf("expected cooldown 45s, got %v", cfg.CooldownTimeout)
	}
	if cfg.Promiscuous {
		t.Errorf("expected promiscuous false")
	}
	if cfg.SnapLen != 1500 {
		t.Errorf("expected snaplen 1500, got %d", cfg.SnapLen)
	}
	if !cfg.Debug {
		t.Errorf("expected debug true")
	}
}

func TestLoadConfigInvalidYAML(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "invalid.yaml")

	if err := os.WriteFile(configPath, []byte("invalid:\n  - yaml: ["), 0600); err != nil {
		t.Fatalf("failed to write invalid config file: %v", err)
	}

	_, err := LoadConfig(configPath)
	if err == nil {
		t.Errorf("expected error loading invalid yaml, got nil")
	}
}

func TestApplyEnvOverrides(t *testing.T) {
	// Set environment variables
	t.Setenv("MONITOR_INTERFACE", "enp4s0")
	t.Setenv("GATEWAY_IP", "10.0.0.1")
	t.Setenv("WEBHOOK_URL", "http://speedrr.local:8080/hook")
	t.Setenv("TARGET_IPS", "10.0.0.50, 10.0.0.51")
	t.Setenv("TARGET_SUBNETS", " 10.0.0.0/24 , 10.0.1.0/24 ")
	t.Setenv("COOLDOWN_SECONDS", "60")
	t.Setenv("PROMISCUOUS", "false")
	t.Setenv("DEBUG", "1")

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.Interface != "enp4s0" {
		t.Errorf("expected Interface enp4s0, got %s", cfg.Interface)
	}
	if cfg.GatewayIP != "10.0.0.1" {
		t.Errorf("expected GatewayIP 10.0.0.1, got %s", cfg.GatewayIP)
	}
	if cfg.WebhookURL != "http://speedrr.local:8080/hook" {
		t.Errorf("expected WebhookURL http://speedrr.local:8080/hook, got %s", cfg.WebhookURL)
	}
	if len(cfg.TargetIPs) != 2 || cfg.TargetIPs[0] != "10.0.0.50" || cfg.TargetIPs[1] != "10.0.0.51" {
		t.Errorf("unexpected TargetIPs: %v", cfg.TargetIPs)
	}
	if len(cfg.TargetSubnets) != 2 || cfg.TargetSubnets[0] != "10.0.0.0/24" || cfg.TargetSubnets[1] != "10.0.1.0/24" {
		t.Errorf("unexpected TargetSubnets: %v", cfg.TargetSubnets)
	}
	if cfg.CooldownTimeout != 60*time.Second {
		t.Errorf("expected CooldownTimeout 60s, got %v", cfg.CooldownTimeout)
	}
	if cfg.Promiscuous {
		t.Errorf("expected Promiscuous false, got true")
	}
	if !cfg.Debug {
		t.Errorf("expected Debug true, got false")
	}
}
