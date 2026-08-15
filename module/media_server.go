package module

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/khw315/speedrr/config"
	"github.com/khw315/speedrr/logger"
	"github.com/khw315/speedrr/units"
)


type MediaServerModule struct {
	mu                   sync.Mutex
	appConfig            *config.SpeedrrConfig
	serverConfigs        []config.MediaServerConfig
	reductionValueDict   map[string]float64
	streamCountDict      map[string]int
	servers              []MediaServer
	notifyUpdateCallback func()
}

type MediaServer interface {
	ServerConfig() config.MediaServerConfig
	GetBandwidth(ctx context.Context) (int, error)
	Run(ctx context.Context)
}

type BaseServer struct {
	mu           sync.Mutex
	appConfig    *config.SpeedrrConfig
	serverConfig config.MediaServerConfig
	module       *MediaServerModule
	httpClient   *http.Client
	baseURL      string
	loggerPrefix string
	pausedSince  map[string]time.Time
}

func NewMediaServerModule(appConfig *config.SpeedrrConfig, serverConfigs []config.MediaServerConfig, notifyUpdate func()) (*MediaServerModule, error) {
	m := &MediaServerModule{
		appConfig:            appConfig,
		serverConfigs:        serverConfigs,
		reductionValueDict:   make(map[string]float64),
		streamCountDict:      make(map[string]int),
		notifyUpdateCallback: notifyUpdate,
	}

	for _, sCfg := range serverConfigs {
		var server MediaServer
		base := newBaseServer(appConfig, sCfg, m)

		switch sCfg.Type {
		case "plex":
			server = &PlexServer{BaseServer: base}
		case "tautulli":
			server = &TautulliServer{BaseServer: base}
		case "jellyfin":
			server = &JellyfinServer{BaseServer: base}
		case "emby":
			server = &EmbyServer{BaseServer: base}
		default:
			return nil, fmt.Errorf("<media_servers> Unknown media server type: %s", sCfg.Type)
		}

		m.servers = append(m.servers, server)
		m.initialBandwidthCheck(server, 5, 2*time.Second)
	}

	return m, nil
}

func newBaseServer(appConfig *config.SpeedrrConfig, serverConfig config.MediaServerConfig, module *MediaServerModule) BaseServer {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: !serverConfig.HTTPSVerify,
		},
	}
	httpClient := &http.Client{
		Transport: transport,
		Timeout:   15 * time.Second,
	}

	baseURL := strings.TrimRight(serverConfig.URL, "/")

	return BaseServer{
		appConfig:    appConfig,
		serverConfig: serverConfig,
		module:       module,
		httpClient:   httpClient,
		baseURL:      baseURL,
		loggerPrefix: fmt.Sprintf("<%s|%s>", serverConfig.Type, serverConfig.URL),
		pausedSince:  make(map[string]time.Time),
	}
}

func (m *MediaServerModule) initialBandwidthCheck(server MediaServer, maxRetries int, baseDelay time.Duration) {
	sCfg := server.ServerConfig()
	for attempt := 1; attempt <= maxRetries; attempt++ {
		_, err := server.GetBandwidth(context.Background())
		if err == nil {
			return
		}

		if attempt < maxRetries {
			delay := baseDelay * time.Duration(1<<(attempt-1))
			logger.Warning("<media_servers> Initial bandwidth check for %s failed (attempt %d/%d), retrying in %v: %v",
				sCfg.URL, attempt, maxRetries, delay, err)
			time.Sleep(delay)
		} else {
			logger.Error("<media_servers> Initial bandwidth check for %s failed after %d attempts. Starting with 0 bandwidth: %v",
				sCfg.URL, maxRetries, err)
			m.SetReduction(sCfg.URL, 0)
			m.SetStreamCount(sCfg.URL, 0)
		}
	}
}

func (m *MediaServerModule) SetReduction(url string, reduction float64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	old, exists := m.reductionValueDict[url]
	if exists && old == reduction {
		return
	}
	m.reductionValueDict[url] = reduction
	m.notifyUpdateCallback()
}

