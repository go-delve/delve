package logflags

import (
	"github.com/sirupsen/logrus"
	"io"
)

// Logger represents a generic interface for logging inside of
// Delve codebase.
type Logger interface {
	// WithField returns a new Logger enriched with the given field.
	WithField(key string, value interface{}) Logger
	// WithFields returns a new Logger enriched with the given fields.
	WithFields(fields Fields) Logger
	// WithError returns a new Logger enriched with the given error.
	WithError(err error) Logger

	Debugf(format string, args ...interface{})
	Infof(format string, args ...interface{})
	Printf(format string, args ...interface{})
	Warnf(format string, args ...interface{})
	Warningf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
	Fatalf(format string, args ...interface{})
	Panicf(format string, args ...interface{})

	Debug(args ...interface{})
	Info(args ...interface{})
	Print(args ...interface{})
	Warn(args ...interface{})
	Warning(args ...interface{})
	Error(args ...interface{})
	Fatal(args ...interface{})
	Panic(args ...interface{})

	Debugln(args ...interface{})
	Infoln(args ...interface{})
	Println(args ...interface{})
	Warnln(args ...interface{})
	Warningln(args ...interface{})
	Errorln(args ...interface{})
	Fatalln(args ...interface{})
	Panicln(args ...interface{})
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

func (l *logrusLogger) WithField(key string, value interface{}) Logger {
	return &logrusLogger{l.Entry.WithField(key, value)}
}

func (l *logrusLogger) WithFields(fields Fields) Logger {
	return &logrusLogger{l.Entry.WithFields(logrus.Fields(fields))}
}

func (l *logrusLogger) WithError(err error) Logger {
	return &logrusLogger{l.Entry.WithError(err)}
}
