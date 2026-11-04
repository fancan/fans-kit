package zap

// zap层实现api层的Logger

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/fancan/fans-kit/log/api"
	uzap "go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

type loggerState struct {
	level uzap.AtomicLevel

	closers []*lumberjack.Logger

	closeOnce sync.Once
	closeErr  error
}

type Logger struct {
	core *uzap.Logger

	state *loggerState

	// root 标记该 Logger 是否为资源所有者（由 New 创建）。
	// 派生 Logger（With / WithCallerSkip）共享 state 但 root=false，
	// 从而避免派生 Logger 调用 Close 时关闭共享的文件 writer。
	root bool
}

type levelEnabler struct {
	level uzap.AtomicLevel
	min   zapcore.Level
	max   zapcore.Level
}

func (e levelEnabler) Enabled(level zapcore.Level) bool {
	return level >= e.min &&
		level <= e.max &&
		e.level.Enabled(level)
}

// New 创建Zap
func New(cfg Config) (*Logger, error) {
	if cfg.Service == "" {
		cfg.Service = "unknown"
	}

	if !cfg.Level.Valid() {
		return nil, fmt.Errorf("invalid log level: %v", cfg.Level)
	}

	if cfg.Format == "" {
		cfg.Format = "json"
	}

	if cfg.Format != "json" && cfg.Format != "console" {
		return nil, fmt.Errorf("unsupported format: %s", cfg.Format)
	}

	if len(cfg.Output) == 0 {
		cfg.Output = []string{"stdout"}
	}

	if cfg.SeparateLevelFiles {
		hasFile := false
		for _, output := range cfg.Output {
			if output == "file" {
				hasFile = true
				break
			}
		}

		if !hasFile {
			return nil, errors.New("separate level files requires a 'file' output")
		}

		if cfg.File.Filename == "" {
			return nil, errors.New("separate file output requires filename")
		}
	}

	atomicLevel := uzap.NewAtomicLevelAt(
		toZapLevel(cfg.Level),
	)

	state := &loggerState{
		level: atomicLevel,
	}

	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "timestamp",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.CapitalLevelEncoder,
		EncodeTime:     zapcore.RFC3339NanoTimeEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	if cfg.Format == "console" {
		encoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}

	var newEncoder func() zapcore.Encoder

	if cfg.Format == "console" {
		newEncoder = func() zapcore.Encoder {
			return zapcore.NewConsoleEncoder(encoderConfig)
		}
	} else {
		newEncoder = func() zapcore.Encoder {
			return zapcore.NewJSONEncoder(encoderConfig)
		}
	}

	cores := make([]zapcore.Core, 0, 8)

	// 创建指定等级范围的 Core。
	addCore := func(
		writer zapcore.WriteSyncer,
		min zapcore.Level,
		max zapcore.Level,
	) {
		cores = append(cores, zapcore.NewCore(
			newEncoder(),
			writer,
			levelEnabler{
				level: atomicLevel,
				min:   min,
				max:   max,
			},
		))
	}

	for _, output := range cfg.Output {
		switch output {
		case "stdout", "stderr":
			writers, err := newWriter(Config{
				Output: []string{output},
			})
			if err != nil {
				return nil, err
			}

			for _, w := range writers.syncers {
				addCore(w, zapcore.DebugLevel, zapcore.ErrorLevel)
			}

		case "file":
			fc := normalizeFileConfig(cfg.File)

			if fc.Filename == "" {
				return nil, errors.New("file output requires filename")
			}

			createFileWriter := func(suffix string) zapcore.WriteSyncer {
				w := &lumberjack.Logger{
					Filename:   fc.Filename + suffix,
					MaxSize:    fc.MaxSize,
					MaxBackups: fc.MaxBackups,
					MaxAge:     fc.MaxAge,
					Compress:   fc.Compress,
					LocalTime:  fc.LocalTime,
				}

				state.closers = append(state.closers, w)

				return zapcore.AddSync(w)
			}

			if cfg.SeparateLevelFiles {
				addCore(
					createFileWriter(".info"),
					zapcore.DebugLevel,
					zapcore.InfoLevel,
				)

				addCore(
					createFileWriter(".warning"),
					zapcore.WarnLevel,
					zapcore.WarnLevel,
				)

				addCore(
					createFileWriter(".error"),
					zapcore.ErrorLevel,
					zapcore.ErrorLevel,
				)
			} else {
				addCore(
					createFileWriter(""),
					zapcore.DebugLevel,
					zapcore.ErrorLevel,
				)
			}

		default:
			for _, c := range state.closers {
				_ = c.Close()
			}

			return nil, fmt.Errorf("unsupported output: %s", output)
		}
	}

	if len(cores) == 0 {
		return nil, errors.New("no available log core")
	}

	core := zapcore.NewTee(cores...)

	options := []uzap.Option{
		// 门面函数 log.Info() 与直接方法调用 logger.Info() 相差一帧，
		// 这里以直接方法调用为基准跳过 write + Info 两层；门面层再通过
		// WithCallerSkip(1) 补齐一帧，从而两条路径都能得到正确的调用位置。
		uzap.AddCallerSkip(2),
		uzap.Fields(
			uzap.String("service", cfg.Service),
		),
	}

	if cfg.Environment != "" {
		options = append(
			options,
			uzap.Fields(
				uzap.String("environment", cfg.Environment),
			),
		)
	}

	if cfg.Caller {
		options = append(options, uzap.AddCaller())
	}

	if cfg.Stacktrace {
		options = append(
			options,
			uzap.AddStacktrace(zapcore.ErrorLevel),
		)
	}

	base := uzap.New(core, options...)

	return &Logger{
		core:  base,
		state: state,
		root:  true,
	}, nil
}

