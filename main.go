package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/khw315/speedrr/client"
	"github.com/khw315/speedrr/config"
	"github.com/khw315/speedrr/logger"
	"github.com/khw315/speedrr/module"
)

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

func calculateBaseUploadSpeed(msModule *module.MediaServerModule, cfg *config.SpeedrrConfig) float64 {
	if msModule == nil {
		return cfg.MaxUpload
	}
	target := msModule.GetTargetUploadSpeed()
	if target == nil {
		return cfg.MaxUpload
	}

	switch v := target.(type) {
	case int:
		return float64(v)
	case float64:
		return v
	case string:
		s := strings.TrimSpace(strings.ToLower(v))
		if s == "unlimited" {
			return math.Inf(1)
		}
		if strings.HasSuffix(s, "%") {
			pctStr := strings.TrimSuffix(s, "%")
			pct, err := strconv.ParseFloat(pctStr, 64)
			if err == nil {
				return cfg.MaxUpload * (pct / 100.0)
			}
		}
		num, err := strconv.ParseFloat(s, 64)
		if err == nil {
			return num
		}
	}
	return cfg.MaxUpload
}

func calculateStreamModeUpload(baseUpload float64, scheduleUploadReductions []float64, minUpload, maxUpload float64) float64 {
	for _, r := range scheduleUploadReductions {
		if math.IsInf(r, 1) {
			return math.Inf(1)
		}
	}

	if len(scheduleUploadReductions) == 0 {
		return baseUpload
	}

	referenceUpload := maxUpload
	if !math.IsInf(baseUpload, 1) {
		referenceUpload = baseUpload
	}

	totalScheduleRed := 0.0
	for _, r := range scheduleUploadReductions {
		totalScheduleRed += r
	}

	return math.Max(minUpload, referenceUpload-totalScheduleRed)
}

func aggregateReductions(reductions []float64, minVal, maxVal float64) float64 {
	sum := 0.0
	for _, r := range reductions {
		if math.IsInf(r, 1) {
			return math.Inf(1)
		}
		sum += r
	}
	return math.Max(minVal, maxVal-sum)
}

func calculateTargetSpeeds(msModule *module.MediaServerModule, schedModule *module.ScheduleModule, cfg *config.SpeedrrConfig) (float64, float64) {
	var msUp, msDown float64
	var schedUp, schedDown float64

	if msModule != nil {
		msUp, msDown = msModule.GetReductionValue()
	}
	if schedModule != nil {
		schedUp, schedDown = schedModule.GetReductionValue()
	}

	var newUpload float64
	if math.IsInf(msUp, -1) {
		baseUpload := calculateBaseUploadSpeed(msModule, cfg)
		var scheduleReductions []float64
		if schedModule != nil && !math.IsInf(schedUp, -1) {
			scheduleReductions = append(scheduleReductions, schedUp)
		}
		newUpload = calculateStreamModeUpload(baseUpload, scheduleReductions, cfg.MinUpload, cfg.MaxUpload)
	} else {
		newUpload = aggregateReductions([]float64{msUp, schedUp}, cfg.MinUpload, cfg.MaxUpload)
	}

	newDownload := aggregateReductions([]float64{msDown, schedDown}, cfg.MinDownload, cfg.MaxDownload)
	return newUpload, newDownload
}

func logCalculatedSpeeds(uploadSpeed, downloadSpeed float64, units string) {
	if math.IsInf(uploadSpeed, 1) {
		logger.Info("New calculated upload speed: unlimited")
	} else {
		logger.Info("New calculated upload speed: %v%s", uploadSpeed, units)
	}

	if math.IsInf(downloadSpeed, 1) {
		logger.Info("New calculated download speed: unlimited")
	} else {
		logger.Info("New calculated download speed: %v%s", downloadSpeed, units)
	}
}

func fetchActiveTorrentCounts(ctx context.Context, clients []client.TorrentClient) (map[client.TorrentClient]int, int) {
	type clientCount struct {
		c     client.TorrentClient
		count int
		err   error
	}

	ch := make(chan clientCount, len(clients))
	var wg sync.WaitGroup

	for _, c := range clients {
		wg.Add(1)
		go func(tc client.TorrentClient) {
			defer wg.Done()
			cnt, err := tc.GetActiveTorrentCount(ctx)
			ch <- clientCount{c: tc, count: cnt, err: err}
		}(c)
	}

	wg.Wait()
	close(ch)

	clientActiveDict := make(map[client.TorrentClient]int)
	sumActiveTorrents := 0

	for res := range ch {
		if res.err != nil {
			logger.Warning("<client|%s> Error getting active torrent count: %v", res.c.Config().URL, res.err)
		} else {
			clientActiveDict[res.c] = res.count
			sumActiveTorrents += res.count
		}
	}

	return clientActiveDict, sumActiveTorrents
}

func calculateEffectiveClientSpeed(speed float64, shares, totalShares, activeCount, totalActive int, isManualShare bool) float64 {
	if math.IsInf(speed, 1) {
		return math.Inf(1)
	}
	if isManualShare {
		if totalShares > 0 {
			return (float64(shares) / float64(totalShares)) * speed
		}
		return speed
	}
	if activeCount > 0 && totalActive > 0 {
		return (float64(activeCount) / float64(totalActive)) * speed
	}
	return speed
}

