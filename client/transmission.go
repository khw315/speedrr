package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/khw315/speedrr/config"
	"github.com/khw315/speedrr/logger"
	"github.com/khw315/speedrr/units"
)

type TransmissionClient struct {
	mu           sync.Mutex
	appConfig    *config.SpeedrrConfig
	clientConfig config.ClientConfig
	httpClient   *http.Client
	rpcURL       string
	sessionID    string
}

type rpcRequest struct {
	Method    string      `json:"method"`
	Arguments interface{} `json:"arguments,omitempty"`
}

type rpcResponse struct {
	Result    string                 `json:"result"`
	Arguments map[string]interface{} `json:"arguments"`
}

func NewTransmissionClient(appConfig *config.SpeedrrConfig, clientConfig config.ClientConfig) (*TransmissionClient, error) {
	u, err := url.Parse(clientConfig.URL)
	if err != nil {
		return nil, fmt.Errorf("<trans|%s> invalid URL: %w", clientConfig.URL, err)
	}

	rpcPath := u.Path
	if rpcPath == "" || rpcPath == "/" {
		rpcPath = "/transmission/rpc"
	}
	u.Path = rpcPath
	rpcURL := u.String()

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: !clientConfig.HTTPSVerify,
		},
	}

	httpClient := &http.Client{
		Transport: transport,
		Timeout:   15 * time.Second,
	}

	c := &TransmissionClient{
		appConfig:    appConfig,
		clientConfig: clientConfig,
		httpClient:   httpClient,
		rpcURL:       rpcURL,
	}

	logger.Debug("<trans|%s> Connecting to Transmission at %s", clientConfig.URL, clientConfig.URL)
	// Test connection via session-stats call
	_, err = c.GetActiveTorrentCount(context.Background())
	if err != nil {
		return nil, fmt.Errorf("<trans|%s> Failed to connect to Transmission: %w", clientConfig.URL, err)
	}
	logger.Debug("<trans|%s> Connected to Transmission", clientConfig.URL)

	return c, nil
}

func (c *TransmissionClient) Config() config.ClientConfig {
	return c.clientConfig
}

func (c *TransmissionClient) doRPC(ctx context.Context, method string, args interface{}) (*rpcResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	reqBody := rpcRequest{
		Method:    method,
		Arguments: args,
	}
	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.rpcURL, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.clientConfig.Username != "" || c.clientConfig.Password != "" {
		req.SetBasicAuth(c.clientConfig.Username, c.clientConfig.Password)
	}
	if c.sessionID != "" {
		req.Header.Set("X-Transmission-Session-Id", c.sessionID)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Handle Transmission 409 Conflict (CSRF Session ID requirement)
	if resp.StatusCode == http.StatusConflict {
		newSessionID := resp.Header.Get("X-Transmission-Session-Id")
		if newSessionID != "" {
			c.sessionID = newSessionID
			// Retry request once with new session ID
			reqRetry, err := http.NewRequestWithContext(ctx, http.MethodPost, c.rpcURL, bytes.NewReader(data))
			if err != nil {
				return nil, err
			}
			reqRetry.Header.Set("Content-Type", "application/json")
			if c.clientConfig.Username != "" || c.clientConfig.Password != "" {
				reqRetry.SetBasicAuth(c.clientConfig.Username, c.clientConfig.Password)
			}
			reqRetry.Header.Set("X-Transmission-Session-Id", c.sessionID)

			respRetry, err := c.httpClient.Do(reqRetry)
			if err != nil {
				return nil, err
			}
			defer respRetry.Body.Close()
			resp = respRetry
		}
	}

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("Transmission authentication failed, check credentials")
	}
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Transmission HTTP error %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var res rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}

	if res.Result != "success" {
		return nil, fmt.Errorf("Transmission RPC error: %s", res.Result)
	}

	return &res, nil
}

func (c *TransmissionClient) GetActiveTorrentCount(ctx context.Context) (int, error) {
	logger.Debug("<trans|%s> Getting active torrent count", c.clientConfig.URL)

	res, err := c.doRPC(ctx, "session-stats", nil)
	if err != nil {
		return 0, err
	}

	if countVal, ok := res.Arguments["activeTorrentCount"]; ok {
		if countFloat, ok := countVal.(float64); ok {
			return int(countFloat), nil
		}
	}
	return 0, nil
}

func (c *TransmissionClient) SetUploadSpeed(ctx context.Context, speed float64) error {
	var args map[string]interface{}
	if math.IsInf(speed, 1) {
		logger.Debug("<trans|%s> Setting upload speed to unlimited", c.clientConfig.URL)
		args = map[string]interface{}{
			"speed-limit-up-enabled": false,
		}
	} else {
		logger.Debug("<trans|%s> Setting upload speed to %v%s", c.clientConfig.URL, speed, c.appConfig.Units)
		converted, err := units.Convert(speed, c.appConfig.Units, "KB")
		if err != nil {
			return err
		}
		speedLimitKB := int64(math.Max(1, math.Round(converted)))
		args = map[string]interface{}{
			"speed-limit-up-enabled": true,
			"speed-limit-up":         speedLimitKB,
		}
	}

	_, err := c.doRPC(ctx, "session-set", args)
	return err
}

func (c *TransmissionClient) SetDownloadSpeed(ctx context.Context, speed float64) error {
	var args map[string]interface{}
	if math.IsInf(speed, 1) {
		logger.Debug("<trans|%s> Setting download speed to unlimited", c.clientConfig.URL)
		args = map[string]interface{}{
			"speed-limit-down-enabled": false,
		}
	} else {
		logger.Debug("<trans|%s> Setting download speed to %v%s", c.clientConfig.URL, speed, c.appConfig.Units)
		converted, err := units.Convert(speed, c.appConfig.Units, "KB")
		if err != nil {
			return err
		}
		speedLimitKB := int64(math.Max(1, math.Round(converted)))
		args = map[string]interface{}{
			"speed-limit-down-enabled": true,
			"speed-limit-down":         speedLimitKB,
		}
	}

	_, err := c.doRPC(ctx, "session-set", args)
	return err
}
