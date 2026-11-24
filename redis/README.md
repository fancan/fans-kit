# Redis —— 零侵入的公共 Redis 基础模块

对 `go-redis/v9` 的薄封装：**一次初始化，全局使用**。业务代码 import 后直接调用
`redis.Get` / `redis.Set` 等包级函数，无需注入 client、无需关心后端是单机/哨兵/集群。

## 特性

| 能力 | 说明 |
| --- | --- |
| 零侵入 | `redis.Init(cfg)` 后直接 `redis.Get/Set/...`，不持有任何实例 |
| 多实例 | 具名实例（缓存/队列/会话各用一套），`redis.Named("cache")` |
| 三种部署 | 单机 / 哨兵 / 集群统一走 `UniversalClient`，业务无感 |
| Key 前缀 | 按实例配置 `Prefix`，自动给所有 key/频道加前缀，便于隔离与治理 |
| 内置监控 | 命令数/错误数/延迟直方图/在途数 + 连接池指标，一行挂载 Prometheus |
| 有界基数 | 命令名收敛到固定集合，未知命令归入 `other`，指标不会爆炸 |
| 开箱即用 | 连接池/超时/重试全部有合理默认值，只需填 `Addrs` |

## 目录结构

```text
redis/
├── redis.go                 # 包级命令函数（零侵入入口，全部走默认实例）
├── manager.go               # 内部 manager：实例注册/查找/关闭 + 包级生命周期函数
├── instance.go              # Instance：命令方法（自动加 key 前缀）
├── observe.go               # 内部 metrics（go-redis Hook 实现）+ MetricsHandler
├── client.go                # 按模式构造 go-redis 客户端 + ping 探活
├── config.go                # Config 归一化与校验、默认值
├── errors.go                # 错误与 Nil
├── redis_test.go            # 单元测试（无需真实 Redis）
├── redis_bench_test.go      # 基准（部分无需真实 Redis）
└── redis_integration_test.go# 集成测试（需 REDIS_ADDR）
```

## 快速接入

### 1. 初始化（进程启动处，一次）

```go
import (
    "context"
    redis "github.com/fancan/fans-kit/redis"
)

func main() {
    // 最简：只需地址，其余走默认值
    if err := redis.Init(redis.Config{
        Addrs: []string{"127.0.0.1:6379"},
    }); err != nil {
        log.Fatal(err)
    }
    defer redis.Close()

    // ... 业务 ...
}
```

需要控制建连超时用 `redis.InitContext(ctx, cfg)`。

### 2. 直接使用（零侵入）

```go
ctx := context.Background()

// String
redis.Set(ctx, "user:1:name", "alice", time.Hour)
name, err := redis.Get(ctx, "user:1:name")
if errors.Is(err, redis.Nil) { /* key 不存在 */ }

// 计数器
redis.Incr(ctx, "api:calls")

// Hash
redis.HSet(ctx, "user:1", "name", "alice", "age", 18)
all, _ := redis.HGetAll(ctx, "user:1")

// List / Set / ZSet
redis.LPush(ctx, "queue:jobs", "job-1")
redis.SAdd(ctx, "online:users", "u1")
redis.ZAdd(ctx, "rank", goredis.Z{Score: 99, Member: "alice"})

// JSON
redis.SetJSON(ctx, "pool:0xabc", poolSnapshot, 30*time.Second)
var p PoolSnapshot
redis.GetJSON(ctx, "pool:0xabc", &p)

// Lua / PubSub
redis.Eval(ctx, script, []string{"lock:1"}, token)
redis.Publish(ctx, "events:pool", payload)
```

`redis.GetJSON` 在 key 不存在时返回 `redis.Nil`；JSON 解析失败返回原始解析错误。

## 多实例

不同业务用不同实例（不同 Redis 集群 / DB / 前缀），互不干扰：

