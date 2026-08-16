package module

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/khw315/speedrr/config"
)

func TestWebhookModule_Health(t *testing.T) {
	appCfg := &config.SpeedrrConfig{MaxUpload: 100}
	wCfg := &config.WebhookConfig{Enabled: true, Port: 8080}
	m := NewWebhookModule(appCfg, wCfg, func() {})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	m.server.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["status"] != "healthy" {
		t.Errorf("expected healthy, got %s", resp["status"])
	}
}

func TestWebhookModule_Authentication(t *testing.T) {
	appCfg := &config.SpeedrrConfig{MaxUpload: 100}
	wCfg := &config.WebhookConfig{Enabled: true, Port: 8080, Token: "secret123"}
	m := NewWebhookModule(appCfg, wCfg, func() {})

	payload := WebhookStreamPayload{
		Event:             "stream_started",
		Service:           "youtube",
		ActiveStreamCount: 1,
	}
	body, _ := json.Marshal(payload)

	// 1. Missing Token -> 401
	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/webhook/stream", bytes.NewReader(body))
	w1 := httptest.NewRecorder()
	m.server.Handler.ServeHTTP(w1, req1)
	if w1.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", w1.Code)
	}

	// 2. Bearer Token -> 200
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/webhook/stream", bytes.NewReader(body))
	req2.Header.Set("Authorization", "Bearer secret123")
	w2 := httptest.NewRecorder()
	m.server.Handler.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Errorf("expected 200 OK with Bearer, got %d", w2.Code)
	}

	// 3. X-API-Key Header -> 200
	req3 := httptest.NewRequest(http.MethodPost, "/api/v1/webhook/stream", bytes.NewReader(body))
	req3.Header.Set("X-API-Key", "secret123")
	w3 := httptest.NewRecorder()
	m.server.Handler.ServeHTTP(w3, req3)
	if w3.Code != http.StatusOK {
		t.Errorf("expected 200 OK with X-API-Key, got %d", w3.Code)
	}

	// 4. Query Token -> 200
	req4 := httptest.NewRequest(http.MethodPost, "/api/v1/webhook/stream?token=secret123", bytes.NewReader(body))
	w4 := httptest.NewRecorder()
	m.server.Handler.ServeHTTP(w4, req4)
	if w4.Code != http.StatusOK {
		t.Errorf("expected 200 OK with query param, got %d", w4.Code)
	}
}

func TestWebhookModule_StreamLifecycle(t *testing.T) {
	appCfg := &config.SpeedrrConfig{MaxUpload: 100}
	wCfg := &config.WebhookConfig{
		Enabled: true,
		Port:    8080,
		StreamBasedSpeeds: &config.StreamBasedSpeedsConfig{
			Enabled: true,
			Speeds:  map[int]interface{}{0: "unlimited", 1: 50.0, 2: 20.0},
		},
	}

	var updateCount int32
	notify := func() {
		atomic.AddInt32(&updateCount, 1)
	}

	m := NewWebhookModule(appCfg, wCfg, notify)

	if m.Port() != 8080 {
		t.Errorf("expected port 8080, got %d", m.Port())
	}

	// 1. Stream started from YouTube
	p1 := WebhookStreamPayload{
		Event:             "stream_started",
		Service:           "youtube",
		TargetIP:          "192.168.0.10",
		ActiveStreamCount: 1,
		Matched:           "googlevideo.com",
	}
	body1, _ := json.Marshal(p1)
	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/webhook/stream", bytes.NewReader(body1))
	w1 := httptest.NewRecorder()
	m.server.Handler.ServeHTTP(w1, req1)

	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w1.Code)
	}
	if count := m.GetStreamCount(); count != 1 {
		t.Errorf("expected stream count 1, got %d", count)
	}
	if speed := m.GetTargetUploadSpeedForCount(1); speed != 50.0 {
		t.Errorf("expected target speed 50.0 for 1 stream, got %v", speed)
	}

	// 2. Stream update (2 clients streaming)
	p2 := WebhookStreamPayload{
		Event:             "stream_update",
		Service:           "youtube",
		TargetIP:          "192.168.0.15",
		ActiveStreamCount: 2,
		Matched:           "googlevideo.com",
	}
	body2, _ := json.Marshal(p2)
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/webhook/stream", bytes.NewReader(body2))
	w2 := httptest.NewRecorder()
	m.server.Handler.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w2.Code)
	}
	if count := m.GetStreamCount(); count != 2 {
		t.Errorf("expected stream count 2, got %d", count)
	}
	if speed := m.GetTargetUploadSpeedForCount(2); speed != 20.0 {
		t.Errorf("expected target speed 20.0 for 2 streams, got %v", speed)
	}

	// 3. Stream stopped
	p3 := WebhookStreamPayload{
		Event:             "stream_stopped",
		Service:           "youtube",
		TargetIP:          "192.168.0.15",
		ActiveStreamCount: 0,
	}
	body3, _ := json.Marshal(p3)
	req3 := httptest.NewRequest(http.MethodPost, "/api/v1/webhook/stream", bytes.NewReader(body3))
	w3 := httptest.NewRecorder()
	m.server.Handler.ServeHTTP(w3, req3)

	if w3.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w3.Code)
	}
	if count := m.GetStreamCount(); count != 0 {
		t.Errorf("expected stream count 0, got %d", count)
	}
	if speed := m.GetTargetUploadSpeedForCount(0); speed != "unlimited" {
		t.Errorf("expected unlimited speed for 0 streams, got %v", speed)
	}

	// 4. Test GetReductionValue in stream mode
	up, down := m.GetReductionValue()
	if !math.IsInf(up, -1) || down != 0 {
		t.Errorf("expected -Inf upload reduction for stream mode, got up=%v, down=%v", up, down)
	}

	// Verify callback notification count
	if atomic.LoadInt32(&updateCount) < 3 {
		t.Errorf("expected at least 3 callback notifications, got %d", atomic.LoadInt32(&updateCount))
	}
}

