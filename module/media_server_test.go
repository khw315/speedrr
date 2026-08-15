package module

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/khw315/speedrr/config"
)

func TestParseFlexibleInt(t *testing.T) {
	tests := []struct {
		val      interface{}
		expected int
	}{
		{100, 100},
		{int64(200), 200},
		{float64(300.7), 300},
		{"400", 400},
		{nil, 0},
		{"invalid", 0},
	}

	for _, tt := range tests {
		got := parseFlexibleInt(tt.val)
		if got != tt.expected {
			t.Errorf("parseFlexibleInt(%v) = %d; want %d", tt.val, got, tt.expected)
		}
	}
}

func TestIsPrivateIPAndMatchesIPNetwork(t *testing.T) {
	if !isPrivateIP("lan") {
		t.Errorf("Expected lan to be private IP")
	}
	if !isPrivateIP("127.0.0.1:8080") {
		t.Errorf("Expected loopback to be private IP")
	}
	if !isPrivateIP("192.168.1.100") {
		t.Errorf("Expected 192.168.x.x to be private IP")
	}
	if isPrivateIP("8.8.8.8") {
		t.Errorf("Expected 8.8.8.8 NOT to be private IP")
	}

	if !matchesIPNetwork("10.0.0.5:1234", []string{"10.0.0.0/8"}) {
		t.Errorf("Expected 10.0.0.5 to match 10.0.0.0/8 CIDR")
	}
	if matchesIPNetwork("8.8.8.8", []string{"10.0.0.0/8"}) {
		t.Errorf("Expected 8.8.8.8 NOT to match 10.0.0.0/8 CIDR")
	}
	if matchesIPNetwork("invalid_ip", []string{"10.0.0.0/8"}) {
		t.Errorf("Expected invalid IP NOT to match CIDR")
	}
}

func TestResolveSpeedForServerAndTargetUploadSpeed(t *testing.T) {
	speedsCfg := &config.StreamBasedSpeedsConfig{
		Enabled: true,
		Speeds: map[int]interface{}{
			0: "unlimited",
			2: 50.0,
		},
		Default: 10.0,
	}

	// Exact match
	if res := resolveSpeedForServer(speedsCfg, 0, 100.0); res != "unlimited" {
		t.Errorf("Expected 'unlimited' for 0 streams, got %v", res)
	}

	// Fallback to step <= streams: for 3 streams, key 2 applies -> 50.0
	if res := resolveSpeedForServer(speedsCfg, 3, 100.0); res != 50.0 {
		t.Errorf("Expected 50.0 for 3 streams, got %v", res)
	}

	// Default fallback
	emptySpeedsCfg := &config.StreamBasedSpeedsConfig{
		Enabled: true,
		Speeds:  map[int]interface{}{},
		Default: 15.0,
	}
	if res := resolveSpeedForServer(emptySpeedsCfg, 5, 100.0); res != 15.0 {
		t.Errorf("Expected 15.0 default speed, got %v", res)
	}

	// Max upload fallback
	noDefaultCfg := &config.StreamBasedSpeedsConfig{
		Enabled: true,
		Speeds:  map[int]interface{}{},
	}
	if res := resolveSpeedForServer(noDefaultCfg, 5, 100.0); res != 100.0 {
		t.Errorf("Expected 100.0 maxUpload fallback, got %v", res)
	}
}

func TestMediaServerModuleReductions(t *testing.T) {
	updated := false
	notify := func() {
		updated = true
	}

	appCfg := &config.SpeedrrConfig{Units: "Mbit", MaxUpload: 100}
	sCfg := config.MediaServerConfig{
		Type: "plex",
		URL:  "http://server1",
		StreamBasedSpeeds: &config.StreamBasedSpeedsConfig{
			Enabled: true,
			Speeds:  map[int]interface{}{0: "unlimited"},
		},
	}

	mod := &MediaServerModule{
		appConfig:            appCfg,
		serverConfigs:        []config.MediaServerConfig{sCfg},
		reductionValueDict:   make(map[string]float64),
		streamCountDict:      make(map[string]int),
		notifyUpdateCallback: notify,
	}

	mod.SetReduction("http://server1", 50.0)
	if !updated {
		t.Errorf("Expected notify callback on SetReduction")
	}

	upRed, _ := mod.GetReductionValue()
	if !math.IsInf(upRed, -1) {
		t.Errorf("Expected math.Inf(-1) stream based upload reduction, got %v", upRed)
	}

	target := mod.GetTargetUploadSpeed()
	if target != "unlimited" {
		t.Errorf("Expected 'unlimited' target upload speed, got %v", target)
	}

	mod.SetStreamCount("http://server1", 3)
	if count := mod.GetStreamCount(); count != 3 {
		t.Errorf("Expected total stream count 3, got %d", count)
	}
}

func TestSessionPausedAndLocalIgnored(t *testing.T) {
	base := &BaseServer{
		serverConfig: config.MediaServerConfig{
			URL: "http://server1",
			IgnoreStreams: config.IgnoreStreamConfig{
				Local:       true,
				PausedAfter: 1, // 1 second
			},
		},
		pausedSince: make(map[string]time.Time),
	}

	// Local stream ignored
	if !base.isLocalStream("192.168.1.50") {
		t.Errorf("Expected local IP to be ignored")
	}

	// Paused stream ignored after threshold
	base.pausedSince["s1"] = time.Now().Add(-2 * time.Second)
	if !base.isSessionPausedIgnored(true, "s1", "Movie") {
		t.Errorf("Expected session paused for >1s to be ignored")
	}

	// Remove old paused sessions
	base.removeOldPaused(map[string]bool{})
	if _, ok := base.pausedSince["s1"]; ok {
		t.Errorf("Expected old paused session s1 to be removed")
	}
}

