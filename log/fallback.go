package log

import (
	"fmt"

	"github.com/fancan/fans-kit/log/api"
	"github.com/fancan/fans-kit/log/zap"
)

// 兜底logger
var fallbackLogger api.Logger

// 兜底门面logger（caller 深度对齐门面函数）
var fallbackFacade api.Logger

func init() {
	logger, err := zap.New(zap.Config{
		Service: "default",
		Level:   api.InfoLevel,
		Format:  "console",
		Output:  []string{"stderr"},
		Caller:  true,
	})

	if err != nil {
		panic(fmt.Errorf("initialize fallback logger: %w", err))
	}

	fallbackLogger = logger
	fallbackFacade = logger.WithCallerSkip(1)
}
