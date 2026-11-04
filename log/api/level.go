package api

// log组件的对外api层封装
// 定义log组件的对外可使用级别

import (
	"fmt"
	"strings"
)

type Level int8

const (
	DebugLevel Level = iota
	InfoLevel
	WarnLevel
	ErrorLevel
)

// 日志级别转换 string->Level
func (l Level) String() string {
	switch l {
	case DebugLevel:
		return "debug"
	case InfoLevel:
		return "info"
	case WarnLevel:
		return "warn"
	case ErrorLevel:
		return "error"
	default:
		return "unknown"
	}
}

// 日志级别转换 Level->string
// 解析失败时返回 InfoLevel 作为安全默认值，调用方应优先检查返回的 error。
func ParseLevel(s string) (Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return DebugLevel, nil
	case "info":
		return InfoLevel, nil
	case "warn", "warning":
		return WarnLevel, nil
	case "error":
		return ErrorLevel, nil
	default:
		return InfoLevel, fmt.Errorf("invalid log level: %q", s)
	}
}

// 日志级别合法性校验
func (l Level) Valid() bool {
	return l >= DebugLevel && l <= ErrorLevel
}