func TestPlexServerMock(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status/sessions" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"MediaContainer": {
					"size": 1,
					"Metadata": [
						{
							"title": "Test Movie",
							"Session": {
								"id": "s1",
								"bandwidth": 2000
							},
							"Player": {
								"state": "playing",
								"address": "8.8.8.8"
							}
						}
					]
				}
			}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	appCfg := &config.SpeedrrConfig{Units: "Mbit"}
	sCfg := config.MediaServerConfig{
		Type:   "plex",
		URL:    server.URL,
		APIKey: "test-token",
	}

	mod, err := NewMediaServerModule(appCfg, []config.MediaServerConfig{sCfg}, func() {})
	if err != nil {
		t.Fatalf("Failed to create MediaServerModule for Plex: %v", err)
	}

	if len(mod.servers) != 1 {
		t.Fatalf("Expected 1 server in module, got %d", len(mod.servers))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	mod.Run(ctx)

	bw, err := mod.servers[0].GetBandwidth(ctx)
	if err != nil {
		t.Fatalf("Plex GetBandwidth failed: %v", err)
	}
	if bw <= 0 {
		t.Errorf("Expected positive bandwidth for Plex session, got %d", bw)
	}
}

func TestTautulliServerMock(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cmd") == "get_activity" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"response": {
					"result": "success",
					"message": "",
					"data": {
						"sessions": [
							{
								"session_id": "s1",
								"bandwidth": 3000,
								"state": "playing",
								"ip_address": "8.8.8.8",
								"full_title": "Test Show"
							}
						]
					}
				}
			}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	appCfg := &config.SpeedrrConfig{Units: "Mbit"}
	sCfg := config.MediaServerConfig{
		Type:   "tautulli",
		URL:    server.URL,
		APIKey: "test-apikey",
	}

	mod, err := NewMediaServerModule(appCfg, []config.MediaServerConfig{sCfg}, func() {})
	if err != nil {
		t.Fatalf("Failed to create MediaServerModule for Tautulli: %v", err)
	}

	ctx := context.Background()
	bw, err := mod.servers[0].GetBandwidth(ctx)
	if err != nil {
		t.Fatalf("Tautulli GetBandwidth failed: %v", err)
	}
	if bw <= 0 {
		t.Errorf("Expected positive bandwidth for Tautulli session, got %d", bw)
	}
}

func TestJellyfinAndEmbyServerMock(t *testing.T) {
	jellyfinServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/Sessions" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[
				{
					"Id": "j1",
					"RemoteEndPoint": "8.8.8.8:1234",
					"NowPlayingItem": {
						"Name": "Jellyfin Movie",
						"MediaStreams": [{"BitRate": 4000000}]
					},
					"PlayState": {
						"IsPaused": false,
						"PlayMethod": "DirectPlay"
					}
				}
			]`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer jellyfinServer.Close()

	appCfg := &config.SpeedrrConfig{Units: "Mbit"}
	sCfgJellyfin := config.MediaServerConfig{
		Type:   "jellyfin",
		URL:    jellyfinServer.URL,
		APIKey: "test-jellyfin-token",
	}

	modJellyfin, err := NewMediaServerModule(appCfg, []config.MediaServerConfig{sCfgJellyfin}, func() {})
	if err != nil {
		t.Fatalf("Failed to create MediaServerModule for Jellyfin: %v", err)
	}

	ctx := context.Background()
	bwJellyfin, err := modJellyfin.servers[0].GetBandwidth(ctx)
	if err != nil {
		t.Fatalf("Jellyfin GetBandwidth failed: %v", err)
	}
	if bwJellyfin <= 0 {
		t.Errorf("Expected positive bandwidth for Jellyfin session, got %d", bwJellyfin)
	}

	embyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/Sessions" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[
				{
					"Id": "e1",
					"RemoteEndPoint": "8.8.8.8:5678",
					"NowPlayingItem": {
						"Name": "Emby Movie",
						"MediaStreams": [{"BitRate": 5000000}]
					},
					"PlayState": {
						"IsPaused": false,
						"PlayMethod": "DirectStream"
					}
				}
			]`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer embyServer.Close()

	sCfgEmby := config.MediaServerConfig{
		Type:   "emby",
		URL:    embyServer.URL,
		APIKey: "test-emby-key",
	}

	modEmby, err := NewMediaServerModule(appCfg, []config.MediaServerConfig{sCfgEmby}, func() {})
	if err != nil {
		t.Fatalf("Failed to create MediaServerModule for Emby: %v", err)
	}

	bwEmby, err := modEmby.servers[0].GetBandwidth(ctx)
	if err != nil {
		t.Fatalf("Emby GetBandwidth failed: %v", err)
	}
	if bwEmby <= 0 {
		t.Errorf("Expected positive bandwidth for Emby session, got %d", bwEmby)
	}

	invalidCfg := config.MediaServerConfig{
		Type: "unknown_type",
	}
	_, err = NewMediaServerModule(appCfg, []config.MediaServerConfig{invalidCfg}, func() {})
	if err == nil {
		t.Errorf("Expected error for unknown media server type, got nil")
	}
}