func normalizeFileConfig(fc FileConfig) FileConfig {
	if fc.MaxSize <= 0 {
		fc.MaxSize = 100
	}

	if fc.MaxBackups <= 0 {
		fc.MaxBackups = 10
	}

	if fc.MaxAge <= 0 {
		fc.MaxAge = 7
	}

	return fc
}

func toZapLevel(level api.Level) zapcore.Level {
	switch level {
	case api.DebugLevel:
		return zapcore.DebugLevel
	case api.InfoLevel:
		return zapcore.InfoLevel
	case api.WarnLevel:
		return zapcore.WarnLevel
	case api.ErrorLevel:
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}

func (l *Logger) Enabled(level api.Level) bool {
	if l == nil || l.core == nil || l.state == nil {
		return false
	}

	return l.state.level.Enabled(toZapLevel(level))
}

func (l *Logger) SetLevel(level api.Level) error {
	if l == nil || l.state == nil {
		return errors.New("logger is nil")
	}

	if !level.Valid() {
		return fmt.Errorf("invalid log level: %s", level)
	}

	l.state.level.SetLevel(toZapLevel(level))
	return nil
}

// With 返回派生 Logger，共享原始资源和动态等级。
func (l *Logger) With(fields ...api.Field) api.Logger {
	if l == nil || l.core == nil {
		return l
	}

	return &Logger{
		core:  l.core.With(convertFields(fields)...),
		state: l.state,
	}
}

func (l *Logger) write(
	level api.Level,
	msg string,
	fields ...api.Field,
) {
	if !l.Enabled(level) {
		return
	}

	zapLevel := toZapLevel(level)

	ce := l.core.Check(zapLevel, msg)
	if ce == nil {
		return
	}

	ce.Write(convertFields(fields)...)
}

func (l *Logger) Debug(msg string, fields ...api.Field) {
	l.write(api.DebugLevel, msg, fields...)
}

func (l *Logger) Info(msg string, fields ...api.Field) {
	l.write(api.InfoLevel, msg, fields...)
}

func (l *Logger) Warn(msg string, fields ...api.Field) {
	l.write(api.WarnLevel, msg, fields...)
}

func (l *Logger) Error(msg string, fields ...api.Field) {
	l.write(api.ErrorLevel, msg, fields...)
}

func (l *Logger) Debugf(format string, args ...any) {
	if l.Enabled(api.DebugLevel) {
		l.Debug(fmt.Sprintf(format, args...))
	}
}

func (l *Logger) Infof(format string, args ...any) {
	if l.Enabled(api.InfoLevel) {
		l.Info(fmt.Sprintf(format, args...))
	}
}

func (l *Logger) Warnf(format string, args ...any) {
	if l.Enabled(api.WarnLevel) {
		l.Warn(fmt.Sprintf(format, args...))
	}
}

func (l *Logger) Errorf(format string, args ...any) {
	if l.Enabled(api.ErrorLevel) {
		l.Error(fmt.Sprintf(format, args...))
	}
}

func (l *Logger) writeContext(
	level api.Level,
	ctx context.Context,
	msg string,
	fields ...api.Field,
) {
	if !l.Enabled(level) {
		return
	}

	contextFields := api.ContextFields(ctx)

	if len(contextFields) > 0 {
		merged := make([]api.Field, 0, len(contextFields)+len(fields))
		merged = append(merged, contextFields...)
		merged = append(merged, fields...)
		fields = merged
	}

	l.write(level, msg, fields...)
}

func (l *Logger) DebugContext(
	ctx context.Context,
	msg string,
	fields ...api.Field,
) {
	l.writeContext(api.DebugLevel, ctx, msg, fields...)
}

func (l *Logger) InfoContext(
	ctx context.Context,
	msg string,
	fields ...api.Field,
) {
	l.writeContext(api.InfoLevel, ctx, msg, fields...)
}

func (l *Logger) WarnContext(
	ctx context.Context,
	msg string,
	fields ...api.Field,
) {
	l.writeContext(api.WarnLevel, ctx, msg, fields...)
}

func (l *Logger) ErrorContext(
	ctx context.Context,
	msg string,
	fields ...api.Field,
) {
	l.writeContext(api.ErrorLevel, ctx, msg, fields...)
}

func (l *Logger) Sync() error {
	if l == nil || l.core == nil {
		return nil
	}

	return l.core.Sync()
}

// Close 释放根 Logger 持有的文件 writer。
// 只有根 Logger（New 创建）会真正关闭资源；派生 Logger（With / WithCallerSkip）
// 共享同一份资源，其 Close 为无操作，避免误关闭共享文件句柄。
func (l *Logger) Close() error {
	if l == nil || l.state == nil || !l.root {
		return nil
	}

	state := l.state

	state.closeOnce.Do(func() {
		var errs []error

		if err := l.Sync(); err != nil {
			// stdout/stderr 在部分操作系统上可能返回
			// syscall.EINVAL，这不一定代表日志未写入。
			errs = append(errs, err)
		}

		for _, writer := range state.closers {
			if err := writer.Close(); err != nil {
				errs = append(errs, err)
			}
		}

		state.closeErr = errors.Join(errs...)
	})

	return state.closeErr
}

// 防止误删 time import；这里可根据后续时间字段扩展删除。
var _ = time.RFC3339Nano

var _ api.Logger = (*Logger)(nil)

// WithCallerSkip 返回额外跳过 skip 帧 caller 的派生 Logger，
// 供包装层（例如全局门面函数）对齐调用位置。派生 Logger 共享原始资源与动态等级。
func (l *Logger) WithCallerSkip(skip int) api.Logger {
	if l == nil || l.core == nil {
		return l
	}

	return &Logger{
		core: l.core.WithOptions(
			uzap.AddCallerSkip(skip),
		),
		state: l.state,
	}
}
