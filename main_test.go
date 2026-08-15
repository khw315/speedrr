package main

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/khw315/speedrr/client"
	"github.com/khw315/speedrr/config"
	"github.com/khw315/speedrr/module"
)

type mockClient struct {
	cfg        config.ClientConfig
	count      int
	countErr   error
	setUpErr   error
	setDownErr error
}

func (m *mockClient) Config() config.ClientConfig {
	return m.cfg
}

func (m *mockClient) GetActiveTorrentCount(ctx context.Context) (int, error) {
	return m.count, m.countErr
}

func (m *mockClient) SetUploadSpeed(ctx context.Context, speed float64) error {
	return m.setUpErr
}

func (m *mockClient) SetDownloadSpeed(ctx context.Context, speed float64) error {
	return m.setDownErr
}

func TestGetEnv(t *testing.T) {
	if val := getEnv("NON_EXISTENT_VAR_12345", "fallback_val"); val != "fallback_val" {
		t.Errorf("Expected fallback_val, got %s", val)
	}

	os.Setenv("TEST_ENV_VAR_12345", "actual_val")
	defer os.Unsetenv("TEST_ENV_VAR_12345")

	if val := getEnv("TEST_ENV_VAR_12345", "fallback_val"); val != "actual_val" {
		t.Errorf("Expected actual_val, got %s", val)
	}
}

