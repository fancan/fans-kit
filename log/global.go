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
var errGlobalAlreadySet = errors.New("log: logger is seted")

// SetGlobal 设置全局Logger。
// 注意：只在应用启动时调用一次，在logger已经New好后调用一次
func SetGlobal(logger Logger) error {
	if logger == nil {
		return errNilLogger
	}

	newGlobalState := &globalState{
		logger: logger,
		facade: logger.WithCallerSkip(1),
	}

	if !global.CompareAndSwap(nil, newGlobalState) {
		return errGlobalAlreadySet
	}

	return nil
}

// getGlobal 获取当前全局Logger（面向直接方法调用）。
func getGlobal() Logger {
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
