//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package log provides logging utilities.
package log

import (
	"context"
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"trpc.group/trpc-go/trpc-a2a-go/log"
)

// Log level constants
const (
	LevelDebug = "debug"
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"
	LevelFatal = "fatal"
)

var (
	// fileZapLevel controls the log level for file-based output.
	// It is managed independently of consoleZapLevel so that the
	// console and file log levels can be tuned separately.
	fileZapLevel = zap.NewAtomicLevelAt(zapcore.InfoLevel)

	// consoleZapLevel controls the log level for console (stdout) output.
	consoleZapLevel = zap.NewAtomicLevelAt(zapcore.InfoLevel)

	traceEnabled  = false
	fileCoreAdded = false
	logFile       *os.File
)

// Default borrows logging utilities from zap.
// You may replace it with whatever logger you like as long as it implements log.Logger interface.
var Default Logger = zap.New(
	zapcore.NewCore(
		zapcore.NewConsoleEncoder(encoderConfig),
		zapcore.AddSync(os.Stdout),
		consoleZapLevel,
	),
	zap.AddCaller(),
	zap.AddCallerSkip(1),
).Sugar()

// ContextDefault is the default logger used by *Context helpers.
// It uses a separate zap logger so that caller information for helpers
// like DebugContext can be tuned independently of Default.
var ContextDefault Logger = zap.New(
	zapcore.NewCore(
		zapcore.NewConsoleEncoder(encoderConfig),
		zapcore.AddSync(os.Stdout),
		consoleZapLevel,
	),
	zap.AddCaller(),
	zap.AddCallerSkip(1),
).Sugar()

func init() {
	log.Default = Default
}

// SetLevel sets the log level for file-based output to the specified level.
// Valid levels are: "debug", "info", "warn", "error", "fatal"
//
// Note: Use SetConsoleLevel to independently control the console (stdout)
// log level.
func SetLevel(level string) {
	switch level {
	case LevelDebug:
		fileZapLevel.SetLevel(zapcore.DebugLevel)
	case LevelInfo:
		fileZapLevel.SetLevel(zapcore.InfoLevel)
	case LevelWarn:
		fileZapLevel.SetLevel(zapcore.WarnLevel)
	case LevelError:
		fileZapLevel.SetLevel(zapcore.ErrorLevel)
	case LevelFatal:
		fileZapLevel.SetLevel(zapcore.FatalLevel)
	default:
		// Default to info level if the level is not recognized
		fileZapLevel.SetLevel(zapcore.InfoLevel)
	}
}

// SetConsoleLevel sets the log level for console (stdout) output to the
// specified level. It does not affect the file-based log output level.
// Valid levels are: "debug", "info", "warn", "error", "fatal"
func SetConsoleLevel(level string) {
	switch level {
	case LevelDebug:
		consoleZapLevel.SetLevel(zapcore.DebugLevel)
	case LevelInfo:
		consoleZapLevel.SetLevel(zapcore.InfoLevel)
	case LevelWarn:
		consoleZapLevel.SetLevel(zapcore.WarnLevel)
	case LevelError:
		consoleZapLevel.SetLevel(zapcore.ErrorLevel)
	case LevelFatal:
		consoleZapLevel.SetLevel(zapcore.FatalLevel)
	default:
		// Default to info level if the level is not recognized
		consoleZapLevel.SetLevel(zapcore.InfoLevel)
	}
}

// fileEncoderConfig is the encoder config for file-based log output.
// It uses a stable, machine-readable format without colors.
var fileEncoderConfig = zapcore.EncoderConfig{
	TimeKey:        "ts",
	LevelKey:       "level",
	NameKey:        "logger",
	CallerKey:      "caller",
	MessageKey:     "msg",
	StacktraceKey:  "stacktrace",
	LineEnding:     zapcore.DefaultLineEnding,
	EncodeLevel:    zapcore.LowercaseLevelEncoder,
	EncodeTime:     zapcore.RFC3339NanoTimeEncoder,
	EncodeDuration: zapcore.NanosDurationEncoder,
	EncodeCaller:   zapcore.ShortCallerEncoder,
}