func (m *MediaServerModule) SetStreamCount(url string, count int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	old, exists := m.streamCountDict[url]
	if exists && old == count {
		return
	}
	m.streamCountDict[url] = count
	m.notifyUpdateCallback()
}

func (m *MediaServerModule) GetReductionValue() (float64, float64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	hasStreamBased := false
	for _, sCfg := range m.serverConfigs {
		if sCfg.StreamBasedSpeeds != nil && sCfg.StreamBasedSpeeds.Enabled {
			hasStreamBased = true
			break
		}
	}

	if hasStreamBased {
		totalStreams := 0
		for _, count := range m.streamCountDict {
			totalStreams += count
		}
		logger.Info("<media_servers> Total active streams: %d", totalStreams)
		return math.Inf(-1), 0
	}

	totalUploadReduction := 0.0
	for _, red := range m.reductionValueDict {
		totalUploadReduction += red
	}

	return totalUploadReduction, 0
}

func (m *MediaServerModule) GetStreamCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	total := 0
	for _, count := range m.streamCountDict {
		total += count
	}
	return total
}

func (m *MediaServerModule) GetTargetUploadSpeed() interface{} {
	m.mu.Lock()
	defer m.mu.Unlock()

	totalStreams := 0
	for _, count := range m.streamCountDict {
		totalStreams += count
	}

	for _, sCfg := range m.serverConfigs {
		if sCfg.StreamBasedSpeeds != nil && sCfg.StreamBasedSpeeds.Enabled {
			speedsConfig := sCfg.StreamBasedSpeeds
			if val, ok := speedsConfig.Speeds[totalStreams]; ok {
				return val
			}

			applicable := -1
			for countKey := range speedsConfig.Speeds {
				if countKey <= totalStreams && countKey > applicable {
					applicable = countKey
				}
			}
			if applicable != -1 {
				return speedsConfig.Speeds[applicable]
			}

			if speedsConfig.Default != nil {
				return speedsConfig.Default
			}

			return m.appConfig.MaxUpload
		}
	}

	return m.appConfig.MaxUpload
}

func (m *MediaServerModule) Run(ctx context.Context) {
	for _, server := range m.servers {
		go server.Run(ctx)
	}
}

const logGettingBandwidth = "%s Getting bandwidth"

// Stream IP & Paused Helpers

func isPrivateIP(ipStr string) bool {
	if strings.ToLower(ipStr) == "lan" {
		return true
	}
	host, _, err := net.SplitHostPort(ipStr)
	if err != nil {
		host = ipStr
	}
	parsedIP := net.ParseIP(host)
	return parsedIP != nil && (parsedIP.IsPrivate() || parsedIP.IsLoopback() || parsedIP.IsLinkLocalUnicast())
}

func matchesIPNetwork(ipStr string, cidrNetworks []string) bool {
	host, _, err := net.SplitHostPort(ipStr)
	if err != nil {
		host = ipStr
	}
	parsedIP := net.ParseIP(host)
	if parsedIP == nil {
		return false
	}
	for _, cidr := range cidrNetworks {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err == nil && ipNet.Contains(parsedIP) {
			return true
		}
	}
	return false
}

func (b *BaseServer) isLocalStream(ipStr string) bool {
	if ipStr == "" {
		return false
	}
	if b.serverConfig.IgnoreStreams.Local && isPrivateIP(ipStr) {
		return true
	}
	if len(b.serverConfig.IgnoreStreams.IPNetworks) > 0 && matchesIPNetwork(ipStr, b.serverConfig.IgnoreStreams.IPNetworks) {
		return true
	}
	return false
}

