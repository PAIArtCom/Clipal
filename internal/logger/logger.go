package logger

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/PAIArtCom/Clipal/internal/config"
)

// Level represents log levels as integers for comparison
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

// Logger is the global logger instance
type Logger struct {
	mu       sync.RWMutex
	minLevel Level
	outMu    sync.Mutex
	out      io.Writer

	hookMu sync.RWMutex
	hook   Hook
}

var (
	instance *Logger
	once     sync.Once
)

type Hook func(levelStr string, message string)

// GetLogger returns the singleton logger instance
func GetLogger() *Logger {
	once.Do(func() {
		instance = &Logger{
			minLevel: LevelInfo,
			out:      os.Stdout,
		}
	})
	return instance
}

func (l *Logger) SetHook(h Hook) {
	l.hookMu.Lock()
	l.hook = h
	l.hookMu.Unlock()
}

func SetHook(h Hook) {
	GetLogger().SetHook(h)
}

// SetLevel sets the minimum log level
func (l *Logger) SetLevel(level config.LogLevel) {
	l.mu.Lock()
	defer l.mu.Unlock()

	switch level {
	case config.LogLevelDebug:
		l.minLevel = LevelDebug
	case config.LogLevelInfo:
		l.minLevel = LevelInfo
	case config.LogLevelWarn:
		l.minLevel = LevelWarn
	case config.LogLevelError:
		l.minLevel = LevelError
	default:
		l.minLevel = LevelInfo
	}
}

// SetLevelFromString sets the log level from a string
func (l *Logger) SetLevelFromString(level string) {
	l.SetLevel(config.LogLevel(level))
}

// log writes a log message if the level is high enough
func (l *Logger) log(level Level, levelStr string, format string, args ...interface{}) {
	l.mu.RLock()
	minLevel := l.minLevel
	l.mu.RUnlock()

	if level < minLevel {
		return
	}

	timestamp := time.Now().Format("2006-01-02 15:04:05.000")
	message := fmt.Sprintf(format, args...)
	l.outMu.Lock()
	fmt.Fprintf(l.out, "[%-5s] %s %s\n", levelStr, timestamp, message)
	l.outMu.Unlock()

	l.hookMu.RLock()
	h := l.hook
	l.hookMu.RUnlock()
	if h != nil {
		func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "clipal: log hook panicked: %v\n", r)
				}
			}()
			h(levelStr, message)
		}()
	}
}

func (l *Logger) SetOutput(w io.Writer) {
	if w == nil {
		w = io.Discard
	}
	l.outMu.Lock()
	l.out = w
	l.outMu.Unlock()
}

func SetOutput(w io.Writer) {
	GetLogger().SetOutput(w)
}

// Debug logs a debug message
func (l *Logger) Debug(format string, args ...interface{}) {
	l.log(LevelDebug, "DEBUG", format, args...)
}

// Info logs an info message
func (l *Logger) Info(format string, args ...interface{}) {
	l.log(LevelInfo, "INFO", format, args...)
}

// Warn logs a warning message
func (l *Logger) Warn(format string, args ...interface{}) {
	l.log(LevelWarn, "WARN", format, args...)
}

// Error logs an error message
func (l *Logger) Error(format string, args ...interface{}) {
	l.log(LevelError, "ERROR", format, args...)
}

// Convenience functions using the global logger

// Debug logs a debug message
func Debug(format string, args ...interface{}) {
	GetLogger().Debug(format, args...)
}

// Info logs an info message
func Info(format string, args ...interface{}) {
	GetLogger().Info(format, args...)
}

// Warn logs a warning message
func Warn(format string, args ...interface{}) {
	GetLogger().Warn(format, args...)
}

// Error logs an error message
func Error(format string, args ...interface{}) {
	GetLogger().Error(format, args...)
}

// SetLevel sets the log level using the global logger
func SetLevel(level config.LogLevel) {
	GetLogger().SetLevel(level)
}

// SetLevelFromString sets the log level from a string using the global logger
func SetLevelFromString(level string) {
	GetLogger().SetLevelFromString(level)
}