// AddFileOutput adds a file-based log output alongside the existing
// stdout output. Logs are written in JSON format to the specified
// directory. The file is named "openclaw.log" and is truncated on
// each call so that every startup begins with a fresh log file.
// This function is idempotent: calling it more than once has no
// effect.
func AddFileOutput(dir string) error {
	if fileCoreAdded {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	logPath := filepath.Join(dir, "openclaw.log")
	file, err := os.OpenFile(logPath,
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}

	fileCore := zapcore.NewCore(
		zapcore.NewJSONEncoder(fileEncoderConfig),
		zapcore.AddSync(file),
		fileZapLevel,
	)

	// Replace Default with a tee that writes to both stdout and file.
	if sugar, ok := Default.(*zap.SugaredLogger); ok {
		teeCore := zapcore.NewTee(sugar.Desugar().Core(), fileCore)
		Default = zap.New(teeCore,
			zap.AddCaller(),
			zap.AddCallerSkip(1),
		).Sugar()
	}

	// Replace ContextDefault similarly.
	if sugar, ok := ContextDefault.(*zap.SugaredLogger); ok {
		teeCore := zapcore.NewTee(sugar.Desugar().Core(), fileCore)
		ContextDefault = zap.New(teeCore,
			zap.AddCaller(),
			zap.AddCallerSkip(1),
		).Sugar()
	}

	// Update the a2a-go log.Default reference.
	log.Default = Default

	logFile = file
	fileCoreAdded = true
	return nil
}

// CloseFileOutput closes the file-based log output if one was opened
// by AddFileOutput. It is safe to call even if no file output was
// configured. This is primarily useful for testing.
func CloseFileOutput() {
	if logFile != nil {
		_ = logFile.Close()
		logFile = nil
	}
	fileCoreAdded = false
}

var encoderConfig = zapcore.EncoderConfig{
	TimeKey:        "ts",
	LevelKey:       "lvl",
	NameKey:        "name",
	CallerKey:      "caller",
	MessageKey:     "message",
	StacktraceKey:  "stacktrace",
	LineEnding:     zapcore.DefaultLineEnding,
	EncodeLevel:    zapcore.CapitalColorLevelEncoder,
	EncodeTime:     zapcore.RFC3339TimeEncoder,
	EncodeDuration: zapcore.SecondsDurationEncoder,
	EncodeCaller:   zapcore.ShortCallerEncoder,
}

// Logger defines the logging interface used throughout trpc-agent-go.
//
// This interface matches trpc-a2a-go/log.Logger so that Default can be
// assigned to log.Default without introducing a direct dependency on
// any specific logging implementation.
type Logger interface {
	// Debug logs to DEBUG log. Arguments are handled in the manner of fmt.Print.
	Debug(args ...any)
	// Debugf logs to DEBUG log. Arguments are handled in the manner of fmt.Printf.
	Debugf(format string, args ...any)
	// Info logs to INFO log. Arguments are handled in the manner of fmt.Print.
	Info(args ...any)
	// Infof logs to INFO log. Arguments are handled in the manner of fmt.Printf.
	Infof(format string, args ...any)
	// Warn logs to WARNING log. Arguments are handled in the manner of fmt.Print.
	Warn(args ...any)
	// Warnf logs to WARNING log. Arguments are handled in the manner of fmt.Printf.
	Warnf(format string, args ...any)
	// Error logs to ERROR log. Arguments are handled in the manner of fmt.Print.
	Error(args ...any)
	// Errorf logs to ERROR log. Arguments are handled in the manner of fmt.Printf.
	Errorf(format string, args ...any)
	// Fatal logs to ERROR log. Arguments are handled in the manner of fmt.Print.
	Fatal(args ...any)
	// Fatalf logs to ERROR log. Arguments are handled in the manner of fmt.Printf.
	Fatalf(format string, args ...any)
}

// Debug logs to DEBUG log. Arguments are handled in the manner of fmt.Print.
func Debug(args ...any) {
	Default.Debug(args...)
}

// DebugContext logs to DEBUG log with context.
// By default, context is ignored and logs are delegated to ContextDefault.
var DebugContext = func(
	_ context.Context, args ...any,
) {
	ContextDefault.Debug(args...)
}

// Debugf logs to DEBUG log. Arguments are handled in the manner of fmt.Printf.
func Debugf(format string, args ...any) {
	Default.Debugf(format, args...)
}

// DebugfContext logs to DEBUG log with context and formatting.
var DebugfContext = func(
	_ context.Context, format string, args ...any,
) {
	ContextDefault.Debugf(format, args...)
}

// Info logs to INFO log. Arguments are handled in the manner of fmt.Print.
func Info(args ...any) {
	Default.Info(args...)
}

// InfoContext logs to INFO log with context.
var InfoContext = func(
	_ context.Context, args ...any,
) {
	ContextDefault.Info(args...)
}

// Infof logs to INFO log. Arguments are handled in the manner of fmt.Printf.
func Infof(format string, args ...any) {
	Default.Infof(format, args...)
}

// InfofContext logs to INFO log with context and formatting.
var InfofContext = func(
	_ context.Context, format string, args ...any,
) {
	ContextDefault.Infof(format, args...)
}

// Warn logs to WARNING log. Arguments are handled in the manner of fmt.Print.
func Warn(args ...any) {
	Default.Warn(args...)
}

// WarnContext logs to WARNING log with context.
var WarnContext = func(
	_ context.Context, args ...any,
) {
	ContextDefault.Warn(args...)
}

// Warnf logs to WARNING log. Arguments are handled in the manner of fmt.Printf.
func Warnf(format string, args ...any) {
	Default.Warnf(format, args...)
}

// WarnfContext logs to WARNING log with context and formatting.
var WarnfContext = func(
	_ context.Context, format string, args ...any,
) {
	ContextDefault.Warnf(format, args...)
}

// Error logs to ERROR log. Arguments are handled in the manner of fmt.Print.
func Error(args ...any) {
	Default.Error(args...)
}

// ErrorContext logs to ERROR log with context.
var ErrorContext = func(
	_ context.Context, args ...any,
) {
	ContextDefault.Error(args...)
}

// Errorf logs to ERROR log. Arguments are handled in the manner of fmt.Printf.
func Errorf(format string, args ...any) {
	Default.Errorf(format, args...)
}

// ErrorfContext logs to ERROR log with context and formatting.
var ErrorfContext = func(
	_ context.Context, format string, args ...any,
) {
	ContextDefault.Errorf(format, args...)
}

// Fatal logs to ERROR log. Arguments are handled in the manner of fmt.Print.
func Fatal(args ...any) {
	Default.Fatal(args...)
}

// FatalContext logs to ERROR log with context.
var FatalContext = func(
	_ context.Context, args ...any,
) {
	ContextDefault.Fatal(args...)
}

// Fatalf logs to ERROR log. Arguments are handled in the manner of fmt.Printf.
func Fatalf(format string, args ...any) {
	Default.Fatalf(format, args...)
}

// FatalfContext logs to ERROR log with context and formatting.
var FatalfContext = func(
	_ context.Context, format string, args ...any,
) {
	ContextDefault.Fatalf(format, args...)
}

// Tracef logs a message at the trace level with formatting.
func Tracef(format string, args ...any) {
	if !traceEnabled {
		return
	}
	Default.Debugf("[TRACE] "+format, args...)
}

// TracefContext logs a TRACE log with context and formatting.
var TracefContext = func(
	_ context.Context, format string, args ...any,
) {
	if !traceEnabled {
		return
	}
	ContextDefault.Debugf("[TRACE] "+format, args...)
}

// SetTraceEnabled sets the trace enabled flag.
func SetTraceEnabled(enabled bool) {
	traceEnabled = enabled
}

// IsTraceEnabled reports whether trace-level logging is currently active.
func IsTraceEnabled() bool {
	return traceEnabled
}
