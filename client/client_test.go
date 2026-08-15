package client

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/khw315/speedrr/config"
)

func TestNewClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result": "success", "arguments": {}}`))
	}))
	defer server.Close()

	appCfg := &config.SpeedrrConfig{Units: "Mbit"}

	qbitCfg := config.ClientConfig{
		Type: "qbittorrent",
		URL:  server.URL,
	}
	client, err := NewClient(appCfg, qbitCfg)
	if err != nil {
		t.Fatalf("Failed to create qbittorrent client: %v", err)
	}
	if client.Config().Type != "qbittorrent" {
		t.Errorf("Expected type qbittorrent, got %s", client.Config().Type)
	}

	transCfg := config.ClientConfig{
		Type: "transmission",
		URL:  server.URL,
	}
	client, err = NewClient(appCfg, transCfg)
	if err != nil {
		t.Fatalf("Failed to create transmission client: %v", err)
	}
	if client.Config().Type != "transmission" {
		t.Errorf("Expected type transmission, got %s", client.Config().Type)
	}

	invalidCfg := config.ClientConfig{
		Type: "invalid_type",
	}
	_, err = NewClient(appCfg, invalidCfg)
	if err == nil {
		t.Errorf("Expected error for invalid client type, got nil")
	}
}

func TestQBittorrentClientMock(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "test-sid-session"})
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprintln(w, "Ok.")
		case "/api/v2/torrents/info":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[
				{"hash": "1", "state": "downloading"},
				{"hash": "2", "state": "uploading"},
				{"hash": "3", "state": "paused"}
			]`))
		case "/api/v2/transfer/setUploadLimit", "/api/v2/transfer/setDownloadLimit":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	appCfg := &config.SpeedrrConfig{Units: "Mbit"}
	clientCfg := config.ClientConfig{
		Type:     "qbittorrent",
		URL:      server.URL,
		Username: "admin",
		Password: "password",
	}

	client, err := NewQBittorrentClient(appCfg, clientCfg)
	if err != nil {
		t.Fatalf("Failed to create QBittorrentClient: %v", err)
	}

	ctx := context.Background()

	count, err := client.GetActiveTorrentCount(ctx)
	if err != nil {
		t.Fatalf("GetActiveTorrentCount failed: %v", err)
	}
	if count != 2 {
		t.Errorf("Expected 2 active torrents, got %d", count)
	}

	if err := client.SetUploadSpeed(ctx, 10.0); err != nil {
		t.Errorf("SetUploadSpeed failed: %v", err)
	}
	if err := client.SetUploadSpeed(ctx, math.Inf(1)); err != nil {
		t.Errorf("SetUploadSpeed unlimited failed: %v", err)
	}

	if err := client.SetDownloadSpeed(ctx, 20.0); err != nil {
		t.Errorf("SetDownloadSpeed failed: %v", err)
	}
	if err := client.SetDownloadSpeed(ctx, math.Inf(1)); err != nil {
		t.Errorf("SetDownloadSpeed unlimited failed: %v", err)
	}
}

func TestQBittorrentClientErrors(t *testing.T) {
	// 403 Forbidden login (banned)
	bannedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer bannedServer.Close()

	appCfg := &config.SpeedrrConfig{Units: "Mbit"}
	bannedCfg := config.ClientConfig{Type: "qbittorrent", URL: bannedServer.URL}
	if _, err := NewQBittorrentClient(appCfg, bannedCfg); err == nil {
		t.Errorf("Expected error for 403 login, got nil")
	}

	// 500 Server Error login
	errServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer errServer.Close()
	errCfg := config.ClientConfig{Type: "qbittorrent", URL: errServer.URL}
	if _, err := NewQBittorrentClient(appCfg, errCfg); err == nil {
		t.Errorf("Expected error for 500 login, got nil")
	}

	// Invalid unit conversion error
	validServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer validServer.Close()
	invalidUnitCfg := &config.SpeedrrConfig{Units: "invalid_unit"}
	client, err := NewQBittorrentClient(invalidUnitCfg, config.ClientConfig{Type: "qbittorrent", URL: validServer.URL})
	if err != nil {
		t.Fatalf("Client creation failed: %v", err)
	}
	if err := client.SetUploadSpeed(context.Background(), 10.0); err == nil {
		t.Errorf("Expected error for invalid unit in SetUploadSpeed, got nil")
	}
}

func TestTransmissionClientMock(t *testing.T) {
	csrfHeaderKey := "X-Transmission-Session-Id"
	csrfHeaderVal := "test-session-id-123"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(csrfHeaderKey) == "" {
			w.Header().Set(csrfHeaderKey, csrfHeaderVal)
			w.WriteHeader(http.StatusConflict)
			return
		}

		if r.URL.Path == "/transmission/rpc" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"result": "success",
				"arguments": {
					"activeTorrentCount": 2
				}
			}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	appCfg := &config.SpeedrrConfig{Units: "Mbit"}
	clientCfg := config.ClientConfig{
		Type: "transmission",
		URL:  server.URL,
	}

	client, err := NewTransmissionClient(appCfg, clientCfg)
	if err != nil {
		t.Fatalf("Failed to create TransmissionClient: %v", err)
	}

	ctx := context.Background()

	count, err := client.GetActiveTorrentCount(ctx)
	if err != nil {
		t.Fatalf("GetActiveTorrentCount failed: %v", err)
	}
	if count != 2 {
		t.Errorf("Expected 2 active torrents, got %d", count)
	}

	if err := client.SetUploadSpeed(ctx, 15.0); err != nil {
		t.Errorf("SetUploadSpeed failed: %v", err)
	}
	if err := client.SetUploadSpeed(ctx, math.Inf(1)); err != nil {
		t.Errorf("SetUploadSpeed unlimited failed: %v", err)
	}

	if err := client.SetDownloadSpeed(ctx, 25.0); err != nil {
		t.Errorf("SetDownloadSpeed failed: %v", err)
	}
	if err := client.SetDownloadSpeed(ctx, math.Inf(1)); err != nil {
		t.Errorf("SetDownloadSpeed unlimited failed: %v", err)
	}
}

func TestTransmissionClientErrors(t *testing.T) {
	// 401 Unauthorized
	unauthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer unauthServer.Close()

	appCfg := &config.SpeedrrConfig{Units: "Mbit"}
	unauthCfg := config.ClientConfig{Type: "transmission", URL: unauthServer.URL}
	if _, err := NewTransmissionClient(appCfg, unauthCfg); err == nil {
		t.Errorf("Expected error for 401 transmission client, got nil")
	}

	// Invalid unit conversion error
	validServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result": "success", "arguments": {}}`))
	}))
	defer validServer.Close()

	badUnitCfg := &config.SpeedrrConfig{Units: "bad_unit"}
	client, err := NewTransmissionClient(badUnitCfg, config.ClientConfig{Type: "transmission", URL: validServer.URL})
	if err != nil {
		t.Fatalf("Client creation failed: %v", err)
	}
	if err := client.SetUploadSpeed(context.Background(), 10.0); err == nil {
		t.Errorf("Expected error for invalid unit in SetUploadSpeed, got nil")
	}
}
