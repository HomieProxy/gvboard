// Package logger 基于 zap 的结构化日志。
package logger

import (
	"fmt"
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"

	"gvboard/internal/config"
)

// New 根据配置创建日志实例。写文件时按 100MB 切割，保留 30 天。
func New(cfg config.LogConfig) (*zap.Logger, error) {
	level, err := zapcore.ParseLevel(cfg.Level)
	if err != nil {
		return nil, fmt.Errorf("log.level 无效: %s", cfg.Level)
	}

	encCfg := zap.NewProductionEncoderConfig()
	encCfg.TimeKey = "time"
	encCfg.EncodeTime = zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05.000")
	var encoder zapcore.Encoder
	if cfg.Format == "json" {
		encoder = zapcore.NewJSONEncoder(encCfg)
	} else {
		encCfg.EncodeLevel = zapcore.CapitalLevelEncoder
		encoder = zapcore.NewConsoleEncoder(encCfg)
	}

	var ws zapcore.WriteSyncer
	if cfg.File != "" {
		ws = zapcore.AddSync(&lumberjack.Logger{
			Filename:  cfg.File,
			MaxSize:   100,
			MaxAge:    30,
			LocalTime: true,
			Compress:  true,
		})
	} else {
		ws = zapcore.AddSync(os.Stdout)
	}

	core := zapcore.NewCore(encoder, ws, level)
	return zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel)), nil
}
