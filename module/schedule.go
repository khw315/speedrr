package module

import (
	"context"
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

func getDayIdx(t time.Time) int {
	goWd := t.Weekday()
	if goWd == time.Sunday {
		return 6
	}
	return int(goWd) - 1
}

func isDayInList(dayIdx int, days []int) bool {
	for _, d := range days {
		if d == dayIdx {
			return true
		}
	}
	return false
}

func (w *scheduleWorker) evaluateScheduleState(now time.Time) (bool, time.Time) {
	loc := now.Location()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)

	isOvernight := (w.startHour > w.endHour) || (w.startHour == w.endHour && w.startMinute >= w.endMinute)

	// Check window starting today
	if isDayInList(getDayIdx(today), w.daysAsInt) {
		startToday := time.Date(today.Year(), today.Month(), today.Day(), w.startHour, w.startMinute, 0, 0, loc)
		var endToday time.Time
		if isOvernight {
			endToday = time.Date(today.Year(), today.Month(), today.Day()+1, w.endHour, w.endMinute, 0, 0, loc)
		} else {
			endToday = time.Date(today.Year(), today.Month(), today.Day(), w.endHour, w.endMinute, 0, 0, loc)
		}

		if (now.Equal(startToday) || now.After(startToday)) && now.Before(endToday) {
			return true, endToday
		}
	}

	// Check window starting yesterday if overnight
	if isOvernight {
		yesterday := today.AddDate(0, 0, -1)
		if isDayInList(getDayIdx(yesterday), w.daysAsInt) {
			startYest := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), w.startHour, w.startMinute, 0, 0, loc)
			endYest := time.Date(today.Year(), today.Month(), today.Day(), w.endHour, w.endMinute, 0, 0, loc)

			if (now.Equal(startYest) || now.After(startYest)) && now.Before(endYest) {
				return true, endYest
			}
		}
	}

	// Currently inactive. Find next start time
	for dayOffset := 0; dayOffset <= 8; dayOffset++ {
		candidateDate := today.AddDate(0, 0, dayOffset)
		if isDayInList(getDayIdx(candidateDate), w.daysAsInt) {
			candidateStart := time.Date(candidateDate.Year(), candidateDate.Month(), candidateDate.Day(), w.startHour, w.startMinute, 0, 0, loc)
			if candidateStart.After(now) {
				return false, candidateStart
			}
		}
	}

	return false, now.Add(1 * time.Hour)
}

func (w *scheduleWorker) run(ctx context.Context) {
	for {
		now := time.Now()
		isActive, nextTransition := w.evaluateScheduleState(now)

		if isActive {
			w.module.SetReduction(w.index, w.uploadReduceBy, w.downloadReduceBy)
			sleepDuration := time.Until(nextTransition)
			logger.Debug("<ScheduleThread> Schedule active until %v, Sleeping for %v", nextTransition, sleepDuration)

			select {
			case <-ctx.Done():
				return
			case <-time.After(sleepDuration):
			}
		} else {
			w.module.RemoveReduction(w.index)
			sleepDuration := time.Until(nextTransition)
			logger.Debug("<ScheduleThread> Schedule inactive until next start at %v, Sleeping for %v", nextTransition, sleepDuration)

			select {
			case <-ctx.Done():
				return
			case <-time.After(sleepDuration):
			}
		}
	}
}