func applySpeedsToClients(ctx context.Context, clients []client.TorrentClient, cfg *config.SpeedrrConfig, newUpload, newDownload float64, sumUploadShares, sumDownloadShares int) {
	logger.Info("Getting active torrent counts")

	clientActiveDict, sumActiveTorrents := fetchActiveTorrentCounts(ctx, clients)

	for _, c := range clients {
		cConfig := c.Config()
		activeCount := clientActiveDict[c]

		effUpload := calculateEffectiveClientSpeed(newUpload, cConfig.UploadShares, sumUploadShares, activeCount, sumActiveTorrents, cfg.ManualSpeedAlgorithmShare)
		effDownload := calculateEffectiveClientSpeed(newDownload, cConfig.DownloadShares, sumDownloadShares, activeCount, sumActiveTorrents, cfg.ManualSpeedAlgorithmShare)

		updateClientSpeed(ctx, c, effUpload, effDownload, cfg.Units)
	}
}

func updateClientSpeed(ctx context.Context, c client.TorrentClient, effUpload, effDownload float64, units string) {
	cConfig := c.Config()

	if err := c.SetUploadSpeed(ctx, effUpload); err != nil {
		logger.Warning("An error occurred while updating upload speed for %s, skipping: %v", cConfig.URL, err)
	} else {
		if math.IsInf(effUpload, 1) {
			logger.Info("Set upload speed for %s to unlimited", cConfig.URL)
		} else {
			logger.Info("Set upload speed for %s to %v%s", cConfig.URL, effUpload, units)
		}
	}

	if err := c.SetDownloadSpeed(ctx, effDownload); err != nil {
		logger.Warning("An error occurred while updating download speed for %s, skipping: %v", cConfig.URL, err)
	} else {
		if math.IsInf(effDownload, 1) {
			logger.Info("Set download speed for %s to unlimited", cConfig.URL)
		} else {
			logger.Info("Set download speed for %s to %v%s", cConfig.URL, effDownload, units)
		}
	}
}

func main() {
	var configPath string
	var logLevelRaw string
	var logFileLevelRaw string

	defaultConfig := getEnv("SPEEDRR_CONFIG", "")
	defaultLogLevel := getEnv("SPEEDRR_LOG_LEVEL", "20")
	defaultLogFileLevel := getEnv("SPEEDRR_LOG_FILE_LEVEL", "30")

	flag.StringVar(&configPath, "config_path", defaultConfig, "Path to the config file")
	flag.StringVar(&logLevelRaw, "log_level", defaultLogLevel, "Logging level to stdout (10=DEBUG, 20=INFO, 30=WARN, 40=ERROR)")
	flag.StringVar(&logFileLevelRaw, "log_file_level", defaultLogFileLevel, "Logging level to file")
	flag.Parse()

	if configPath == "" {
		fmt.Println("CRITICAL: No config file specified, use --config_path arg or SPEEDRR_CONFIG env var")
		os.Exit(1)
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		fmt.Printf("CRITICAL: Failed to load config: %v\n", err)
		os.Exit(1)
	}

	logLvl := logger.ParseLevel(logLevelRaw)
	logger.SetStdoutLevel(logLvl)

	if cfg.LogsPath != "" {
		fileLvl := logger.ParseLevel(logFileLevelRaw)
		if err := logger.SetFileHandler(cfg.LogsPath, fileLvl); err != nil {
			logger.Warning("Failed to set log file handler for %s: %v", cfg.LogsPath, err)
		}
	}

	logger.Info("Starting Speedrr")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle OS Signals (SIGINT, SIGTERM)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		logger.Info("Stopping Speedrr...")
		cancel()
	}()

	// Initialize Clients
	var clients []client.TorrentClient
	sumUploadShares := 0
	sumDownloadShares := 0

	for _, cConfig := range cfg.Clients {
		tc, err := client.NewClient(cfg, cConfig)
		if err != nil {
			logger.Critical("Failed to initialize client %s: %v", cConfig.URL, err)
			os.Exit(1)
		}
		clients = append(clients, tc)
		sumUploadShares += cConfig.UploadShares
		sumDownloadShares += cConfig.DownloadShares
	}

	// Update Event Signal Channel
	updateEventChan := make(chan struct{}, 1)
	triggerUpdate := func() {
		select {
		case updateEventChan <- struct{}{}:
		default:
		}
	}

	// Initialize Modules
	var msModule *module.MediaServerModule
	var schedModule *module.ScheduleModule

	if len(cfg.Modules.MediaServers) > 0 {
		var err error
		msModule, err = module.NewMediaServerModule(cfg, cfg.Modules.MediaServers, triggerUpdate)
		if err != nil {
			logger.Critical("Failed to initialize media servers module: %v", err)
			os.Exit(1)
		}
		msModule.Run(ctx)
		logger.Info("Started module: MediaServerModule")
	}

	if len(cfg.Modules.Schedule) > 0 {
		schedModule = module.NewScheduleModule(cfg, cfg.Modules.Schedule, triggerUpdate)
		schedModule.Run(ctx)
		logger.Info("Started module: ScheduleModule")
	}

	if msModule == nil && schedModule == nil {
		logger.Critical("No modules enabled in config, exiting")
		os.Exit(1)
	}

	// Initial trigger
	triggerUpdate()

	for {
		select {
		case <-ctx.Done():
			logger.Info("Speedrr shutdown complete")
			return
		case <-updateEventChan:
			logger.Info("Update event triggered")

			newUpload, newDownload := calculateTargetSpeeds(msModule, schedModule, cfg)
			logCalculatedSpeeds(newUpload, newDownload, cfg.Units)
			applySpeedsToClients(ctx, clients, cfg, newUpload, newDownload, sumUploadShares, sumDownloadShares)
			logger.Info("Speeds updated")

			logger.Info("Waiting for next update event")
		}
	}
}
