//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package log

// ... existing code ...

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// TestSetLevel verifies that SetLevel correctly updates the
// underlying zap file atomic level according to the provided level
// string. It iterates through all supported levels and checks the
// fileZapLevel after the call.
func TestSetLevel(t *testing.T) {
	cases := []struct {
		in       string
		expected zapcore.Level
	}{
		{LevelDebug, zapcore.DebugLevel},
		{LevelInfo, zapcore.InfoLevel},
		{LevelWarn, zapcore.WarnLevel},
		{LevelError, zapcore.ErrorLevel},
		{LevelFatal, zapcore.FatalLevel},
		{"unknown", zapcore.InfoLevel}, // default branch
	}

	for _, c := range cases {
		SetLevel(c.in)
		got := fileZapLevel.Level()
		assert.Equal(t, c.expected, got,
			"SetLevel(%q) should set level to %v", c.in, c.expected)
	}
}

// TestTraceDisabledByDefault ensures trace logging starts disabled and Tracef is a no-op.
func TestTraceDisabledByDefault(t *testing.T) {
	stub := &stubLogger{}
	oldDefault := Default
	oldTrace := traceEnabled
	Default = stub
	t.Cleanup(func() {
		Default = oldDefault
		traceEnabled = oldTrace
	})

	assert.False(t, traceEnabled,
		"traceEnabled should be false by default")

	Tracef("hello %s", "world")

	assert.Equal(t, 0, stub.debugfCalls,
		"Tracef should not log when trace is disabled")
}

// TestTracefEnabled makes sure Tracef forwards the call when trace is enabled.
func TestTracefEnabled(t *testing.T) {
	stub := &stubLogger{}
	oldDefault := Default
	oldTrace := traceEnabled
	Default = stub
	SetTraceEnabled(true)
	t.Cleanup(func() {
		Default = oldDefault
		traceEnabled = oldTrace
	})

	Tracef("hello %s", "world")

	assert.Equal(t, 1, stub.debugfCalls,
		"Tracef should log once when trace is enabled")
	assert.True(t, strings.HasPrefix(stub.lastFormat, "[TRACE] "),
		"Tracef should prefix message with \"[TRACE] \"; got %q",
		stub.lastFormat)
}

// stubLogger is a minimal implementation of Logger that captures
// Debugf calls for verification.
// Only the methods required by the tests are implemented; the rest
// are no-ops to satisfy the interface.
type stubLogger struct {
	lastFormat  string
	debugfCalls int
}

func (s *stubLogger) Debug(args ...any) {}
func (s *stubLogger) Debugf(format string, args ...any) {
	s.debugfCalls++
	s.lastFormat = format
}
func (s *stubLogger) Info(args ...any)                  {}
func (s *stubLogger) Infof(format string, args ...any)  {}
func (s *stubLogger) Warn(args ...any)                  {}
func (s *stubLogger) Warnf(format string, args ...any)  {}
func (s *stubLogger) Error(args ...any)                 {}
func (s *stubLogger) Errorf(format string, args ...any) {}
func (s *stubLogger) Fatal(args ...any)                 {}
func (s *stubLogger) Fatalf(format string, args ...any) {}

func TestAddFileOutput(t *testing.T) {
	// Save and restore global state.
	oldDefault := Default
	oldCtxDefault := ContextDefault
	t.Cleanup(func() {
		Default = oldDefault
		ContextDefault = oldCtxDefault
		CloseFileOutput()
	})

	dir := t.TempDir()
	err := AddFileOutput(dir)
	require.NoError(t, err, "AddFileOutput should succeed")
	assert.True(t, fileCoreAdded,
		"fileCoreAdded should be true after AddFileOutput")

	// Verify log file was created under date-based subdirectory.
	now := time.Now()
	dateDir := filepath.Join(dir,
		fmt.Sprintf("%04d", now.Year()),
		fmt.Sprintf("%02d", now.Month()),
		fmt.Sprintf("%02d", now.Day()),
	)
	entries, readErr := os.ReadDir(dateDir)
	require.NoError(t, readErr, "date directory should exist")
	require.NotEmpty(t, entries, "date directory should contain log files")

	// Verify filename pattern: openclaw_YYYYMMDD_HHmmss_XXXXX.log
	found := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "openclaw_") &&
			strings.HasSuffix(entry.Name(), ".log") {
			found = true
			break
		}
	}
	assert.True(t, found, "should find a log file matching openclaw_*.log pattern")

	// Verify idempotency: second call should be a no-op.
	err = AddFileOutput(dir)
	require.NoError(t, err, "AddFileOutput should be idempotent")

	// Verify that Default and ContextDefault are still SugaredLogger.
	_, ok1 := Default.(*zap.SugaredLogger)
	assert.True(t, ok1, "Default should be *zap.SugaredLogger")
	_, ok2 := ContextDefault.(*zap.SugaredLogger)
	assert.True(t, ok2, "ContextDefault should be *zap.SugaredLogger")

	// Close the file before TempDir cleanup tries to remove it.
	CloseFileOutput()
}

func TestAddFileOutput_BadDir(t *testing.T) {
	oldFileCoreAdded := fileCoreAdded
	t.Cleanup(func() {
		fileCoreAdded = oldFileCoreAdded
	})

	// Use a path that cannot be created as a directory.
	badDir := filepath.Join(t.TempDir(), "file-not-dir")
	// Create a file where a directory is expected.
	f, err := os.Create(badDir)
	require.NoError(t, err)
	f.Close()

	err = AddFileOutput(filepath.Join(badDir, "subdir"))
	assert.Error(t, err,
		"AddFileOutput should fail when directory cannot be created")
}