func TestCalculateBaseUploadSpeed(t *testing.T) {
	cfg := &config.SpeedrrConfig{MaxUpload: 100}

	if speed := calculateBaseUploadSpeed(nil, cfg); speed != 100 {
		t.Errorf("Expected 100 for nil msModule, got %v", speed)
	}

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"MediaContainer": {"size": 0, "Metadata": []}}`))
	}))
	defer mockServer.Close()

	appCfg := &config.SpeedrrConfig{MaxUpload: 100}
	sCfg := config.MediaServerConfig{
		Type: "plex",
		URL:  mockServer.URL,
		StreamBasedSpeeds: &config.StreamBasedSpeedsConfig{
			Enabled: true,
			Speeds:  map[int]interface{}{0: "unlimited", 1: "50%", 2: 25.0, 3: 10},
		},
	}
	msMod, _ := module.NewMediaServerModule(appCfg, []config.MediaServerConfig{sCfg}, func() {})

	// Stream count 0 -> unlimited
	speed := calculateBaseUploadSpeed(msMod, cfg)
	if !math.IsInf(speed, 1) {
		t.Errorf("Expected Inf for stream count 0, got %v", speed)
	}

	// Set stream count to 1 -> 50% of 100 = 50
	msMod.SetStreamCount(mockServer.URL, 1)
	if speed := calculateBaseUploadSpeed(msMod, cfg); speed != 50 {
		t.Errorf("Expected 50 for stream count 1, got %v", speed)
	}

	// Set stream count to 2 -> float64(25.0)
	msMod.SetStreamCount(mockServer.URL, 2)
	if speed := calculateBaseUploadSpeed(msMod, cfg); speed != 25 {
		t.Errorf("Expected 25 for stream count 2, got %v", speed)
	}

	// Set stream count to 3 -> int(10)
	msMod.SetStreamCount(mockServer.URL, 3)
	if speed := calculateBaseUploadSpeed(msMod, cfg); speed != 10 {
		t.Errorf("Expected 10 for stream count 3, got %v", speed)
	}
}

func TestCalculateStreamModeUpload(t *testing.T) {
	// Inf reduction -> Inf
	if speed := calculateStreamModeUpload(100, []float64{math.Inf(1)}, 10, 100); !math.IsInf(speed, 1) {
		t.Errorf("Expected Inf for Inf schedule reduction, got %v", speed)
	}

	// Empty reductions -> baseUpload
	if speed := calculateStreamModeUpload(50, []float64{}, 10, 100); speed != 50 {
		t.Errorf("Expected 50 for empty reductions, got %v", speed)
	}

	// Reductions applied to baseUpload
	if speed := calculateStreamModeUpload(50, []float64{10, 5}, 10, 100); speed != 35 {
		t.Errorf("Expected 35 for 50 - (10+5), got %v", speed)
	}

	// Respect minUpload limit
	if speed := calculateStreamModeUpload(50, []float64{100}, 10, 100); speed != 10 {
		t.Errorf("Expected 10 for minUpload clamp, got %v", speed)
	}
}

func TestAggregateReductions(t *testing.T) {
	// Inf reduction -> Inf
	if val := aggregateReductions([]float64{10, math.Inf(1)}, 5, 100); !math.IsInf(val, 1) {
		t.Errorf("Expected Inf for Inf reduction, got %v", val)
	}

	// Normal reductions
	if val := aggregateReductions([]float64{20, 30}, 5, 100); val != 50 {
		t.Errorf("Expected 50 for 100-(20+30), got %v", val)
	}

	// Clamp to minVal
	if val := aggregateReductions([]float64{200}, 5, 100); val != 5 {
		t.Errorf("Expected 5 for minVal clamp, got %v", val)
	}
}

func TestCalculateTargetSpeeds(t *testing.T) {
	cfg := &config.SpeedrrConfig{
		MinUpload:   10,
		MaxUpload:   100,
		MinDownload: 10,
		MaxDownload: 100,
	}

	// Test normal non-stream-based reductions
	schedModule := module.NewScheduleModule(cfg, []config.ScheduleConfig{}, func() {})
	schedModule.SetReduction(0, 20.0, 30.0)

	up, down := calculateTargetSpeeds(nil, schedModule, cfg)
	if up != 80 || down != 70 {
		t.Errorf("Expected up=80 down=70, got up=%v down=%v", up, down)
	}
}

func TestCalculateEffectiveClientSpeed(t *testing.T) {
	// Inf speed -> Inf
	if eff := calculateEffectiveClientSpeed(math.Inf(1), 1, 2, 1, 2, false); !math.IsInf(eff, 1) {
		t.Errorf("Expected Inf, got %v", eff)
	}

	// Manual share: 1/2 of 100 = 50
	if eff := calculateEffectiveClientSpeed(100, 1, 2, 0, 0, true); eff != 50 {
		t.Errorf("Expected 50 for manual share 1/2, got %v", eff)
	}

	// Active count share: 2/4 of 100 = 50
	if eff := calculateEffectiveClientSpeed(100, 1, 1, 2, 4, false); eff != 50 {
		t.Errorf("Expected 50 for active count share 2/4, got %v", eff)
	}

	// Default fallback: speed
	if eff := calculateEffectiveClientSpeed(100, 1, 1, 0, 0, false); eff != 100 {
		t.Errorf("Expected 100 for fallback, got %v", eff)
	}
}

func TestFetchActiveTorrentCounts(t *testing.T) {
	c1 := &mockClient{cfg: config.ClientConfig{URL: "http://client1"}, count: 3}
	c2 := &mockClient{cfg: config.ClientConfig{URL: "http://client2"}, count: 5}

	clients := []client.TorrentClient{c1, c2}
	counts, total := fetchActiveTorrentCounts(context.Background(), clients)

	if total != 8 {
		t.Errorf("Expected total 8 active torrents, got %d", total)
	}
	if counts[c1] != 3 || counts[c2] != 5 {
		t.Errorf("Expected counts c1=3 c2=5, got c1=%d c2=%d", counts[c1], counts[c2])
	}
}

func TestApplyAndLogSpeeds(t *testing.T) {
	c1 := &mockClient{cfg: config.ClientConfig{URL: "http://client1", UploadShares: 1, DownloadShares: 1}, count: 1}
	cfg := &config.SpeedrrConfig{
		Units:                     "Mbit",
		ManualSpeedAlgorithmShare: true,
	}

	logCalculatedSpeeds(50.0, math.Inf(1), "Mbit")
	applySpeedsToClients(context.Background(), []client.TorrentClient{c1}, cfg, 50.0, math.Inf(1), 1, 1)
}
