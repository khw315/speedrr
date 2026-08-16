package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config represents the runtime configuration for the YouTube Network Traffic Monitor.
type Config struct {
	Interface       string        `yaml:"interface"`        // Host physical network interface (e.g. "eth0", "en0", "br0")
	TargetIPs       []string      `yaml:"target_ips"`       // Specific target IP addresses (e.g. "192.168.1.50")
	TargetSubnets   []string      `yaml:"target_subnets"`   // Subnet/CIDR ranges to monitor (e.g. "192.168.1.0/24", "10.0.0.0/16")
	GatewayIP       string        `yaml:"gateway_ip"`       // Gateway / Router IP address (optional)
	WebhookURL      string        `yaml:"webhook_url"`      // Destination webhook URL (Speedrr / generic receiver)
	CooldownTimeout time.Duration `yaml:"cooldown_seconds"` // Cooldown timeout duration before state returns to IDLE
	Promiscuous     bool          `yaml:"promiscuous"`      // Enable promiscuous mode on PCAP interface
	SnapLen         int32         `yaml:"snaplen"`          // Packet snapshot length in bytes
	Debug           bool          `yaml:"debug"`            // Verbose logging mode
}

// DefaultConfig returns safe default configuration without hardcoded IPs.
func DefaultConfig() *Config {
	return &Config{
		Interface:       "eth0",
		TargetIPs:       []string{},
		TargetSubnets:   []string{},
		GatewayIP:       "",
		WebhookURL:      "http://speedrr:8080/api/v1/webhook/stream",
		CooldownTimeout: 30 * time.Second,
		Promiscuous:     true,
		SnapLen:         65535,
		Debug:           false,
	}
}

// GetAllTargets merges individual target IPs and subnet CIDR strings.
func (c *Config) GetAllTargets() []string {
	var targets []string
	targets = append(targets, c.TargetIPs...)
	targets = append(targets, c.TargetSubnets...)
	return targets
}

// LoadConfig loads configuration from a YAML file with environment variable overrides.
func LoadConfig(filePath string) (*Config, error) {
	cfg := DefaultConfig()

	// 1. Read from YAML file if available
	if filePath != "" {
		if data, err := os.ReadFile(filePath); err == nil {
			if err := yaml.Unmarshal(data, cfg); err != nil {
				return nil, fmt.Errorf("failed to parse YAML configuration: %w", err)
			}
		}
	}

	// 2. Apply environment variable overrides
	applyEnvOverrides(cfg)

	return cfg, nil
}

// applyEnvOverrides maps environment variables to configuration fields.
func applyEnvOverrides(cfg *Config) {
	applyStringEnv("MONITOR_INTERFACE", &cfg.Interface)
	applyStringEnv("GATEWAY_IP", &cfg.GatewayIP)
	applyStringEnv("WEBHOOK_URL", &cfg.WebhookURL)
	applyStringSliceEnv("TARGET_IPS", &cfg.TargetIPs)
	applyStringSliceEnv("TARGET_SUBNETS", &cfg.TargetSubnets)
	applyDurationSecondsEnv("COOLDOWN_SECONDS", &cfg.CooldownTimeout)
	applyBoolEnv("PROMISCUOUS", &cfg.Promiscuous)
	applyBoolEnv("DEBUG", &cfg.Debug)
}

func applyStringEnv(key string, target *string) {
	if val := os.Getenv(key); val != "" {
		*target = val
	}
}

func applyStringSliceEnv(key string, target *[]string) {
	val := os.Getenv(key)
	if val == "" {
		return
	}

	var cleaned []string
	for _, item := range strings.Split(val, ",") {
		trimmed := strings.TrimSpace(item)
		if trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}

	if len(cleaned) > 0 {
		*target = cleaned
	}
}

func applyDurationSecondsEnv(key string, target *time.Duration) {
	val := os.Getenv(key)
	if val == "" {
		return
	}

	if sec, err := strconv.Atoi(val); err == nil && sec > 0 {
		*target = time.Duration(sec) * time.Second
	}
}

func applyBoolEnv(key string, target *bool) {
	val := os.Getenv(key)
	if val == "" {
		return
	}

	lower := strings.ToLower(val)
	*target = (lower == "true" || lower == "1" || lower == "yes")
}
