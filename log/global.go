package log

import (
	"errors"
	"sync/atomic"

	"github.com/fancan/fans-kit/log/api"
)

type Logger = api.Logger

type globalState struct {
	// logger面向直接方法调用（Global().Info / logger.Info），caller深度以此为基准。
	logger Logger

	// facade面向全局门面函数（log.Info / log.Error 等），
	// 在logger基础上额外跳过门面函数自身一帧，使caller指向业务调用点。
	facade Logger
}

// 使用atomic.Pointer指针控制全局log
var global atomic.Pointer[globalState]

var errNilLogger = errors.New("log: logger is nil")

// SetGlobal 设置全局Logger。
// 建议只在应用启动时调用一次；重复调用是并发安全的，并会关闭被替换的旧Logger。
func SetGlobal(logger Logger) error {
	if logger == nil {
		return errNilLogger
	}

	next := &globalState{
		logger: logger,
		facade: logger.WithCallerSkip(1),
	}

	old := global.Swap(next)

	// 关闭被替换掉的旧Logger，避免文件句柄泄漏。
	// 排除回退Logger（由包自身持有）以及同一Logger的重复设置。
	if old != nil && old.logger != logger && old.logger != fallbackLogger {
		_ = old.logger.Close()
	}

	return nil
}

// Global 获取当前全局Logger（面向直接方法调用）。
func Global() Logger {
	state := global.Load()
	if state == nil {
		return fallbackLogger
	}

	return state.logger
}

// facade 获取面向全局门面函数的Logger（caller深度已对齐）。
func facade() Logger {
	state := global.Load()
	if state == nil {
		return fallbackFacade
	}

	return state.facade
}
