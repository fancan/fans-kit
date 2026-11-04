# log 模块使用文档

> 一个**全局日志优先、零侵入使用、API 与实现分离**的公用日志模块。

## 1. 概述

`log` 模块提供了一套业务友好、可替换底层实现的日志能力，核心设计目标：

| 目标 | 说明 |
| --- | --- |
| **零侵入使用** | 业务模块无需注入 `Logger`，`import "github.com/fancan/fans-kit/log"` 后直接调用 `log.Info()`、`log.Error()` 等全局函数。 |
| **无数据竞争** | 底层通过 `atomic.Pointer[globalState]` 管理全局 `Logger`，避免全局指针反复赋值产生的数据竞争。 |
| **API 与实现分离** | 业务代码只依赖 `log` 包暴露的 API 与类型，不直接依赖 Zap；底层实现（当前为 Zap + lumberjack）可整体替换。 |
| **结构化日志** | 支持结构化字段（`Field`）、Context 链路字段、文件轮转、动态日志级别。 |

### 分层结构

```
log/                对外门面：类型别名、全局函数、字段构造器（业务只 import 这一层）
 ├── log.go         全局 Info/Error/...、SetLevel/Sync/With
 ├── global.go      全局 Logger 管理（atomic.Pointer）
 └── fallback.go    未设置全局 Logger 时的兜底实现

log/api/            API 抽象层：Logger 接口、Level、Field、Context 字段提取（不依赖任何引擎）
log/zap/            引擎实现层：Zap + lumberjack 文件轮转（业务不直接依赖）
```

## 2. 快速开始

```go
package main

import (
	"github.com/fancan/fans-kit/log"
)

func main() {
	// 1. 创建 Logger 并设置为全局（建议只在应用启动时执行一次）
	logger, err := log.New(log.Config{
		Service:     "order-service",
		Environment: "prod",
		Level:       log.InfoLevel,
		Format:      "json",                 // json / console
		Output:      []string{"stdout", "file"}, // stdout / stderr / file
		Caller:      true,
		File: log.FileConfig{
			Filename:   "/var/log/order/app.log",
			MaxSize:    100,   // 单文件上限，单位 MB
			MaxBackups: 10,    // 保留旧文件数量
			MaxAge:     7,     // 保留天数
			Compress:   true,  // 是否压缩旧文件
			LocalTime:  true,  // 轮转时间是否使用本地时间
		},
	})
	if err != nil {
		panic(err)
	}
	if err := log.SetGlobal(logger); err != nil {
		panic(err)
	}
	defer log.Sync() // 进程退出前刷出缓冲区

	// 2. 业务代码直接使用，无需注入 Logger
	log.Info("service started")
	log.Errorf("load config failed: %v", err)
}
```

> 若不调用 `SetGlobal`，`log.Info` 等函数会落到内置的兜底 Logger（`console` 格式输出到 `stderr`，级别 `info`），保证初始化前也能安全打日志。

## 3. 基本用法

### 3.1 全局日志

```go
log.Debug("debug message")
log.Info("info message", log.String("uid", "u_123"))
log.Warn("warn message")
log.Error("error message", log.Err(err))
```

### 3.2 格式化日志

```go
log.Infof("user %s login, code=%d", uid, code)
log.Errorf("request failed: %v", err)
```

### 3.3 结构化字段

```go
log.Info("order paid",
	log.String("order_id", "o_123"),
	log.Int("amount_cents", 100),
	log.Int64("user_id", 123456789),
	log.Uint64("version", 42),
	log.Float64("rate", 0.98),
	log.Bool("is_vip", true),
	log.Duration("cost", time.Since(start)),
	log.Time("paid_at", time.Now()),
	log.Err(err),                       // key 固定为 "error"
	log.NamedError("cause", innerErr),  // 自定义 key
	log.Any("payload", someStruct),     // 任意类型（走反射）
)
```

字段构造器一览：

| 构造器 | 签名 | 说明 |
| --- | --- | --- |
| `String` | `(key, value string)` | 字符串 |
| `Int` | `(key string, value int)` | 整型 |
| `Int64` | `(key string, value int64)` | 64 位整型 |
| `Uint64` | `(key string, value uint64)` | 无符号 64 位整型 |
| `Float64` | `(key string, value float64)` | 浮点 |
| `Bool` | `(key string, value bool)` | 布尔 |
| `Duration` | `(key string, value time.Duration)` | 时长 |
| `Time` | `(key string, value time.Time)` | 时间 |
| `Err` | `(err error)` | 错误，key 为 `"error"` |
| `NamedError` | `(key string, err error)` | 自定义 key 的错误 |
| `Any` | `(key string, value any)` | 任意类型 |

