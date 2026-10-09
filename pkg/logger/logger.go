// Package logger provides a Zap-backed logger with Gin and GORM adapters.
// Lines go to stdout plus daily files (7-day retention); empty dir means
// stdout only. Production encodes JSON, development is human-readable.
package logger

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	gormlogger "gorm.io/gorm/logger"
)

type Logger struct {
	*zap.SugaredLogger
	zapLogger *zap.Logger
}

// New builds a logger for env with level, writing to stdout plus output dir.
// An uncreatable dir falls back to stdout with a stderr warning.
func New(env, level, output string) *Logger {
	lvl := parseLevel(level)
	var cfg zap.Config
	if strings.ToLower(env) == "production" {
		cfg = zap.NewProductionConfig()
		cfg.Level.SetLevel(lvl)
		cfg.EncoderConfig.TimeKey = "ts"
	} else {
		cfg = zap.NewDevelopmentConfig()
		cfg.Level.SetLevel(lvl)
		cfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}
	var enc zapcore.Encoder
	if cfg.Encoding == "json" {
		enc = zapcore.NewJSONEncoder(cfg.EncoderConfig)
	} else {
		enc = zapcore.NewConsoleEncoder(cfg.EncoderConfig)
	}
	atomic := zap.NewAtomicLevelAt(lvl)
	cores := []zapcore.Core{
		zapcore.NewCore(enc, zapcore.Lock(os.Stdout), atomic),
	}
	if output != "" {
		if err := os.MkdirAll(output, 0o700); err != nil {
			fmt.Fprintf(os.Stderr, "logger: cannot create log dir %q: %v (stdout only)\n", output, err)
		} else {
			pruneOldLogs(output, time.Now())
			cores = append(cores, zapcore.NewCore(enc, zapcore.Lock(&dailyWriter{dir: output, now: time.Now}), atomic))
		}
	}
	zl := zap.New(
		zapcore.NewTee(cores...),
		zap.AddCaller(),
		zap.AddStacktrace(zapcore.ErrorLevel),
	)
	return &Logger{SugaredLogger: zl.Sugar(), zapLogger: zl}
}

func NewNop() *Logger {
	zl := zap.NewNop()
	return &Logger{SugaredLogger: zl.Sugar(), zapLogger: zl}
}

// Sync flushes buffered output. Call on shutdown.
func (l *Logger) Sync() { _ = l.zapLogger.Sync() }

const retainLogDays = 7

var logFileRe = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})\.logs$`)

type dailyWriter struct {
	mu   sync.Mutex
	dir  string
	now  func() time.Time
	day  string
	file *os.File
}

// Write appends p to today's file. Rotation failure drops the line (stdout
// still carries it) rather than blocking.
func (w *dailyWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	day := w.now().Format("2006-01-02")
	if w.file == nil || day != w.day {
		if err := w.rotate(day); err != nil {
			return 0, err
		}
	}
	return w.file.Write(p)
}

func (w *dailyWriter) rotate(day string) error {
	if w.file != nil {
		_ = w.file.Close()
		w.file = nil
	}
	if err := os.MkdirAll(w.dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(w.dir, day+".logs"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	w.file, w.day = f, day
	return nil
}

func (w *dailyWriter) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	return w.file.Sync()
}

func pruneOldLogs(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	cutoff := today.AddDate(0, 0, -(retainLogDays - 1))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := logFileRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		d, err := time.Parse("2006-01-02", m[1]+"-"+m[2]+"-"+m[3])
		if err != nil {
			continue
		}
		if d.Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

func (l *Logger) GinWriter() io.Writer {
	if l == nil || l.SugaredLogger == nil {
		return os.Stdout
	}
	return zapWriter{l}
}

type zapWriter struct{ l *Logger }

func (w zapWriter) Write(p []byte) (int, error) {
	msg := strings.TrimSpace(string(p))
	if msg != "" {
		w.l.Info(msg)
	}
	return len(p), nil
}

func (l *Logger) GormLogger(level gormlogger.LogLevel, slowThreshold time.Duration) gormlogger.Interface {
	if l == nil || l.SugaredLogger == nil {
		return gormlogger.Default.LogMode(level)
	}
	if slowThreshold <= 0 {
		slowThreshold = 200 * time.Millisecond
	}
	return &gormAdapter{log: l, level: level, slow: slowThreshold}
}

type gormAdapter struct {
	log   *Logger
	level gormlogger.LogLevel
	slow  time.Duration
}

// LogMode returns a copy of the adapter at the requested GORM level.
func (a *gormAdapter) LogMode(level gormlogger.LogLevel) gormlogger.Interface {
	cp := *a
	cp.level = level
	return &cp
}

// Info logs a GORM info message when the level allows it.
func (a *gormAdapter) Info(_ context.Context, msg string, data ...interface{}) {
	if a.level >= gormlogger.Info {
		a.log.Infof(msg, data...)
	}
}

// Warn logs a GORM warning when the level allows it.
func (a *gormAdapter) Warn(_ context.Context, msg string, data ...interface{}) {
	if a.level >= gormlogger.Warn {
		a.log.Warnf(msg, data...)
	}
}

// Error logs a GORM error when the level allows it.
func (a *gormAdapter) Error(_ context.Context, msg string, data ...interface{}) {
	if a.level >= gormlogger.Error {
		a.log.Errorf(msg, data...)
	}
}

// Trace logs a SQL statement by severity: errors first, then slow queries
// over the threshold as warnings, everything else at debug.
func (a *gormAdapter) Trace(_ context.Context, begin time.Time, fc func() (string, int64), err error) {
	if a.level <= gormlogger.Silent {
		return
	}
	elapsed := time.Since(begin)
	sql, rows := fc()
	switch {
	case err != nil && a.level >= gormlogger.Error:
		a.log.Errorf("sql error after %s (%d rows): %s: %v", elapsed, rows, sql, err)
	case elapsed > a.slow && a.level >= gormlogger.Warn:
		a.log.Warnf("slow sql %s (%d rows): %s", elapsed, rows, sql)
	case a.level >= gormlogger.Info:
		a.log.Debugf("sql %s (%d rows): %s", elapsed, rows, sql)
	}
}

func parseLevel(s string) zapcore.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return zapcore.DebugLevel
	case "warn", "warning":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	case "fatal":
		return zapcore.FatalLevel
	case "info", "":
		return zapcore.InfoLevel
	default:
		return zapcore.InfoLevel
	}
}
