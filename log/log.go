package log

import (
	"context"

	"github.com/fancan/fans-kit/log/api"
	zapbackend "github.com/fancan/fans-kit/log/zap"
)

// 类型别名，业务层不需要 import api。
type Level = api.Level
type Field = api.Field
type ContextGetter = api.ContextGetter

const (
	DebugLevel = api.DebugLevel
	InfoLevel  = api.InfoLevel
	WarnLevel  = api.WarnLevel
	ErrorLevel = api.ErrorLevel
)

const (
	LogIDKey   = api.LogIDKey
	ReqIDKey   = api.ReqIDKey
	RefIDKey   = api.RefIDKey
	TraceIDKey = api.TraceIDKey
	SpanIDKey  = api.SpanIDKey
)

// 供 Gin 等基于字符串 key 的框架使用的 key 常量。
const (
	LogIDKeyString   = api.LogIDKeyString
	ReqIDKeyString   = api.ReqIDKeyString
	RefIDKeyString   = api.RefIDKeyString
	TraceIDKeyString = api.TraceIDKeyString
	SpanIDKeyString  = api.SpanIDKeyString
)

// Field 构造器。
var (
	String     = api.String
	Int        = api.Int
	Int64      = api.Int64
	Uint64     = api.Uint64
	Float64    = api.Float64
	Bool       = api.Bool
	Duration   = api.Duration
	Time       = api.Time
	Err        = api.Err
	NamedError = api.NamedError
	Any        = api.Any
)

// Context 字段提取（供手动附加链路字段使用）。
var (
	ContextFields       = api.ContextFields
	ContextGetterFields = api.ContextGetterFields
)

// FileConfig 文件轮转配置。
type FileConfig = zapbackend.FileConfig

// Config 日志配置。
type Config = zapbackend.Config

// New 创建一个独立Logger。
// 通常用于应用初始化，随后通过SetGlobal设置全局Logger。
func New(cfg Config) (Logger, error) {
	return zapbackend.New(cfg)
}

// Debug 输出 Debug 日志。
func Debug(msg string, fields ...Field) {
	facade().Debug(msg, fields...)
}

// Info 输出 Info 日志。
func Info(msg string, fields ...Field) {
	facade().Info(msg, fields...)
}

// Warn 输出 Warn 日志。
func Warn(msg string, fields ...Field) {
	facade().Warn(msg, fields...)
}

// Error 输出 Error 日志。
func Error(msg string, fields ...Field) {
	facade().Error(msg, fields...)
}

// Debugf 输出格式化 Debug 日志。
func Debugf(format string, args ...any) {
	facade().Debugf(format, args...)
}

// Infof 输出格式化 Info 日志。
func Infof(format string, args ...any) {
	facade().Infof(format, args...)
}

// Warnf 输出格式化 Warn 日志。
func Warnf(format string, args ...any) {
	facade().Warnf(format, args...)
}

// Errorf 输出格式化 Error 日志。
func Errorf(format string, args ...any) {
	facade().Errorf(format, args...)
}

// Context 版本。
func DebugContext(ctx context.Context, msg string, fields ...Field) {
	facade().DebugContext(ctx, msg, fields...)
}

func InfoContext(ctx context.Context, msg string, fields ...Field) {
	facade().InfoContext(ctx, msg, fields...)
}

func WarnContext(ctx context.Context, msg string, fields ...Field) {
	facade().WarnContext(ctx, msg, fields...)
}

func ErrorContext(ctx context.Context, msg string, fields ...Field) {
	facade().ErrorContext(ctx, msg, fields...)
}

// 动态修改全局日志级别。
func SetLevel(level Level) error {
	return getGlobal().SetLevel(level)
}

// 同步全局日志缓冲区。不释放底层资源
func Sync() error {
	return getGlobal().Sync()
}

// Close 刷新日志并关闭全局Logger，释放底层资源。
func Close() error {
	return getGlobal().Close()
}

// With 创建带有固定字段的全局日志包装器。
func With(fields ...Field) Logger {
	return getGlobal().With(fields...)
}
