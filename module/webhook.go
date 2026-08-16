package module

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/khw315/speedrr/config"
	"github.com/khw315/speedrr/logger"
)

// WebhookStreamPayload represents inbound streaming webhook JSON events.
type WebhookStreamPayload struct {
	Event             string   `json:"event"`
	State             string   `json:"state"`
	Service           string   `json:"service"`
	TargetIP          string   `json:"target_ip"`
	ActiveClients     []string `json:"active_clients"`
	ActiveStreamCount int      `json:"active_stream_count"`
	Matched           string   `json:"matched"`
	Protocol          string   `json:"protocol"`
	UploadReduction   *float64 `json:"upload_reduction,omitempty"`
	DownloadReduction *float64 `json:"download_reduction,omitempty"`
}

// WebhookModule provides an HTTP webhook server for receiving external streaming and speed events.
type WebhookModule struct {
	mu                   sync.Mutex
	appConfig            *config.SpeedrrConfig
	webhookConfig        *config.WebhookConfig
	streamCountDict      map[string]int
	reductionUpload      float64
	reductionDownload    float64
	server               *http.Server
	notifyUpdateCallback func()
}

// NewWebhookModule initializes a new WebhookModule.
func NewWebhookModule(appConfig *config.SpeedrrConfig, webhookConfig *config.WebhookConfig, notifyUpdate func()) *WebhookModule {
	m := &WebhookModule{
		appConfig:            appConfig,
		webhookConfig:        webhookConfig,
		streamCountDict:      make(map[string]int),
		notifyUpdateCallback: notifyUpdate,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/webhook/stream", m.handleStreamWebhook)
	mux.HandleFunc("/api/v1/webhook", m.handleStreamWebhook)
	mux.HandleFunc("/webhook", m.handleStreamWebhook)
	mux.HandleFunc("/api/v1/health", m.handleHealth)
	mux.HandleFunc("/health", m.handleHealth)

	bindAddr := fmt.Sprintf("%s:%d", webhookConfig.BindAddress, webhookConfig.Port)
	m.server = &http.Server{
		Addr:         bindAddr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	return m
}

// Port returns the listening port of the webhook server.
func (m *WebhookModule) Port() int {
	return m.webhookConfig.Port
}

// Run starts the HTTP webhook listener in a background goroutine.
func (m *WebhookModule) Run(ctx context.Context) {
	go func() {
		logger.Info("<webhook> HTTP server listening on %s", m.server.Addr)
		if err := m.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Warning("<webhook> HTTP server error: %v", err)
		}
	}()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = m.server.Shutdown(shutdownCtx)
		logger.Info("<webhook> HTTP server stopped")
	}()
}

// SetStreamCount updates the active stream count for a source key directly (useful for tests).
func (m *WebhookModule) SetStreamCount(sourceKey string, count int) {
	m.mu.Lock()
	m.streamCountDict[sourceKey] = count
	m.mu.Unlock()
	if m.notifyUpdateCallback != nil {
		m.notifyUpdateCallback()
	}
}

// GetStreamCount returns the sum of all active streaming streams reported via webhook.
func (m *WebhookModule) GetStreamCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	total := 0
	for _, count := range m.streamCountDict {
		total += count
	}
	return total
}

// GetTargetUploadSpeedForCount returns target upload speed for a given stream count if stream-based speeds are configured.
func (m *WebhookModule) GetTargetUploadSpeedForCount(totalStreams int) interface{} {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.webhookConfig != nil && m.webhookConfig.StreamBasedSpeeds != nil && m.webhookConfig.StreamBasedSpeeds.Enabled {
		return resolveSpeedForServer(m.webhookConfig.StreamBasedSpeeds, totalStreams, m.appConfig.MaxUpload)
	}
	return nil
}

// GetReductionValue calculates the upload and download reductions from the webhook module.
func (m *WebhookModule) GetReductionValue() (float64, float64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.webhookConfig != nil && m.webhookConfig.StreamBasedSpeeds != nil && m.webhookConfig.StreamBasedSpeeds.Enabled {
		totalStreams := 0
		for _, count := range m.streamCountDict {
			totalStreams += count
		}
		logger.Info("<webhook> Total active streams: %d", totalStreams)
		return math.Inf(-1), 0
	}

	return m.reductionUpload, m.reductionDownload
}

func (m *WebhookModule) authenticateRequest(r *http.Request) bool {
	if m.webhookConfig.Token == "" {
		return true
	}

	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") && strings.TrimPrefix(authHeader, "Bearer ") == m.webhookConfig.Token {
		return true
	}
	if r.Header.Get("X-API-Key") == m.webhookConfig.Token {
		return true
	}
	if r.URL.Query().Get("token") == m.webhookConfig.Token {
		return true
	}

	return false
}

func (m *WebhookModule) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "healthy",
		"module": "speedrr-webhook",
	})
}

func (m *WebhookModule) handleStreamWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	if !m.authenticateRequest(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var payload WebhookStreamPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, fmt.Sprintf("Bad Request: %v", err), http.StatusBadRequest)
		return
	}

	sourceKey := payload.Service
	if sourceKey == "" {
		sourceKey = payload.TargetIP
	}
	if sourceKey == "" {
		sourceKey = "external_stream"
	}

	m.mu.Lock()
	isStopping := strings.EqualFold(payload.Event, "stream_stopped") || strings.EqualFold(payload.State, "IDLE")
	if isStopping {
		m.streamCountDict[sourceKey] = 0
		logger.Info("<webhook> Stream stopped for source: %s", sourceKey)
	} else {
		count := payload.ActiveStreamCount
		if count <= 0 {
			count = 1
		}
		m.streamCountDict[sourceKey] = count
		logger.Info("<webhook> Stream active for source: %s (Count: %d, Event: %s, Matched: %s)", sourceKey, count, payload.Event, payload.Matched)
	}

	if payload.UploadReduction != nil {
		m.reductionUpload = *payload.UploadReduction
	}
	if payload.DownloadReduction != nil {
		m.reductionDownload = *payload.DownloadReduction
	}

	totalStreams := 0
	for _, cnt := range m.streamCountDict {
		totalStreams += cnt
	}
	m.mu.Unlock()

	if m.notifyUpdateCallback != nil {
		m.notifyUpdateCallback()
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":              "ok",
		"source":              sourceKey,
		"active_stream_count": totalStreams,
	})
}
