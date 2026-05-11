package logger

import (
	"fmt"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Config struct {
	Level      string
	Format     string
	OutputPath string
}

func New(cfg Config) (*zap.Logger, error) {
	level, err := parseLevel(cfg.Level)
	if err != nil {
		return nil, fmt.Errorf("logger: %w", err)
	}

	encoderCfg := buildEncoderConfig(cfg.Format)

	var encoding string
	if cfg.Format == "json" {
		encoding = "json"
	} else {
		encoding = "console"
	}

	outputPath := cfg.OutputPath
	if outputPath == "" {
		outputPath = "stderr"
	}

	zapCfg := zap.Config{
		Level:             zap.NewAtomicLevelAt(level),
		Development:       cfg.Format == "console",
		Encoding:          encoding,
		EncoderConfig:     encoderCfg,
		OutputPaths:       []string{outputPath},
		ErrorOutputPaths:  []string{"stderr"},
		DisableCaller:     false,
		DisableStacktrace: level != zapcore.DebugLevel,
	}

	logger, err := zapCfg.Build(
		zap.AddCallerSkip(0),
		zap.Fields(zap.String("service", "nexus")),
	)
	if err != nil {
		return nil, fmt.Errorf("logger: build failed: %w", err)
	}

	return logger, nil
}

func NewNop() *zap.Logger {
	return zap.NewNop()
}

func parseLevel(level string) (zapcore.Level, error) {
	switch level {
	case "debug":
		return zapcore.DebugLevel, nil
	case "info", "":
		return zapcore.InfoLevel, nil
	case "warn":
		return zapcore.WarnLevel, nil
	case "error":
		return zapcore.ErrorLevel, nil
	default:
		return zapcore.InfoLevel, fmt.Errorf("unknown log level %q", level)
	}
}

func buildEncoderConfig(format string) zapcore.EncoderConfig {
	if format == "json" {
		return zapcore.EncoderConfig{
			TimeKey:        "ts",
			LevelKey:       "level",
			NameKey:        "logger",
			CallerKey:      "caller",
			MessageKey:     "msg",
			StacktraceKey:  "stacktrace",
			LineEnding:     zapcore.DefaultLineEnding,
			EncodeLevel:    zapcore.LowercaseLevelEncoder,
			EncodeTime:     zapcore.ISO8601TimeEncoder,
			EncodeDuration: zapcore.StringDurationEncoder,
			EncodeCaller:   zapcore.ShortCallerEncoder,
		}
	}

	return zapcore.EncoderConfig{
		TimeKey:        "T",
		LevelKey:       "L",
		NameKey:        "N",
		CallerKey:      "C",
		MessageKey:     "M",
		StacktraceKey:  "S",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.CapitalColorLevelEncoder,
		EncodeTime:     zapcore.TimeEncoderOfLayout("15:04:05"),
		EncodeDuration: zapcore.StringDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}
}

func With(base *zap.Logger, fields ...zap.Field) *zap.Logger {
	return base.With(fields...)
}