func (b *BaseServer) isSessionPausedIgnored(paused bool, sessionID string, title string) bool {
	pausedAfter := b.serverConfig.IgnoreStreams.PausedAfter
	if pausedAfter == -1 {
		return false
	}

	if paused {
		t, noted := b.pausedSince[sessionID]
		if !noted {
			b.pausedSince[sessionID] = time.Now()
			logger.Debug("%s %s:%s is paused, noted time", b.loggerPrefix, title, sessionID)
			return false
		}
		if int(time.Since(t).Seconds()) > pausedAfter {
			logger.Debug("%s Removing %s:%s from count, paused for too long", b.loggerPrefix, title, sessionID)
			return true
		}
		return false
	}

	if _, noted := b.pausedSince[sessionID]; noted {
		logger.Debug("%s %s:%s is no longer paused, removing from paused dict", b.loggerPrefix, title, sessionID)
		delete(b.pausedSince, sessionID)
	}
	return false
}

func (b *BaseServer) processSession(bandwidth int, paused bool, ipAddress string, sessionID string, title string) int {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.isSessionPausedIgnored(paused, sessionID, title) {
		return 0
	}

	if b.isLocalStream(ipAddress) {
		logger.Debug("%s Ignoring local stream %s:%s (%s)", b.loggerPrefix, title, sessionID, ipAddress)
		return 0
	}

	logger.Debug("%s Adding %d to count for %s:%s", b.loggerPrefix, bandwidth, title, sessionID)
	return bandwidth
}

func (b *BaseServer) removeOldPaused(activeSessionIDs map[string]bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for sid := range b.pausedSince {
		if !activeSessionIDs[sid] {
			logger.Debug("%s Removing %s from pausedSince, no longer in session list", b.loggerPrefix, sid)
			delete(b.pausedSince, sid)
		}
	}
}

func (b *BaseServer) setReductionKbit(bandwidthKbit int) {
	converted, err := units.Convert(float64(bandwidthKbit), "Kbit", b.appConfig.Units)
	if err != nil {
		logger.Error("%s Unit conversion error: %v", b.loggerPrefix, err)
		return
	}
	b.module.SetReduction(b.serverConfig.URL, converted)
}

func (b *BaseServer) runLoop(ctx context.Context, getBandwidthFn func(ctx context.Context) (int, error)) {
	interval := time.Duration(b.serverConfig.UpdateInterval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			bw, err := getBandwidthFn(ctx)
			if err != nil {
				logger.Error("%s Error getting bandwidth: %v", b.loggerPrefix, err)
			} else {
				bwWithMult := int(float64(bw) * b.serverConfig.BandwidthMultiplier)
				b.setReductionKbit(bwWithMult)
			}
		}
	}
}

// Plex Server Implementation

type PlexServer struct {
	BaseServer
}

func (p *PlexServer) ServerConfig() config.MediaServerConfig {
	return p.serverConfig
}

