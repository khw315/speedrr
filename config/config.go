package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type ClientConfig struct {
	Type           string `yaml:"type"`
	URL            string `yaml:"url"`
	Username       string `yaml:"username"`
	Password       string `yaml:"password"`
	HTTPSVerify    bool   `yaml:"https_verify"`
	DownloadShares int    `yaml:"download_shares"`
	UploadShares   int    `yaml:"upload_shares"`
}

type IgnoreStreamConfig struct {
	Local       bool     `yaml:"local"`
	IPNetworks  []string `yaml:"ip_networks"`
	PausedAfter int      `yaml:"paused_after"`
}

type StreamBasedSpeedsConfig struct {
	Enabled bool                   `yaml:"enabled"`
	Speeds  map[int]interface{}    `yaml:"speeds"`
	Default interface{}            `yaml:"default"`
}

// UnmarshalYAML custom unmarshaler to handle integer keys in speeds map
func (s *StreamBasedSpeedsConfig) UnmarshalYAML(value *yaml.Node) error {
	var raw struct {
		Enabled bool        `yaml:"enabled"`
		Speeds  yaml.Node   `yaml:"speeds"`
		Default interface{} `yaml:"default"`
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	s.Enabled = raw.Enabled
	s.Default = raw.Default
	s.Speeds = make(map[int]interface{})

	if raw.Speeds.Kind == yaml.MappingNode {
		for i := 0; i < len(raw.Speeds.Content); i += 2 {
			keyNode := raw.Speeds.Content[i]
			valNode := raw.Speeds.Content[i+1]

			intKey, err := strconv.Atoi(keyNode.Value)
			if err != nil {
				return fmt.Errorf("invalid stream count key in speeds: %s", keyNode.Value)
			}
			var val interface{}
			if err := valNode.Decode(&val); err != nil {
				return err
			}
			s.Speeds[intKey] = val
		}
	}
	return nil
}


type MediaServerConfig struct {
	Type                string                   `yaml:"type"`
	URL                 string                   `yaml:"url"`
	HTTPSVerify         bool                     `yaml:"https_verify"`
	BandwidthMultiplier float64                  `yaml:"bandwidth_multiplier"`
	UpdateInterval      int                      `yaml:"update_interval"`
	IgnoreStreams       IgnoreStreamConfig       `yaml:"ignore_streams"`
	Token               string                   `yaml:"token"`
	APIKey              string                   `yaml:"api_key"`
	StreamBasedSpeeds   *StreamBasedSpeedsConfig `yaml:"stream_based_speeds"`
}

type ScheduleConfig struct {
	Start    string      `yaml:"start"`
	End      string      `yaml:"end"`
	Days     []string    `yaml:"days"`
	Upload   interface{} `yaml:"upload"`
	Download interface{} `yaml:"download"`
}

type ModulesConfig struct {
	MediaServers []MediaServerConfig `yaml:"media_servers"`
	Schedule     []ScheduleConfig    `yaml:"schedule"`
}

type SpeedrrConfig struct {
	LogsPath                  string        `yaml:"logs_path"`
	Units                     string        `yaml:"units"`
	MinUpload                 float64       `yaml:"min_upload"`
	MaxUpload                 float64       `yaml:"max_upload"`
	MinDownload               float64       `yaml:"min_download"`
	MaxDownload               float64       `yaml:"max_download"`
	Clients                   []ClientConfig `yaml:"clients"`
	Modules                   ModulesConfig `yaml:"modules"`
	ManualSpeedAlgorithmShare bool          `yaml:"manual_speed_algorithm_share"`
}

// LoadConfig reads and unmarshals Speedrr configuration from a YAML file.
func LoadConfig(filePath string) (*SpeedrrConfig, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", filePath, err)
	}

	cfg := &SpeedrrConfig{
		MinUpload:   0,
		MaxUpload:   100,
		MinDownload: 0,
		MaxDownload: 100,
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse YAML config: %w", err)
	}

	// Set default client share weights if not set
	for i := range cfg.Clients {
		if cfg.Clients[i].DownloadShares <= 0 {
			cfg.Clients[i].DownloadShares = 1
		}
		if cfg.Clients[i].UploadShares <= 0 {
			cfg.Clients[i].UploadShares = 1
		}
	}

	// Normalize units
	cfg.Units = strings.TrimSpace(cfg.Units)

	return cfg, nil
}
