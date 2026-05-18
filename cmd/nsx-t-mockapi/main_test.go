package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

var errLoggerFailed = errors.New("logger failed")

func TestParseCommandRejectsMissingConfig(t *testing.T) {
	t.Parallel()

	_, err := parseCommand([]string{"serve"})
	if err == nil {
		t.Fatal("parseCommand() error = nil, want error")
	}
}

func TestParseCommandAcceptsExplicitConfig(t *testing.T) {
	t.Parallel()

	cfg, err := parseCommand([]string{"serve", "-config", "/tmp/nsx.yaml"})
	if err != nil {
		t.Fatalf("parseCommand() error = %v", err)
	}
	if cfg.configPath != "/tmp/nsx.yaml" {
		t.Fatalf("configPath = %q, want /tmp/nsx.yaml", cfg.configPath)
	}
}

func TestParseCommandRejectsUnknownCommand(t *testing.T) {
	t.Parallel()

	_, err := parseCommand([]string{"start", "-config", "/tmp/nsx.yaml"})
	if err == nil {
		t.Fatal("parseCommand() error = nil, want error")
	}
}

func TestParseCommandRejectsExtraArgs(t *testing.T) {
	t.Parallel()

	_, err := parseCommand([]string{"serve", "-config", "/tmp/nsx.yaml", "extra"})
	if err == nil {
		t.Fatal("parseCommand() error = nil, want error")
	}
}

func TestRealMainWithLoggerReturnsFailureForMissingConfig(t *testing.T) {
	t.Parallel()

	core, logs := observer.New(zap.DebugLevel)
	buildLogger := func() (*zap.Logger, error) {
		return zap.New(core), nil
	}

	if code := realMainWithLogger(context.Background(), []string{"serve"}, buildLogger); code != 1 {
		t.Fatalf("realMainWithLogger() code = %d, want 1", code)
	}
	if logs.FilterMessage("invalid command line").Len() != 1 {
		t.Fatal("missing invalid command line log")
	}
}

func TestRealMainReturnsFailureForMissingConfig(t *testing.T) {
	if code := realMain(context.Background(), []string{"serve"}); code != 1 {
		t.Fatalf("realMain() code = %d, want 1", code)
	}
}

func TestRealMainWithLoggerReturnsFailureForInvalidConfig(t *testing.T) {
	t.Parallel()

	core, logs := observer.New(zap.DebugLevel)
	buildLogger := func() (*zap.Logger, error) {
		return zap.New(core), nil
	}

	configPath := filepath.Join(t.TempDir(), "invalid.yaml")
	if err := os.WriteFile(configPath, []byte("server:\n  listen_addr: \"127.0.0.1:0\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	code := realMainWithLogger(context.Background(), []string{"serve", "-config", configPath}, buildLogger)
	if code != 1 {
		t.Fatalf("realMainWithLogger() code = %d, want 1", code)
	}
	if logs.FilterMessage("application failed").Len() != 1 {
		t.Fatal("missing application failed log")
	}
}

func TestRealMainWithLoggerReturnsFailureWhenLoggerCannotBuild(t *testing.T) {
	t.Parallel()

	buildLogger := func() (*zap.Logger, error) {
		return nil, errLoggerFailed
	}

	if code := realMainWithLogger(context.Background(), []string{"serve"}, buildLogger); code != 1 {
		t.Fatalf("realMainWithLogger() code = %d, want 1", code)
	}
}

func TestNewLoggerBuildsJSONLogger(t *testing.T) {
	logger, err := newLogger()
	if err != nil {
		t.Fatalf("newLogger() error = %v", err)
	}
	if err = logger.Sync(); err != nil {
		t.Fatalf("logger.Sync() error = %v", err)
	}
}

func TestStderrWriteSyncerWritesAndSyncs(t *testing.T) {
	originalStderr := os.Stderr
	t.Cleanup(func() {
		os.Stderr = originalStderr
	})

	stderrFile, err := os.CreateTemp(t.TempDir(), "stderr-*")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	defer func() {
		if closeErr := stderrFile.Close(); closeErr != nil {
			t.Errorf("stderrFile.Close() error = %v", closeErr)
		}
	}()
	os.Stderr = stderrFile

	syncer := stderrWriteSyncer{}
	written, err := syncer.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if written != len("hello") {
		t.Fatalf("Write() written = %d, want %d", written, len("hello"))
	}
	if err = syncer.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
}

func TestStderrWriteSyncerReportsWriteError(t *testing.T) {
	originalStderr := os.Stderr
	t.Cleanup(func() {
		os.Stderr = originalStderr
	})

	closedStderr, err := os.CreateTemp(t.TempDir(), "stderr-*")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	if err = closedStderr.Close(); err != nil {
		t.Fatalf("closedStderr.Close() error = %v", err)
	}
	os.Stderr = closedStderr

	written, err := stderrWriteSyncer{}.Write([]byte("hello"))
	if err == nil {
		t.Fatal("Write() error = nil, want error")
	}
	if written != 0 {
		t.Fatalf("Write() written = %d, want 0", written)
	}
}

func TestRealMainWithLoggerReturnsFailureWhenFallbackStderrWriteFails(t *testing.T) {
	originalStderr := os.Stderr
	t.Cleanup(func() {
		os.Stderr = originalStderr
	})

	closedStderr, err := os.CreateTemp(t.TempDir(), "stderr-*")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	if err = closedStderr.Close(); err != nil {
		t.Fatalf("closedStderr.Close() error = %v", err)
	}
	os.Stderr = closedStderr

	buildLogger := func() (*zap.Logger, error) {
		return nil, errLoggerFailed
	}

	if code := realMainWithLogger(context.Background(), []string{"serve"}, buildLogger); code != 1 {
		t.Fatalf("realMainWithLogger() code = %d, want 1", code)
	}
}
