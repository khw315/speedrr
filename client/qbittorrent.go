package client

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/khw315/speedrr/config"
	"github.com/khw315/speedrr/logger"
	"github.com/khw315/speedrr/units"
)

const (
	contentTypeHeader = "Content-Type"
	formURLEncoded    = "application/x-www-form-urlencoded"
)

type QBittorrentClient struct {
	mu           sync.Mutex
	appConfig    *config.SpeedrrConfig
	clientConfig config.ClientConfig
	httpClient   *http.Client
	baseURL      string
	loggedIn     bool
}

type qbitTorrentInfo struct {
	State string `json:"state"`
}

func NewQBittorrentClient(appConfig *config.SpeedrrConfig, clientConfig config.ClientConfig) (*QBittorrentClient, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("<qbit|%s> cookiejar error: %w", clientConfig.URL, err)
	}

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: !clientConfig.HTTPSVerify,
		},
	}

	httpClient := &http.Client{
		Jar:       jar,
		Transport: transport,
		Timeout:   15 * time.Second,
	}

	baseURL := strings.TrimRight(clientConfig.URL, "/")

	c := &QBittorrentClient{
		appConfig:    appConfig,
		clientConfig: clientConfig,
		httpClient:   httpClient,
		baseURL:      baseURL,
	}

	logger.Debug("<qbit|%s> Connecting to qBittorrent at %s", clientConfig.URL, clientConfig.URL)
	if err := c.login(context.Background()); err != nil {
		return nil, err
	}
	logger.Debug("<qbit|%s> Connected to qBittorrent", clientConfig.URL)

	return c, nil
}

func (c *QBittorrentClient) login(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	data := url.Values{}
	data.Set("username", c.clientConfig.Username)
	data.Set("password", c.clientConfig.Password)

	loginURL := fmt.Sprintf("%s/api/v2/auth/login", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL, strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Errorf("<qbit|%s> failed to create login request: %w", c.clientConfig.URL, err)
	}
	req.Header.Set(contentTypeHeader, formURLEncoded)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("<qbit|%s> login request failed: %w", c.clientConfig.URL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("<qbit|%s> Failed to login to qBittorrent, temporarily banned, try again later", c.clientConfig.URL)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("<qbit|%s> Failed to login to qBittorrent, check your credentials (status %d)", c.clientConfig.URL, resp.StatusCode)
	}


	c.loggedIn = true
	return nil
}

func (c *QBittorrentClient) Config() config.ClientConfig {
	return c.clientConfig
}

func isQbitActiveState(state string) bool {
	s := strings.ToLower(state)
	return strings.Contains(s, "dl") || strings.Contains(s, "up") ||
		strings.Contains(s, "downloading") || strings.Contains(s, "uploading")
}

func (c *QBittorrentClient) GetActiveTorrentCount(ctx context.Context) (int, error) {
	logger.Debug("<qbit|%s> Getting active torrent count", c.clientConfig.URL)

	apiURL := fmt.Sprintf("%s/api/v2/torrents/info", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return 0, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		// Try re-login once
		if relogErr := c.login(ctx); relogErr == nil {
			return c.GetActiveTorrentCount(ctx)
		}
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("qBittorrent returned HTTP status %d", resp.StatusCode)
	}

	var torrents []qbitTorrentInfo
	if err := json.NewDecoder(resp.Body).Decode(&torrents); err != nil {
		return 0, err
	}

	count := 0
	for _, t := range torrents {
		if isQbitActiveState(t.State) {
			count++
		}
	}
	return count, nil
}

func (c *QBittorrentClient) SetUploadSpeed(ctx context.Context, speed float64) error {
	var limitBytes int64
	if math.IsInf(speed, 1) {
		logger.Debug("<qbit|%s> Setting upload speed to unlimited", c.clientConfig.URL)
		limitBytes = 0
	} else {
		logger.Debug("<qbit|%s> Setting upload speed to %v%s", c.clientConfig.URL, speed, c.appConfig.Units)
		converted, err := units.Convert(speed, c.appConfig.Units, "B")
		if err != nil {
			return err
		}
		limitBytes = int64(math.Max(1, math.Round(converted)))
	}

	data := url.Values{}
	data.Set("limit", fmt.Sprintf("%d", limitBytes))

	apiURL := fmt.Sprintf("%s/api/v2/transfer/setUploadLimit", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set(contentTypeHeader, formURLEncoded)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("qBittorrent returned HTTP status %d when setting upload limit", resp.StatusCode)
	}
	return nil
}

func (c *QBittorrentClient) SetDownloadSpeed(ctx context.Context, speed float64) error {
	var limitBytes int64
	if math.IsInf(speed, 1) {
		logger.Debug("<qbit|%s> Setting download speed to unlimited", c.clientConfig.URL)
		limitBytes = 0
	} else {
		logger.Debug("<qbit|%s> Setting download speed to %v%s", c.clientConfig.URL, speed, c.appConfig.Units)
		converted, err := units.Convert(speed, c.appConfig.Units, "B")
		if err != nil {
			return err
		}
		limitBytes = int64(math.Max(1, math.Round(converted)))
	}

	data := url.Values{}
	data.Set("limit", fmt.Sprintf("%d", limitBytes))

	apiURL := fmt.Sprintf("%s/api/v2/transfer/setDownloadLimit", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set(contentTypeHeader, formURLEncoded)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("qBittorrent returned HTTP status %d when setting download limit", resp.StatusCode)
	}
	return nil
}