```go
// 默认实例
redis.Init(redis.Config{Addrs: []string{"redis-cache:6379"}, Prefix: "cache:"})

// 具名实例
redis.InitNamed("queue", redis.Config{
    Addrs:  []string{"redis-queue:6379"},
    Prefix: "q:",
})

// 通过包级函数只能操作「默认实例」；操作具名实例需要拿句柄
queue, err := redis.Named("queue")
queue.LPush(ctx, "jobs", "job-1")   // 实际写入 q:jobs
```

默认实例的归属规则：

- `Init` / `InitNamedAsDefault` 会**强制**把该实例设为默认；
- `InitNamed` 在**已有默认实例**时不会抢占；
- 若当前没有默认实例（例如只调用了一次 `InitNamed("queue")`），该实例会**自动成为默认**，
  保证 `redis.Get` 等包级函数始终可用。

## Key 前缀与 Key()

配置 `Prefix` 后，`Instance` 的所有命令方法都会自动加前缀；`redis.Get` 等包级函数同理。

```go
inst, _ := redis.Default()
inst.Set(ctx, "user:1", "v", 0)   // 实际 key = "<prefix>user:1"
inst.Key("user:1")               // 手动拿到完整 key（用于自定义逻辑）
```

> 前缀是纯字符串拼接（无分隔符），请自行在 `Prefix` 末尾带上 `:`。

## 高级用法：Pipeline / 事务

`Instance` 提供的是常用命令的便捷方法，未覆盖 Pipeline、Watch/Tx 等能力。
需要时用 `Client()` 拿到底层客户端（**不会自动加前缀**，用 `Key()` 自行拼接）：

```go
inst, _ := redis.Default()
c := inst.Client()

// Pipeline
pipe := c.Pipeline()
pipe.Set(ctx, inst.Key("k1"), "v1", time.Minute)
pipe.Set(ctx, inst.Key("k2"), "v2", time.Minute)
_, err := pipe.Exec(ctx)
```

也可以绕过本模块，直接用 go-redis 的 `redis.NewClient`——本模块不阻止，但那样就失去了
统一的前缀/监控/生命周期管理。

## 监控（Prometheus）

```go
mux := http.NewServeMux()
mux.Handle("/metrics", redis.MetricsHandler())
go http.ListenAndServe(":9090", mux)
```

导出指标：

| 指标 | 类型 | 说明 |
| --- | --- | --- |
| `fans_redis_commands_total{instance,command}` | counter | 命令执行次数 |
| `fans_redis_command_errors_total{instance,command}` | counter | 命令错误次数（`Nil` 不计） |
| `fans_redis_command_duration_seconds` | histogram | 命令延迟（固定桶 1ms~5s） |
| `fans_redis_inflight_commands{instance}` | gauge | 在途命令数 |
| `fans_redis_pool_connections{instance,state}` | gauge | 连接数（total/idle） |
| `fans_redis_pool_timeouts_total{instance}` | counter | 从池中取连接超时次数 |
| `fans_redis_pool_hits_total` / `misses_total` | counter | 连接复用命中/未命中 |

不需要内置监控时设 `DisableMetrics: true`，可省掉每条命令的 Hook 开销。

## 配置

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `Mode` | `standalone` | `standalone` / `failover` / `cluster` |
| `Addrs` | 必填 | 地址列表；单机必须恰好 1 个 |
| `Username` / `Password` | 空 | ACL 认证 |
| `DB` | 0 | 库号；集群模式必须为 0 |
| `MasterName` | 空 | 哨兵模式必填 |
| `SentinelUsername` / `SentinelPassword` | 空 | 哨兵自身认证 |
| `PoolSize` | 10 | 每节点连接池大小 |
| `MinIdleConns` | 2 | 最小空闲连接 |
| `MaxIdleConns` / `MaxActiveConns` | 0 | 0 表示不限制 |
| `DialTimeout` | 5s | 建连超时 |
| `ReadTimeout` / `WriteTimeout` | 3s | 读写超时 |
| `PoolTimeout` | 4s | 等可用连接超时 |
| `ConnMaxIdleTime` | 5m | 连接最大空闲时间 |
| `ConnMaxLifetime` | 30m | 连接最大生命周期 |
| `MaxRetries` | 3 | 重试次数；`-1` 表示禁用 |
| `MinRetryBackoff` / `MaxRetryBackoff` | 8ms / 512ms | 重试退避区间 |
| `ClientName` | 空 | 连接名（服务端 `CLIENT LIST` 可见） |
| `TLSConfig` | nil | TLS 配置（内部会 Clone） |
| `Prefix` | 空 | Key 前缀 |
| `ReadOnly` / `RouteByLatency` / `RouteRandomly` | false | 仅集群模式的路由策略 |
| `DisableMetrics` | false | 关闭内置监控 |
| `Hooks` | nil | 自定义 go-redis Hook |

