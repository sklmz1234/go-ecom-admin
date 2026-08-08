// Package logger 封装 Zap 的初始化逻辑。
//
// 设计决策：
//  1. 不直接依赖 pkg/config，而是定义自己的 Config 结构体——日志模块应该能被
//     任何调用方独立使用/测试，不应该因为换了配置加载方式（Viper -> 别的东西）
//     被迫跟着改。main.go 负责把 pkg/config.LogConfig 转换成 logger.Config。
//  2. 用构造函数返回 *zap.Logger 交给调用方持有（依赖注入），而不是包级全局变量，
//     方便后续单元测试里替换成 zaptest.NewLogger。
package logger

import (
	"fmt"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Config 描述日志的行为，字段含义：
//   - Level: "debug" | "info" | "warn" | "error"
//   - Encoding: "console"（本地开发，人眼友好） | "json"（生产环境，便于日志采集）
//   - OutputPaths: 输出目标，例如 ["stdout"]，也可以是文件路径
type Config struct {
	Level       string
	Encoding    string
	OutputPaths []string
}

// New 根据 Config 构建一个可用的 *zap.Logger。
func New(cfg Config) (*zap.Logger, error) {
	level, err := zapcore.ParseLevel(defaultIfEmpty(cfg.Level, "info"))
	if err != nil {
		return nil, fmt.Errorf("logger: parse level %q: %w", cfg.Level, err)
	}

	encoding := defaultIfEmpty(cfg.Encoding, "console")
	outputPaths := cfg.OutputPaths
	if len(outputPaths) == 0 {
		outputPaths = []string{"stdout"}
	}

	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.TimeKey = "ts"
	encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderCfg.EncodeLevel = zapcore.CapitalColorLevelEncoder

	zapCfg := zap.Config{
		Level:            zap.NewAtomicLevelAt(level),
		Development:      false,
		Encoding:         encoding,
		EncoderConfig:    encoderCfg,
		OutputPaths:      outputPaths,
		ErrorOutputPaths: []string{"stderr"},
	}

	l, err := zapCfg.Build(zap.AddCallerSkip(0))
	if err != nil {
		return nil, fmt.Errorf("logger: build zap logger: %w", err)
	}
	return l, nil
}

func defaultIfEmpty(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