func (p *PlexServer) GetBandwidth(ctx context.Context) (int, error) {
	logger.Debug(logGettingBandwidth, p.loggerPrefix)

	reqURL := fmt.Sprintf("%s/status/sessions", p.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return 0, err
	}

	q := req.URL.Query()
	q.Set("X-Plex-Token", p.serverConfig.Token)
	q.Set("X-Plex-Language", "en")
	req.URL.RawQuery = q.Encode()
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("Plex HTTP status %d", resp.StatusCode)
	}

	var data struct {
		MediaContainer struct {
			Size     int `json:"size"`
			Metadata []struct {
				Title   string `json:"title"`
				Session struct {
					ID        string      `json:"id"`
					Bandwidth interface{} `json:"bandwidth"`
				} `json:"Session"`
				Player struct {
					State   string `json:"state"`
					Address string `json:"address"`
				} `json:"Player"`
			} `json:"Metadata"`
		} `json:"MediaContainer"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return 0, err
	}

	if data.MediaContainer.Size == 0 {
		logger.Debug("%s No sessions found", p.loggerPrefix)
		p.module.SetStreamCount(p.serverConfig.URL, 0)
		return 0, nil
	}

	totalBandwidth := 0
	streamCount := 0
	activeSessionIDs := make(map[string]bool)

	for _, item := range data.MediaContainer.Metadata {
		sid := item.Session.ID
		if sid == "" {
			continue
		}
		activeSessionIDs[sid] = true

		bwInt := parseFlexibleInt(item.Session.Bandwidth)
		processed := p.processSession(bwInt, item.Player.State == "paused", item.Player.Address, sid, item.Title)
		totalBandwidth += processed
		if processed > 0 {
			streamCount++
		}
	}

	p.removeOldPaused(activeSessionIDs)
	p.module.SetStreamCount(p.serverConfig.URL, streamCount)

	return totalBandwidth, nil
}

func (p *PlexServer) Run(ctx context.Context) {
	p.runLoop(ctx, p.GetBandwidth)
}

// Tautulli Server Implementation

type TautulliServer struct {
	BaseServer
}

func (t *TautulliServer) ServerConfig() config.MediaServerConfig {
	return t.serverConfig
}

func (t *TautulliServer) GetBandwidth(ctx context.Context) (int, error) {
	logger.Debug(logGettingBandwidth, t.loggerPrefix)

	reqURL := fmt.Sprintf("%s/api/v2", t.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return 0, err
	}

	q := req.URL.Query()
	q.Set("apikey", t.serverConfig.APIKey)
	q.Set("cmd", "get_activity")
	req.URL.RawQuery = q.Encode()

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("Tautulli HTTP status %d", resp.StatusCode)
	}

	var data struct {
		Response struct {
			Result  string `json:"result"`
			Message string `json:"message"`
			Data    struct {
				Sessions []struct {
					SessionID string      `json:"session_id"`
					Bandwidth interface{} `json:"bandwidth"`
					State     string      `json:"state"`
					IPAddress string      `json:"ip_address"`
					FullTitle string      `json:"full_title"`
				} `json:"sessions"`
			} `json:"data"`
		} `json:"response"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return 0, err
	}

	if data.Response.Result != "success" {
		return 0, fmt.Errorf("Tautulli error: %s", data.Response.Message)
	}

	totalBandwidth := 0
	streamCount := 0
	activeSessionIDs := make(map[string]bool)

	for _, session := range data.Response.Data.Sessions {
		sid := session.SessionID
		if sid == "" {
			continue
		}
		activeSessionIDs[sid] = true

		bwInt := parseFlexibleInt(session.Bandwidth)
		processed := t.processSession(bwInt, session.State == "paused", session.IPAddress, sid, session.FullTitle)
		totalBandwidth += processed
		if processed > 0 {
			streamCount++
		}
	}

	t.removeOldPaused(activeSessionIDs)
	t.module.SetStreamCount(t.serverConfig.URL, streamCount)

	return totalBandwidth, nil
}

func (t *TautulliServer) Run(ctx context.Context) {
	t.runLoop(ctx, t.GetBandwidth)
}

// Jellyfin Server Implementation

type JellyfinServer struct {
	BaseServer
}

func (j *JellyfinServer) ServerConfig() config.MediaServerConfig {
	return j.serverConfig
}