func TestWebhookModule_ExplicitReductions(t *testing.T) {
	appCfg := &config.SpeedrrConfig{MaxUpload: 100}
	wCfg := &config.WebhookConfig{Enabled: true, Port: 8080}
	m := NewWebhookModule(appCfg, wCfg, func() {})

	upRed := 15.5
	downRed := 25.0
	payload := WebhookStreamPayload{
		Event:             "custom_event",
		Service:           "custom",
		UploadReduction:   &upRed,
		DownloadReduction: &downRed,
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhook", bytes.NewReader(body))
	w := httptest.NewRecorder()
	m.server.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}

	up, down := m.GetReductionValue()
	if up != 15.5 || down != 25.0 {
		t.Errorf("expected up=15.5 down=25.0, got up=%v down=%v", up, down)
	}
}

func TestWebhookModule_InvalidRequests(t *testing.T) {
	appCfg := &config.SpeedrrConfig{MaxUpload: 100}
	wCfg := &config.WebhookConfig{Enabled: true, Port: 8080}
	m := NewWebhookModule(appCfg, wCfg, func() {})

	// GET on stream endpoint -> 405 Method Not Allowed
	req1 := httptest.NewRequest(http.MethodGet, "/api/v1/webhook/stream", nil)
	w1 := httptest.NewRecorder()
	m.server.Handler.ServeHTTP(w1, req1)
	if w1.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 Method Not Allowed, got %d", w1.Code)
	}

	// Invalid JSON body -> 400 Bad Request
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/webhook/stream", bytes.NewReader([]byte("{invalid-json")))
	w2 := httptest.NewRecorder()
	m.server.Handler.ServeHTTP(w2, req2)
	if w2.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", w2.Code)
	}
}

func TestWebhookModule_RunShutdown(t *testing.T) {
	appCfg := &config.SpeedrrConfig{MaxUpload: 100}
	// Use an ephemeral local port
	wCfg := &config.WebhookConfig{Enabled: true, BindAddress: "127.0.0.1", Port: 59381}
	m := NewWebhookModule(appCfg, wCfg, func() {})

	ctx, cancel := context.WithCancel(context.Background())
	m.Run(ctx)

	// Wait for server to start
	time.Sleep(50 * time.Millisecond)

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/health", wCfg.Port))
	if err != nil {
		t.Fatalf("failed to connect to running webhook server: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", resp.StatusCode)
	}

	// Shutdown server
	cancel()
	time.Sleep(50 * time.Millisecond)
}
