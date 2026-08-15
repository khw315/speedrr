package module

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/khw315/speedrr/config"
	"github.com/khw315/speedrr/logger"
)

type ScheduleModule struct {
	mu                   sync.Mutex
	appConfig            *config.SpeedrrConfig
	scheduleConfigs      []config.ScheduleConfig
	reductionValueDict   map[int][2]float64
	notifyUpdateCallback func()
}

func NewScheduleModule(appConfig *config.SpeedrrConfig, scheduleConfigs []config.ScheduleConfig, notifyUpdate func()) *ScheduleModule {
	m := &ScheduleModule{
		appConfig:            appConfig,
		scheduleConfigs:      scheduleConfigs,
		reductionValueDict:   make(map[int][2]float64),
		notifyUpdateCallback: notifyUpdate,
	}

	return m
}

func (m *ScheduleModule) SetReduction(index int, uploadRed, downloadRed float64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	old, exists := m.reductionValueDict[index]
	if exists && old[0] == uploadRed && old[1] == downloadRed {
		return
	}
	m.reductionValueDict[index] = [2]float64{uploadRed, downloadRed}
	m.notifyUpdateCallback()
}

func (m *ScheduleModule) RemoveReduction(index int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.reductionValueDict[index]; !exists {
		return
	}
	delete(m.reductionValueDict, index)
	m.notifyUpdateCallback()
}

func (m *ScheduleModule) GetReductionValue() (float64, float64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	totalUpload := 0.0
	totalDownload := 0.0

	for _, red := range m.reductionValueDict {
		if math.IsInf(red[0], 1) {
			totalUpload = math.Inf(1)
		} else if !math.IsInf(totalUpload, 1) {
			totalUpload += red[0]
		}

		if math.IsInf(red[1], 1) {
			totalDownload = math.Inf(1)
		} else if !math.IsInf(totalDownload, 1) {
			totalDownload += red[1]
		}
	}

	return totalUpload, totalDownload
}

func (m *ScheduleModule) Run(ctx context.Context) {
	logger.Debug("<schedule> Starting schedule module threads")
	for i, cfg := range m.scheduleConfigs {
		worker := newScheduleWorker(m.appConfig, cfg, i, m)
		go worker.run(ctx)
	}
}

type scheduleWorker struct {
	appConfig        *config.SpeedrrConfig
	cfg              config.ScheduleConfig
	index            int
	module           *ScheduleModule
	startHour        int
	startMinute      int
	endHour          int
	endMinute        int
	daysAsInt        []int
	uploadReduceBy   float64
	downloadReduceBy float64
}

func newScheduleWorker(appConfig *config.SpeedrrConfig, cfg config.ScheduleConfig, index int, module *ScheduleModule) *scheduleWorker {
	sHour, sMin := parseTimeHM(cfg.Start)
	eHour, eMin := parseTimeHM(cfg.End)

	dayList := []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}
	var days []int

	for _, d := range cfg.Days {
		lowerD := strings.ToLower(strings.TrimSpace(d))
		if lowerD == "all" {
			days = []int{0, 1, 2, 3, 4, 5, 6}
			break
		}
		for idx, name := range dayList {
			if name == lowerD {
				days = append(days, idx)
				break
			}
		}
	}

	uploadRed := parseSpeedVal(cfg.Upload, appConfig.MaxUpload)
	downloadRed := parseSpeedVal(cfg.Download, appConfig.MaxDownload)

	return &scheduleWorker{
		appConfig:        appConfig,
		cfg:              cfg,
		index:            index,
		module:           module,
		startHour:        sHour,
		startMinute:      sMin,
		endHour:          eHour,
		endMinute:        eMin,
		daysAsInt:        days,
		uploadReduceBy:   uploadRed,
		downloadReduceBy: downloadRed,
	}
}

func parseTimeHM(hmStr string) (int, int) {
	parts := strings.Split(hmStr, ":")
	if len(parts) != 2 {
		return 0, 0
	}
	h, _ := strconv.Atoi(parts[0])
	m, _ := strconv.Atoi(parts[1])
	return h, m
}

func parseSpeedVal(val interface{}, maxVal float64) float64 {
	if val == nil {
		return 0
	}
	switch v := val.(type) {
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
				return maxVal * (pct / 100.0)
			}
		}
		num, err := strconv.ParseFloat(s, 64)
		if err == nil {
			return num
		}
	}
	return 0
}

func (w *scheduleWorker) calculateNextOccurrence(hour, minute int) time.Time {
	now := time.Now()
	loc := now.Location()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)

	for dayOffset := 0; dayOffset <= 7; dayOffset++ {
		candidateDate := today.AddDate(0, 0, dayOffset)
		// Go Weekday: Sunday=0, Monday=1, ..., Saturday=6 -> map to Mon=0 ... Sun=6
		goWeekday := candidateDate.Weekday()
		var idx int
		if goWeekday == time.Sunday {
			idx = 6
		} else {
			idx = int(goWeekday) - 1
		}

		isDayValid := false
		for _, d := range w.daysAsInt {
			if d == idx {
				isDayValid = true
				break
			}
		}

		if isDayValid {
			candidateTime := time.Date(candidateDate.Year(), candidateDate.Month(), candidateDate.Day(), hour, minute, 0, 0, loc)
			if candidateTime.After(now) {
				return candidateTime
			}
		}
	}

	return now.Add(24 * time.Hour)
}

func (w *scheduleWorker) run(ctx context.Context) {
	for {
		nextStart := w.calculateNextOccurrence(w.startHour, w.startMinute)
		nextEnd := w.calculateNextOccurrence(w.endHour, w.endMinute)

		logger.Debug("<ScheduleThread> Next start occurrence: %v, Next end occurrence: %v", nextStart, nextEnd)

		if nextStart.After(nextEnd) {
			// Currently inside scheduled window
			w.module.SetReduction(w.index, w.uploadReduceBy, w.downloadReduceBy)
			sleepDuration := time.Until(nextEnd)
			logger.Debug("<ScheduleThread> start>end, Sleeping for %v", sleepDuration)

			select {
			case <-ctx.Done():
				return
			case <-time.After(sleepDuration):
			}
		} else if nextStart.Before(nextEnd) {
			// Currently outside scheduled window
			w.module.RemoveReduction(w.index)
			sleepDuration := time.Until(nextStart)
			logger.Debug("<ScheduleThread> start<end, Sleeping for %v", sleepDuration)

			select {
			case <-ctx.Done():
				return
			case <-time.After(sleepDuration):
			}
		} else {
			logger.Warning("<ScheduleThread> start time and end time are equal, sleeping 1 minute")
			select {
			case <-ctx.Done():
				return
			case <-time.After(1 * time.Minute):
			}
		}
	}
}