func (j *JellyfinServer) GetBandwidth(ctx context.Context) (int, error) {
	logger.Debug(logGettingBandwidth, j.loggerPrefix)

	reqURL := fmt.Sprintf("%s/Sessions", j.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", fmt.Sprintf(`MediaBrowser Token="%s"`, j.serverConfig.APIKey))

	resp, err := j.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("Jellyfin HTTP status %d", resp.StatusCode)
	}

	var sessions []struct {
		ID             string `json:"Id"`
		RemoteEndPoint string `json:"RemoteEndPoint"`
		NowPlayingItem *struct {
			Name         string `json:"Name"`
			MediaStreams []struct {
				BitRate interface{} `json:"BitRate"`
			} `json:"MediaStreams"`
		} `json:"NowPlayingItem"`
		PlayState struct {
			IsPaused   bool   `json:"IsPaused"`
			PlayMethod string `json:"PlayMethod"`
		} `json:"PlayState"`
		TranscodingInfo *struct {
			Bitrate interface{} `json:"Bitrate"`
		} `json:"TranscodingInfo"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		return 0, err
	}

	totalBits := 0
	streamCount := 0
	activeSessionIDs := make(map[string]bool)

	for _, session := range sessions {
		if session.NowPlayingItem == nil {
			continue
		}
		sid := session.ID
		activeSessionIDs[sid] = true

		bandwidth := 0
		if session.PlayState.PlayMethod == "DirectPlay" || session.PlayState.PlayMethod == "DirectStream" {
			for _, stream := range session.NowPlayingItem.MediaStreams {
				bandwidth += parseFlexibleInt(stream.BitRate)
			}
		} else if session.TranscodingInfo != nil {
			bandwidth = parseFlexibleInt(session.TranscodingInfo.Bitrate)
		}

		processed := j.processSession(bandwidth, session.PlayState.IsPaused, session.RemoteEndPoint, sid, session.NowPlayingItem.Name)
		totalBits += processed
		if processed > 0 {
			streamCount++
		}
	}

	j.removeOldPaused(activeSessionIDs)
	j.module.SetStreamCount(j.serverConfig.URL, streamCount)

	kbit, _ := units.Convert(float64(totalBits), "bit", "Kbit")
	return int(math.Round(kbit)), nil
}

func (j *JellyfinServer) Run(ctx context.Context) {
	j.runLoop(ctx, j.GetBandwidth)
}

// Emby Server Implementation

type EmbyServer struct {
	BaseServer
}

func (e *EmbyServer) ServerConfig() config.MediaServerConfig {
	return e.serverConfig
}

func (e *EmbyServer) GetBandwidth(ctx context.Context) (int, error) {
	logger.Debug(logGettingBandwidth, e.loggerPrefix)

	reqURL := fmt.Sprintf("%s/Sessions?api_key=%s", e.baseURL, e.serverConfig.APIKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return 0, err
	}

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("Emby HTTP status %d", resp.StatusCode)
	}

	var sessions []struct {
		ID             string `json:"Id"`
		RemoteEndPoint string `json:"RemoteEndPoint"`
		NowPlayingItem *struct {
			Name         string `json:"Name"`
			MediaStreams []struct {
				BitRate interface{} `json:"BitRate"`
			} `json:"MediaStreams"`
		} `json:"NowPlayingItem"`
		PlayState struct {
			IsPaused   bool   `json:"IsPaused"`
			PlayMethod string `json:"PlayMethod"`
		} `json:"PlayState"`
		TranscodingInfo *struct {
			Bitrate interface{} `json:"Bitrate"`
		} `json:"TranscodingInfo"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		return 0, err
	}

	totalBits := 0
	streamCount := 0
	activeSessionIDs := make(map[string]bool)

	for _, session := range sessions {
		if session.NowPlayingItem == nil {
			continue
		}
		sid := session.ID
		activeSessionIDs[sid] = true

		bandwidth := 0
		if session.PlayState.PlayMethod == "Transcode" && session.TranscodingInfo != nil {
			bandwidth = parseFlexibleInt(session.TranscodingInfo.Bitrate)
		} else {
			for _, stream := range session.NowPlayingItem.MediaStreams {
				bandwidth += parseFlexibleInt(stream.BitRate)
			}
		}

		processed := e.processSession(bandwidth, session.PlayState.IsPaused, session.RemoteEndPoint, sid, session.NowPlayingItem.Name)
		totalBits += processed
		if processed > 0 {
			streamCount++
		}
	}

	e.removeOldPaused(activeSessionIDs)
	e.module.SetStreamCount(e.serverConfig.URL, streamCount)

	kbit, _ := units.Convert(float64(totalBits), "bit", "Kbit")
	return int(math.Round(kbit)), nil
}

func (e *EmbyServer) Run(ctx context.Context) {
	e.runLoop(ctx, e.GetBandwidth)
}

func parseFlexibleInt(val interface{}) int {
	if val == nil {
		return 0
	}
	switch v := val.(type) {
	case int:
		return v
	case float64:
		return int(v)
	case string:
		i, _ := strconv.Atoi(v)
		return i
	}
	return 0
}
