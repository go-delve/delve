package logflags

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"time"
)

// Logger represents a generic interface for logging inside of
// Delve codebase.
type Logger interface {
	Debugf(format string, args ...interface{})
	Infof(format string, args ...interface{})
	Warnf(format string, args ...interface{})
	Errorf(format string, args ...interface{})

	Debug(...interface{})
	Info(...interface{})
	Warn(...interface{})
	Error(...interface{})
}

// LoggerFactory is used to create new Logger instances.
// SetLoggerFactory can be used to configure it.
//
// The given parameters fields and out can be both be nil.
type LoggerFactory func(flag bool, fields Fields, out io.Writer) Logger

var loggerFactory LoggerFactory

// SetLoggerFactory will ensure that every Logger created by this package, will be now created
// by the given LoggerFactory. Default behavior will be a slog based Logger instance using DefaultFormatter.
func SetLoggerFactory(lf LoggerFactory) {
	loggerFactory = lf
}

// Fields type wraps many fields for Logger
type Fields map[string]interface{}

type slogLogger struct {
	s *slog.Logger
}

func sloglog(logger *slog.Logger, level slog.Level, thunk func() string) {
	// see the "Wrapping" example in the documentation of log/slog
	if !logger.Enabled(context.Background(), slog.LevelDebug) {
		return
	}
	var pcs [1]uintptr
	runtime.Callers(3, pcs[:]) // skip [ runtime.Callers, sloglog, the caller of this function ]
	r := slog.NewRecord(time.Now(), level, thunk(), pcs[0])
	_ = logger.Handler().Handle(context.Background(), r)
}

func (s slogLogger) Debugf(format string, args ...interface{}) {
	sloglog(s.s, slog.LevelDebug, func() string {
		return fmt.Sprintf(format, args...)
	})
}

func (s slogLogger) Infof(format string, args ...interface{}) {
	sloglog(s.s, slog.LevelInfo, func() string {
		return fmt.Sprintf(format, args...)
	})
}

func (s slogLogger) Warnf(format string, args ...interface{}) {
	sloglog(s.s, slog.LevelWarn, func() string {
		return fmt.Sprintf(format, args...)
	})
}

func (s slogLogger) Errorf(format string, args ...interface{}) {
	sloglog(s.s, slog.LevelError, func() string {
		return fmt.Sprintf(format, args...)
	})
}

func (s slogLogger) Debug(args ...interface{}) {
	sloglog(s.s, slog.LevelDebug, func() string {
		return fmt.Sprint(args...)
	})
}

func (s slogLogger) Info(args ...interface{}) {
	sloglog(s.s, slog.LevelInfo, func() string {
		return fmt.Sprint(args...)
	})
}

func (s slogLogger) Warn(args ...interface{}) {
	sloglog(s.s, slog.LevelWarn, func() string {
		return fmt.Sprint(args...)
	})
}

func (s slogLogger) Error(args ...interface{}) {
	sloglog(s.s, slog.LevelError, func() string {
		return fmt.Sprint(args...)
	})
}