## 4. Context 链路字段

`*Context` 系列方法会自动从 `context.Context` 中提取标准链路字段并附加到日志：

```go
import (
	"context"
	"github.com/fancan/fans-kit/log"
)

func handle(ctx context.Context) {
	// 上游注入链路 ID（key 为类型化常量，见下表）
	ctx = context.WithValue(ctx, log.TraceIDKey, "trace-abc")
	ctx = context.WithValue(ctx, log.ReqIDKey, "req-123")

	log.InfoContext(ctx, "request start")
	log.ErrorContext(ctx, "db error", log.Err(err))
}
```

标准 Context Key 与输出的字段名对应关系：

| 常量 | 输出字段名 |
| --- | --- |
| `log.LogIDKey` | `logid` |
| `log.ReqIDKey` | `reqid` |
| `log.RefIDKey` | `refid` |
| `log.TraceIDKey` | `traceid` |
| `log.SpanIDKey` | `spanid` |

> 说明：这些 key 是**类型化常量**（内部为整型，非字符串），好处是：
> 1. 天然避免与业务自定义字符串 key 碰撞；
> 2. `ctx.Value` 无需装箱，空 Context 提取零分配。
>
> 字段值通过 `ctx.Value(key)` 提取；若 Context 中不存在对应 key，则不输出该字段，值以 `Any` 类型（反射）编码。

### 4.1 Gin 等字符串 key 框架

Gin 的 `c.Set` / `c.Get` 基于字符串 key，因此另提供一组字符串常量：

| 常量 | Key 值 |
| --- | --- |
| `log.LogIDKeyString` | `"LOGID"` |
| `log.ReqIDKeyString` | `"REQID"` |
| `log.RefIDKeyString` | `"REFID"` |
| `log.TraceIDKeyString` | `"TRACEID"` |
| `log.SpanIDKeyString` | `"SPANID"` |

配合 `ContextGetterFields` 从 Gin Context 提取字段：

```go
func handler(c *gin.Context) {
	c.Set(log.TraceIDKeyString, "trace-abc")

	log.Info("request start", log.ContextGetterFields(c)...)
}
```

也可以手动提取标准 Context 字段（`ContextFields`）后自行拼接到普通日志方法：

```go
log.Info("request start", log.ContextFields(ctx)...)
```

## 5. 派生 Logger（固定字段）

`With` 返回一个携带固定字段的派生 `Logger`，适合给某个模块/租户统一打标签：

```go
payLogger := log.With(log.String("module", "pay"))
payLogger.Info("payment start", log.String("order_id", "o_1"))
// 等价于每次 Info 都附带 module=pay
```

## 6. 动态日志级别

```go
// 运行时动态调整全局日志级别（线程安全）
if err := log.SetLevel(log.DebugLevel); err != nil {
	// ...
}
```

级别常量：

| 常量 | 值 | 说明 |
| --- | --- | --- |
| `log.DebugLevel` | 0 | 最详细 |
| `log.InfoLevel` | 1 | 默认推荐 |
| `log.WarnLevel` | 2 | 警告 |
| `log.ErrorLevel` | 3 | 错误 |

也可以解析字符串级别（`log.ParseLevel`，支持 `debug/info/warn/warning/error`）。

## 7. 配置说明

### 7.1 `Config`

| 字段 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `Service` | `string` | `"unknown"` | 服务名，作为固定字段 `service` 输出 |
| `Environment` | `string` | `""` | 环境名，非空时作为固定字段 `environment` 输出 |
| `Level` | `Level` | 必填 | 初始日志级别（非法值返回错误） |
| `Format` | `string` | `"json"` | 输出格式：`json` / `console` |
| `Output` | `[]string` | `["stdout"]` | 输出目标：`stdout` / `stderr` / `file` |
| `Caller` | `bool` | `false` | 是否输出调用位置（`caller` 字段） |
| `Stacktrace` | `bool` | `false` | 是否在 Error 级别输出堆栈 |
| `SeparateLevelFiles` | `bool` | `false` | 是否按级别拆分日志文件（仅 `file` 输出生效） |
| `File` | `FileConfig` | — | 文件轮转配置（`Output` 含 `file` 时必填 `Filename`） |

### 7.2 `FileConfig`

