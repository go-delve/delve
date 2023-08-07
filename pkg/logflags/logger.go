package logflags

import (
	"github.com/sirupsen/logrus"
	"io"
)

// Logger represents a generic interface for logging inside of
// Delve codebase.
type Logger interface {
	Debugf(format string, args ...interface{})
	Infof(format string, args ...interface{})
	Warnf(format string, args ...interface{})
	Errorf(format string, args ...interface{})

	Debug(args ...interface{})
	Info(args ...interface{})
	Warn(args ...interface{})
	Error(args ...interface{})
}

// LoggerFactory is used to create new Logger instances.
// SetLoggerFactory can be used to configure it.
//
// The given parameters fields and out can be both be nil.
type LoggerFactory func(flag bool, fields Fields, out io.Writer) Logger

var loggerFactory LoggerFactory

// SetLoggerFactory will ensure that every Logger created by this package, will be now created
// by the given LoggerFactory. Default behavior will be a logrus based Logger instance using DefaultFormatter.
func SetLoggerFactory(lf LoggerFactory) {
	loggerFactory = lf
}

// Fields type wraps many fields for Logger
type Fields map[string]interface{}

type logrusLogger struct {
	*logrus.Entry
}