三种模式的最小配置：

```go
// 单机
redis.Config{Addrs: []string{"127.0.0.1:6379"}}

// 哨兵
redis.Config{Mode: redis.ModeFailover, Addrs: []string{"sentinel-1:26379", "sentinel-2:26379"}, MasterName: "mymaster"}

// 集群
redis.Config{Mode: redis.ModeCluster, Addrs: []string{"node-1:7000", "node-2:7000"}}
```

## 设计说明：为什么这些标识符是「包外不可见」的

本模块的定位是**薄封装 + 全局零侵入**。据此，我们把「内部管道」全部收敛为包内私有，
只暴露业务真正需要的东西。

| 标识符 | 现状 | 理由 |
| --- | --- | --- |
| `manager` / `newManager` | **私有** | 全局单例是唯一生命周期入口。导出 `Manager` 会让业务各自 `NewManager`，出现多份实例表、重复 Close、与包级函数状态不一致 |
| `manager.init/initNamed/defaultInstance/named/list/close` | **私有**（方法） | 同上：注册与关闭只应由包级函数驱动 |
| `metrics` / `newMetrics` | **私有** | 它只是 go-redis Hook 的内部实现。导出会让「如何观测」变成第二套公共契约；业务需要自定义观测时应实现 `goredis.Hook` 走 `Config.Hooks` |
| `metrics.snapshot/metricsSnapshot/commandSnapshot` | **私有** | 是 `MetricsHandler` 的中间产物，不是稳定 API |
| `Instance.Metrics()` | **已移除** | 泄漏内部指标类型；对外统一走 `MetricsHandler()` |
| `Instance.PoolStats()` | **已移除** | 与内部 `poolSnapshot()` 完全重复，且直接暴露 `goredis.PoolStats` |
| `Instance.poolSnapshot()` / `poolSnapshot` | **私有** | 仅供指标导出使用 |
| `Instance` 及其命令方法 | **保持导出** | `redis.Named()` 必须返回可用句柄，这是业务操作具名实例的唯一途径 |
| `Instance.Client()` / `Key()` | **保持导出** | 逃生口：Pipeline/事务/自定义 Hook 需要；`Key` 用于自行拼前缀 |
| `MetricsHandler()` | **保持导出** | 对外唯一的监控入口 |
| `Config` / `Mode` / `Err*` / `Nil` | **保持导出** | 配置与错误判定是公共契约 |

一句话原则：**「业务要调用的」导出，「模块自己怎么跑的」不导出。**

### 仍存在的限制（使用需注意）

1. **Pub/Sub 频道名也会被加前缀**：`Publish("events:pool")` 实际发到 `<prefix>events:pool`。
   跨服务（他方未使用本模块）通信时容易对不上，此时请用 `instance.Client()` 手动发布。
2. **前缀是裸拼接**：`Prefix` 末尾需自行带 `:`；拼接发生在**每条命令**，`Key` 实测 25 ns / 0 alloc。
3. **集群模式下的 Hash Tag 会被前缀破坏**：若 key 形如 `{user}:1`，加前缀后变为
   `cache:{user}:1`，hash tag 仍有效；但若前缀自身含 `{}` 会改变分片，请勿在前缀中使用花括号。
