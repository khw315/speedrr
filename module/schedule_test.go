package module

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/khw315/speedrr/config"
)

func TestParseTimeHM(t *testing.T) {
	h, m := parseTimeHM("14:30")
	if h != 14 || m != 30 {
		t.Errorf("Expected 14:30, got %d:%d", h, m)
	}

	h, m = parseTimeHM("invalid")
	if h != 0 || m != 0 {
		t.Errorf("Expected 0:0 for invalid input, got %d:%d", h, m)
	}
}

func TestParseSpeedVal(t *testing.T) {
	tests := []struct {
		val      interface{}
		maxVal   float64
		expected float64
	}{
		{100, 1000, 100},
		{50.5, 1000, 50.5},
		{"unlimited", 1000, math.Inf(1)},
		{"50%", 1000, 500},
		{"250", 1000, 250},
		{nil, 1000, 0},
		{"invalid", 1000, 0},
	}

	for _, tt := range tests {
		got := parseSpeedVal(tt.val, tt.maxVal)
		if math.IsInf(tt.expected, 1) {
			if !math.IsInf(got, 1) {
				t.Errorf("parseSpeedVal(%v) = %v; want Inf", tt.val, got)
			}
		} else if math.Abs(got-tt.expected) > 0.001 {
			t.Errorf("parseSpeedVal(%v) = %v; want %v", tt.val, got, tt.expected)
		}
	}
}

func TestGetDayIdx(t *testing.T) {
	// Sunday -> 6
	sun := time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
	if getDayIdx(sun) != 6 {
		t.Errorf("Expected Sunday to be 6, got %d", getDayIdx(sun))
	}

	// Monday -> 0
	mon := time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)
	if getDayIdx(mon) != 0 {
		t.Errorf("Expected Monday to be 0, got %d", getDayIdx(mon))
	}
}

func TestScheduleModuleReductions(t *testing.T) {
	updated := false
	notify := func() {
		updated = true
	}

	appCfg := &config.SpeedrrConfig{}
	schedCfgs := []config.ScheduleConfig{}

	mod := NewScheduleModule(appCfg, schedCfgs, notify)

	mod.SetReduction(0, 10.0, 20.0)
	if !updated {
		t.Errorf("Expected notify callback on SetReduction")
	}

	up, down := mod.GetReductionValue()
	if up != 10.0 || down != 20.0 {
		t.Errorf("Expected reduction (10, 20), got (%v, %v)", up, down)
	}

	mod.RemoveReduction(0)
	up, down = mod.GetReductionValue()
	if up != 0.0 || down != 0.0 {
		t.Errorf("Expected reduction (0, 0) after remove, got (%v, %v)", up, down)
	}
}

func TestScheduleWorkerWindow(t *testing.T) {
	appCfg := &config.SpeedrrConfig{MaxUpload: 100, MaxDownload: 100}
	cfg := config.ScheduleConfig{
		Start:    "09:00",
		End:      "17:00",
		Days:     []string{"mon", "tue", "wed", "thu", "fri"},
		Upload:   "50%",
		Download: "50%",
	}

	mod := NewScheduleModule(appCfg, []config.ScheduleConfig{cfg}, func() {})
	worker := newScheduleWorker(appCfg, cfg, 0, mod)

	loc := time.UTC
	nowInWindow := time.Date(2026, 8, 17, 12, 0, 0, 0, loc) // Mon 12:00
	today := time.Date(2026, 8, 17, 0, 0, 0, 0, loc)

	active, _ := worker.checkWindowStartingOn(today, nowInWindow, false)
	if !active {
		t.Errorf("Expected window to be active at 12:00 on Monday")
	}

	nextStart := worker.findNextStartTime(nowInWindow, today)
	if nextStart.IsZero() {
		t.Errorf("Expected valid next start time")
	}
}

func TestOvernightScheduleWorker(t *testing.T) {
	appCfg := &config.SpeedrrConfig{MaxUpload: 100, MaxDownload: 100}
	cfg := config.ScheduleConfig{
		Start:    "22:00",
		End:      "06:00",
		Days:     []string{"all"},
		Upload:   "100",
		Download: "100",
	}

	mod := NewScheduleModule(appCfg, []config.ScheduleConfig{cfg}, func() {})
	worker := newScheduleWorker(appCfg, cfg, 0, mod)

	loc := time.UTC
	// Test late night: 23:00
	nowLateNight := time.Date(2026, 8, 17, 23, 0, 0, 0, loc)
	active, _ := worker.evaluateScheduleState(nowLateNight)
	if !active {
		t.Errorf("Expected overnight schedule active at 23:00")
	}

	// Test early morning next day: 03:00
	nowEarlyMorning := time.Date(2026, 8, 18, 3, 0, 0, 0, loc)
	activeEarly, _ := worker.evaluateScheduleState(nowEarlyMorning)
	if !activeEarly {
		t.Errorf("Expected overnight schedule active at 03:00")
	}
}

func TestScheduleModuleRun(t *testing.T) {
	appCfg := &config.SpeedrrConfig{MaxUpload: 100, MaxDownload: 100}
	cfg := config.ScheduleConfig{
		Start:    "00:00",
		End:      "23:59",
		Days:     []string{"all"},
		Upload:   "10",
		Download: "10",
	}

	mod := NewScheduleModule(appCfg, []config.ScheduleConfig{cfg}, func() {})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	mod.Run(ctx)
	time.Sleep(150 * time.Millisecond)
}
