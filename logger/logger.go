package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

type Level int

const (
	LevelDebug    Level = 10
	LevelInfo     Level = 20
	LevelWarning  Level = 30
	LevelError    Level = 40
	LevelCritical Level = 50
)

func ParseLevel(lvl interface{}) Level {
	switch v := lvl.(type) {
	case int:
		return Level(v)
	case int64:
		return Level(v)
	case float64:
		return Level(v)
	case string:
		switch strings.ToUpper(strings.TrimSpace(v)) {
		case "10", "DEBUG":
			return LevelDebug
		case "20", "INFO":
			return LevelInfo
		case "30", "WARNING", "WARN":
			return LevelWarning
		case "40", "ERROR":
			return LevelError
		case "50", "CRITICAL":
			return LevelCritical
		}
	}
	return LevelInfo
}

type Logger struct {
	mu            sync.Mutex
	stdoutLevel   Level
	fileLevel     Level
	fileWriter    io.WriteCloser
	stdoutHandler *log.Logger
}

var globalLogger *Logger

func init() {
	globalLogger = &Logger{
		stdoutLevel:   LevelInfo,
		fileLevel:     LevelWarning,
		stdoutHandler: log.New(os.Stdout, "", 0),
	}
}

func SetStdoutLevel(lvl Level) {
	globalLogger.mu.Lock()
	defer globalLogger.mu.Unlock()
	globalLogger.stdoutLevel = lvl
}

func SetFileHandler(filePath string, lvl Level) error {
	globalLogger.mu.Lock()
	defer globalLogger.mu.Unlock()

	if globalLogger.fileWriter != nil {
		globalLogger.fileWriter.Close()
	}

	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	globalLogger.fileWriter = f
	globalLogger.fileLevel = lvl
	return nil
}

func logMessage(lvl Level, lvlStr string, format string, v ...interface{}) {
	globalLogger.mu.Lock()
	defer globalLogger.mu.Unlock()

	now := time.Now().Format("2006-01-02 15:04:05.000")
	msg := fmt.Sprintf(format, v...)
	line := fmt.Sprintf("[%s] [%s] %s", now, lvlStr, msg)

	if lvl >= globalLogger.stdoutLevel {
		globalLogger.stdoutHandler.Println(line)
	}

	if globalLogger.fileWriter != nil && lvl >= globalLogger.fileLevel {
		fmt.Fprintln(globalLogger.fileWriter, line)
	}
}

func Debug(format string, v ...interface{}) {
	logMessage(LevelDebug, "DEBUG", format, v...)
}

func Info(format string, v ...interface{}) {
	logMessage(LevelInfo, "INFO", format, v...)
}

func Warning(format string, v ...interface{}) {
	logMessage(LevelWarning, "WARNING", format, v...)
}

func Error(format string, v ...interface{}) {
	logMessage(LevelError, "ERROR", format, v...)
}

func Critical(format string, v ...interface{}) {
	logMessage(LevelCritical, "CRITICAL", format, v...)
}
