// Command nsx-t-mockapi starts the local NSX-T mock API process.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"nsx-t-mockapi/internal/app"
	"nsx-t-mockapi/internal/clock"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var errUsage = errors.New("usage: nsx-t-mockapi serve -config /path/to/config.yaml")

type commandConfig struct {
	configPath string
}

func main() {
	os.Exit(realMain(context.Background(), os.Args[1:]))
}

func realMain(ctx context.Context, args []string) int {
	return realMainWithLogger(ctx, args, newLogger)
}

func realMainWithLogger(ctx context.Context, args []string, buildLogger func() (*zap.Logger, error)) int {
	logger, err := buildLogger()
	if err != nil {
		_, writeErr := fmt.Fprintf(os.Stderr, "{\"level\":\"fatal\",\"msg\":\"create logger\",\"error\":%q}\n", err.Error())
		if writeErr != nil {
			return 1
		}
		return 1
	}

	exitCode := 0
	cfg, err := parseCommand(args)
	if err != nil {
		logger.Error("invalid command line", zap.Error(err))
		exitCode = 1
	} else if err = app.Run(ctx, app.Options{
		ConfigPath: cfg.configPath,
		Logger:     logger,
		Clock:      clock.SystemClock{},
	}); err != nil {
		logger.Error("application failed", zap.String("config_path", cfg.configPath), zap.Error(err))
		exitCode = 1
	}

	if syncErr := logger.Sync(); syncErr != nil {
		_, writeErr := fmt.Fprintf(os.Stderr, "{\"level\":\"warn\",\"msg\":\"sync logger\",\"error\":%q}\n", syncErr.Error())
		if writeErr != nil {
			return 1
		}
	}

	return exitCode
}

func parseCommand(args []string) (commandConfig, error) {
	if len(args) == 0 {
		return commandConfig{}, errUsage
	}
	if args[0] != "serve" {
		return commandConfig{}, fmt.Errorf("%w: unknown command %q", errUsage, args[0])
	}

	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	configPath := flags.String("config", "", "path to YAML config file")
	if err := flags.Parse(args[1:]); err != nil {
		return commandConfig{}, fmt.Errorf("%w: %w", errUsage, err)
	}
	if flags.NArg() != 0 {
		return commandConfig{}, fmt.Errorf("%w: unexpected arguments %q", errUsage, flags.Args())
	}
	if configPath == nil || *configPath == "" {
		return commandConfig{}, fmt.Errorf("%w: -config is required", errUsage)
	}

	return commandConfig{configPath: *configPath}, nil
}

func newLogger() (*zap.Logger, error) {
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderConfig),
		stderrWriteSyncer{},
		zap.DebugLevel,
	)
	return zap.New(core, zap.AddCaller()), nil
}

type stderrWriteSyncer struct{}

func (stderrWriteSyncer) Write(p []byte) (int, error) {
	written, err := os.Stderr.Write(p)
	if err != nil {
		return written, fmt.Errorf("write stderr: %w", err)
	}

	return written, nil
}

func (stderrWriteSyncer) Sync() error {
	return nil
}
