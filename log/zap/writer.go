package zap

// 负责输出Writer的创建和资源管理

import (
	"fmt"
	"os"

	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

type writerSet struct {
	syncers []zapcore.WriteSyncer
	closers []*lumberjack.Logger
}

func newWriter(cfg Config) (*writerSet, error) {
	result := &writerSet{}

	rollback := func(err error) (*writerSet, error) {
		for _, c := range result.closers {
			_ = c.Close()
		}
		return nil, err
	}

	if len(cfg.Output) == 0 {
		cfg.Output = []string{"stdout"}
	}

	for _, output := range cfg.Output {
		switch output {
		case "stdout":
			result.syncers = append(
				result.syncers,
				zapcore.Lock(zapcore.AddSync(os.Stdout)),
			)

		case "stderr":
			result.syncers = append(
				result.syncers,
				zapcore.Lock(zapcore.AddSync(os.Stderr)),
			)

		case "file":
			fc := cfg.File

			if fc.Filename == "" {
				return rollback(
					fmt.Errorf("file output requires filename"),
				)
			}

			if fc.MaxSize <= 0 {
				fc.MaxSize = 100
			}

			if fc.MaxBackups <= 0 {
				fc.MaxBackups = 10
			}

			if fc.MaxAge <= 0 {
				fc.MaxAge = 7
			}

			writer := &lumberjack.Logger{
				Filename:   fc.Filename,
				MaxSize:    fc.MaxSize,
				MaxBackups: fc.MaxBackups,
				MaxAge:     fc.MaxAge,
				Compress:   fc.Compress,
				LocalTime:  fc.LocalTime,
			}

			result.closers = append(result.closers, writer)

			result.syncers = append(
				result.syncers,
				zapcore.AddSync(writer),
			)

		default:
			return rollback(
				fmt.Errorf("unsupported log output: %s", output),
			)
		}
	}

	return result, nil
}
