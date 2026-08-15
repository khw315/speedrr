package logger

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input    interface{}
		expected Level
	}{
		{10, LevelDebug},
		{int64(20), LevelInfo},
		{float64(30), LevelWarning},
		{"DEBUG", LevelDebug},
		{"10", LevelDebug},
		{"INFO", LevelInfo},
		{"20", LevelInfo},
		{"WARN", LevelWarning},
		{"WARNING", LevelWarning},
		{"30", LevelWarning},
		{"ERROR", LevelError},
		{"40", LevelError},
		{"CRITICAL", LevelCritical},
		{"50", LevelCritical},
		{"unknown", LevelInfo},
		{nil, LevelInfo},
	}

	for _, tt := range tests {
		got := ParseLevel(tt.input)
		if got != tt.expected {
			t.Errorf("ParseLevel(%v) = %v; want %v", tt.input, got, tt.expected)
		}
	}
}

func TestLoggerOutput(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	SetStdoutLevel(LevelDebug)
	if err := SetFileHandler(logFile, LevelDebug); err != nil {
		t.Fatalf("SetFileHandler failed: %v", err)
	}
	defer CloseFileHandler()

	Debug("debug message %d", 1)
	Info("info message %s", "test")
	Warning("warning message")
	Error("error message")
	Critical("critical message")

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	content := string(data)
	if !contains(content, "DEBUG") || !contains(content, "INFO") || !contains(content, "WARNING") || !contains(content, "ERROR") || !contains(content, "CRITICAL") {
		t.Errorf("Log file missing expected levels. Got:\n%s", content)
	}
}

func TestSetFileHandlerDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "sublogs")

	if err := SetFileHandler(subDir, LevelInfo); err != nil {
		t.Fatalf("SetFileHandler with dir failed: %v", err)
	}
	defer CloseFileHandler()
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
