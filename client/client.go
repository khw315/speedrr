package client

import (
	"context"
	"fmt"
	"math"

	"github.com/khw315/speedrr/config"
)

var UnlimitedSpeed = math.Inf(1)

type TorrentClient interface {
	Config() config.ClientConfig
	GetActiveTorrentCount(ctx context.Context) (int, error)
	SetUploadSpeed(ctx context.Context, speed float64) error
	SetDownloadSpeed(ctx context.Context, speed float64) error
}

func NewClient(appCfg *config.SpeedrrConfig, clientCfg config.ClientConfig) (TorrentClient, error) {
	switch clientCfg.Type {
	case "qbittorrent":
		return NewQBittorrentClient(appCfg, clientCfg)
	case "transmission":
		return NewTransmissionClient(appCfg, clientCfg)
	default:
		return nil, fmt.Errorf("unknown client type: %s", clientCfg.Type)
	}
}
