package zap

// 具体的日志引擎层 zap
// zap的Backend配置

import "github.com/fancan/fans-kit/log/api"

// zap文件配置
type FileConfig struct {
	Filename string

	MaxSize    int
	MaxBackups int
	MaxAge     int

	Compress  bool
	LocalTime bool
}

type Config struct {
	Service     string
	Environment string

	Level api.Level

	// 输出模式 json / console
	Format string

	//输出地方  stdout / stderr / file
	Output []string

	// 是否输出caller
	Caller bool

	// 是否在ERROR级别记录堆栈
	Stacktrace bool

	// 是否分级文件
	SeparateLevelFiles bool

	File FileConfig
}
