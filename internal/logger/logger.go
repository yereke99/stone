package logger

import (
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func New(env string) (*zap.Logger, error) {
	var cfg zap.Config
	if strings.EqualFold(env, "production") {
		cfg = zap.NewProductionConfig()
	} else {
		cfg = zap.NewDevelopmentConfig()
	}

	cfg.OutputPaths = []string{"stderr"}
	cfg.ErrorOutputPaths = []string{"stderr"}
	cfg.EncoderConfig.TimeKey = "time"
	cfg.EncoderConfig.LevelKey = "level"
	cfg.EncoderConfig.NameKey = "logger"
	cfg.EncoderConfig.CallerKey = "caller"
	cfg.EncoderConfig.MessageKey = "message"
	cfg.EncoderConfig.StacktraceKey = "stacktrace"
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	cfg.EncoderConfig.EncodeDuration = zapcore.StringDurationEncoder
	cfg.EncoderConfig.EncodeCaller = zapcore.ShortCallerEncoder

	return cfg.Build(
		zap.AddCaller(),
		zap.AddStacktrace(zapcore.ErrorLevel),
	)
}