| 字段 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `Filename` | `string` | — | 日志文件路径 |
| `MaxSize` | `int` | `100` | 单文件最大 MB，超过后轮转 |
| `MaxBackups` | `int` | `10` | 保留的旧文件数量 |
| `MaxAge` | `int` | `7` | 旧文件保留天数 |
| `Compress` | `bool` | `false` | 旧文件是否 gzip 压缩 |
| `LocalTime` | `bool` | `false` | 轮转文件名时间戳是否使用本地时间 |

### 7.3 按级别拆分文件

```go
log.New(log.Config{
	Level:              log.DebugLevel,
	Output:             []string{"file"},
	SeparateLevelFiles: true,
	File: log.FileConfig{
		Filename: "/var/log/app/app.log",
	},
})
```

拆分后生成三个文件：

- `app.log.info` —— Debug / Info 级别
- `app.log.warning` —— Warn 级别
- `app.log.error` —— Error 级别

## 8. 自定义 / 替换底层实现

业务代码只依赖 `log` 包的 API，底层引擎可整体替换。实现 `api.Logger` 接口即可接入：

```go
type Logger interface {
	Enabled(level Level) bool
	Debug/Info/Warn/Error(msg string, fields ...Field)
	Debugf/Infof/Warnf/Errorf(format string, args ...any)
	DebugContext/InfoContext/WarnContext/ErrorContext(ctx context.Context, msg string, fields ...Field)
	With(fields ...Field) Logger
	WithCallerSkip(skip int) Logger
	SetLevel(level Level) error
	Sync() error
	Close() error
}
```

```go
// 使用自研/其他引擎
log.SetGlobal(myLogger)
```

`log.New` 目前返回 `*zap.Logger`（实现了 `api.Logger`），也可以通过类型断言获取底层扩展能力。

## 9. 性能与基准测试

模块内置了基准测试，运行方式：

```bash
# 门面层（全局函数）基准
go test ./log/ -run '^$' -bench . -benchmem

# Context 字段提取基准
go test ./log/api/ -run '^$' -bench . -benchmem

# api.Field -> zap.Field 转换基准
go test ./log/zap/ -run '^$' -bench . -benchmem
```

参考数据（Windows / amd64 / Intel Core Ultra 7 155H）：

| 基准 | 结果 | 分配 |
| --- | --- | --- |
| `Info`（级别被过滤，无字段） | ~5.4 ns/op | 0 allocs |
| `Info`（级别被过滤，2 个字段） | ~62 ns/op | 1 alloc（128 B） |
| `Infof`（级别被过滤） | ~25 ns/op | 1 alloc（16 B） |
| `ContextFields`（空 Context） | ~20 ns/op | **0 allocs** |
| `ContextFields`（2 个命中字段） | ~156 ns/op | 1 alloc（320 B） |
| `convertFields`（6 个字段） | ~186 ns/op | 1 alloc（384 B） |

要点：

- **丢弃路径极快**：级别被过滤时只有一次 `atomic.Pointer` 加载 + 接口分发 + `Enabled` 级别检查，无堆分配。
- **带字段的开销主要来自变参切片分配**：`fields ...Field` 由调用方构造，即使日志被丢弃也无法避免；`Field` 已压缩到 64 字节，2 个字段仅 128 B。
- **Context 提取零分配**：链路 key 采用类型化整型，空 Context 不再产生字符串装箱分配。
- **命中路径额外开销**：`convertFields` 每次带字段日志引入一次 `[]zap.Field` 堆分配，这是相对原生 zap 的主要额外成本；对极端高频场景建议沉淀固定字段到派生 Logger 并减少每行字段数量。

## 10. 注意事项与已知限制

1. **`SetGlobal` 建议启动时调用一次**：重复调用是并发安全的，且会关闭被替换掉的旧 Logger 及其文件句柄；但与运行时替换 + 并发打日志天然存在竞态，不建议频繁切换。
2. **`With` / `WithCallerSkip` 派生 Logger 共享状态**：其 `Close` 为无操作，只有根 Logger（`log.New` 创建）会真正关闭文件 writer。
3. **字段类型覆盖有限**：缺少 `Uint`、`Int8/16/32`、`Float32`、`[]byte`、嵌套对象等类型，必要时用 `Any` 兜底（走反射，代价最高）。
4. **`Time` 字段有一次性装箱**：`log.Time` 内部将 `time.Time` 存入 `interface{}`，构造时会产生一次堆分配；对极高频日志可考虑改用 `Any` 传字符串时间戳。
