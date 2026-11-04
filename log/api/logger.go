package api

// log组件的对外api层封装
// 定义log组件的对外可使用接口

import "context"

// 这组logger接口有zap实现，未来切换日志引擎可以由别的日志引擎实现
type Logger interface {
	// 日志等级
	Enabled(level Level) bool

	// 对外接口
	Debug(msg string, fields ...Field)
	Info(msg string, fields ...Field)
	Warn(msg string, fields ...Field)
	Error(msg string, fields ...Field)

	// 格式化日志
	Debugf(format string, args ...any)
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)

	// Context 日志
	DebugContext(ctx context.Context, msg string, fields ...Field)
	InfoContext(ctx context.Context, msg string, fields ...Field)
	WarnContext(ctx context.Context, msg string, fields ...Field)
	ErrorContext(ctx context.Context, msg string, fields ...Field)

	// 派生Logger
	With(fields ...Field) Logger

	// 派生Logger，额外跳过skip帧caller（用于包装层对齐调用位置）
	WithCallerSkip(skip int) Logger

	// 动态修改等级
	SetLevel(level Level) error

	// 生命周期
	Sync() error
	Close() error
}