4. **`Instance` 命令方法未覆盖全部 Redis 命令**：Pipeline/事务/Watch/Scan 迭代等需走 `Client()`。
5. **重复 `Init` 返回 `ErrAlreadyExists`**（而不是专门的重入错误）：语义上可接受，但注意
   它是「实例同名」而非「全局已初始化」，如需重建请先 `Close()`。
6. **`Close()` 不做在途命令等待**：关闭瞬间的在途命令会以连接关闭错误结束。优雅停机请先停止
   业务流量再 `Close()`。
7. **`Subscribe` 返回的 `*goredis.PubSub` 由调用方负责 `Close()`**，本模块不跟踪其生命周期。

## 性能分析

### 实测数据（Windows / amd64 / Go 1.25，`-benchmem`）

不依赖真实 Redis 的固定开销：

| 基准 | 结果 | 分配 | 说明 |
| --- | --- | --- | --- |
| `MetricsObserve` | **81 ns/op** | **0 B / 0 allocs** | 每条命令的观测开销（修复前 202 ns / 160 B / 3 allocs） |
| `MetricsObserveUnknownCommand` | 146 ns/op | 16 B / 1 alloc | 未知命令多一次 `strings.ToLower` 分配（命令名含大写才触发） |
| `InstanceKey` | 25 ns/op | 0 B / 0 allocs | key 前缀拼接 |
| `MetricsSnapshot` | 958 ns/op | 1488 B / 11 allocs | Prometheus 抓取时构件快照，**非热路径** |

需要真实 Redis 的端到端基准（设 `REDIS_ADDR` 后运行）：`BenchmarkRedisSet/Get/MGet10/
Pipeline10/ConcurrentGet/JSON`。

```bash
REDIS_ADDR=127.0.0.1:6379 go test ./redis/ -run '^$' -bench . -benchmem
```

### 结论与建议

- **固定开销可忽略**：每条命令除网络往返外，本模块只增加「前缀拼接 25 ns + 观测 81 ns」。
  真实场景单次 RTT 通常 100 µs~2 ms，本模块占比 < 0.1%。
- **监控几乎免费**：修复后观测路径零分配，高 QPS 下不会给 GC 增加压力。若确实不需要指标，
  设 `DisableMetrics: true` 可再省 81 ns/op 与两个 Hook 调用。
- **基数有界**：命令名用固定白名单收敛，未知命令统一 `other`，指标组合数上界 ≈
  `实例数 × 命令数(≈60)`，不会因业务自定义命令导致 Prometheus 内存膨胀。
- **多实例查找是一次 RWMutex 读锁**：`Default()`/`Named()` 均为 O(1) 读锁，成本 ~20 ns，
  相比网络往返可忽略。
- **`sync.Map` 选型正确**：命令指标是「读多写一次」的典型场景，`Load` 快路径无锁。

## 测试

```bash
# 单元测试（无需 Redis）
go test ./redis/

# 集成 + 基准（需真实 Redis）
REDIS_ADDR=127.0.0.1:6379 go test ./redis/ -run TestRedisIntegration -v
REDIS_ADDR=127.0.0.1:6379 go test ./redis/ -run '^$' -bench . -benchmem
```

## 错误判定

```go
if errors.Is(err, redis.Nil) {
    // key 不存在（不是真正的错误）
}

if errors.Is(err, redis.ErrNotInitialized) {
    // 忘记 Init，或已经 Close
}
```

| 错误 | 含义 |
| --- | --- |
| `Nil` | go-redis 的 key 不存在哨兵，等价于 `goredis.Nil` |
| `ErrNotInitialized` | 尚未初始化 / 已关闭 |
| `ErrAlreadyExists` | 同名实例已注册 |
| `ErrInstanceNotFound` | `Named()` 查不到该名称 |
| `ErrInvalidConfig` | 配置非法（地址为空、集群 DB≠0、池参数矛盾等） |
| `ErrManagerClosedDuringInit` | 建连过程中被 `Close()`，本次初始化作废 |
