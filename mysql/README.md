# MySQL —— 主从读写分离的公共数据库模块

基于 **GORM + 官方 DBResolver 插件 + database/sql** 的轻量级 MySQL 封装。提供**全局零侵入 API**、
**主从读写分离**、**健康检测与自动摘除**、**写后读强一致**、**连接池动态调整**与**指标统计**。

## 特性一览

| 能力 | 说明 |
| --- | --- |
| 零侵入全局 API | `mysqlkit.Init()` 后直接用 `mysqlkit.DB()`，无需注入实例 |
| 读写分离 | 写自动走主库，读自动在健康从库间轮询（DBResolver 插件） |
| 健康检测 | 后台并发 Ping 所有节点，连续失败自动摘除、连续成功自动恢复 |
| 强一致读 | `WithStrongConsistency(ctx)` 让当前请求的读也走主库，规避主从延迟 |
| 事务 | `Transaction(fn)` 自动在主库开启事务，回调内必须用 `tx` |
| 连接池治理 | 运行期 `UpdatePoolConfig()` 动态调整，无需重连 |
| 可观测 | `Stats()` 连接池指标、`Health()` 节点健康快照 |

## 目录结构

```text
mysql/
├── mysql.go                  # 对外门面：类型别名 + 全局 API（Init/DB/Master/Transaction/...）
├── api.go                    # 全局单例管理 + 内部路由转发
├── config/
│   └── config.go             # Config/NodeConfig/PoolConfig/HealthConfig + SetDefaults/Validate
├── manager/
│   ├── manager.go            # 连接池构建、DBResolver 装配、生命周期、动态池配置
│   ├── node.go               # 节点健康状态机（阈值切换）
│   ├── policy.go             # 自定义 Resolver Policy：健康从库轮询
│   ├── callbacks.go          # gorm callback：上下文强一致 + 连接类错误上报
│   ├── consistency.go        # WithPrimary / forcePrimary 上下文标记
│   └── stats.go              # 连接池统计
├── model/
│   ├── base.go               # BaseModel：在表模型上直接拿 DB/Master/Read/Transaction
│   └── example_models.go     # PoolRecord 等示例模型与常用查询
└── example/
    └── main.go               # 完整接入示例
```

## 快速接入

### 1. 初始化（进程启动处，只需一次）

```go
import (
    "context"
    "time"

    mysqlkit "github.com/fancan/fans-kit/mysql"
)

func main() {
    err := mysqlkit.Init(mysqlkit.Config{
        Master: mysqlkit.NodeConfig{
            DSN: "user:password@tcp(127.0.0.1:3306)/dex?charset=utf8mb4&parseTime=True&loc=Local",
        },
        Replicas: []mysqlkit.NodeConfig{
            {Name: "read-1", DSN: "user:password@tcp(127.0.0.1:3307)/dex?charset=utf8mb4&parseTime=True&loc=Local"},
            {Name: "read-2", DSN: "user:password@tcp(127.0.0.1:3308)/dex?charset=utf8mb4&parseTime=True&loc=Local"},
        },
        Pool: mysqlkit.PoolConfig{
            MaxIdleConns:    10,
            MaxOpenConns:    100,
            ConnMaxLifetime: time.Hour,
            ConnMaxIdleTime: 10 * time.Minute,
        },
        Health: mysqlkit.HealthConfig{
            Enabled:          true,
            Interval:         5 * time.Second,
            ProbeTimeout:     2 * time.Second,
            FailureThreshold: 2,
            SuccessThreshold: 2,
        },
    })
    if err != nil {
        log.Fatal(err)
    }
    defer mysqlkit.Close()
}
```

> 需要控制初始化超时用 `InitContext(ctx, cfg)`。

### 2. 定义表模型（每张表一个 struct）

把 struct 的 gorm tag 当作建表 DDL 的映射，模块本身用 gorm 原生能力操作：

```go
type PoolRecord struct {
    ID           uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
    ChainId      uint64    `gorm:"type:bigint;not null" json:"chain_id"`
    ProtocolType uint16    `gorm:"type:smallint;not null" json:"protocol_type"`
    PoolAddress  string    `gorm:"type:varchar(64);not null" json:"pool_address"`
    Token0       string    `gorm:"type:varchar(64);not null" json:"token0"`
    Token1       string    `gorm:"type:varchar(64);not null" json:"token1"`
    Reserve0     string    `gorm:"type:varchar(100);not null" json:"reserve0"`
    Reserve1     string    `gorm:"type:varchar(100);not null" json:"reserve1"`
    Fee          uint16    `gorm:"type:smallint;not null" json:"fee"`
    TickSpacing  uint16    `gorm:"type:smallint;not null" json:"tick_spacing"`
    CreateTime   time.Time `gorm:"column:create_time;type:DATETIME;not null;default:CURRENT_TIMESTAMP;autoCreateTime" json:"create_time"`
    UpdateTime   time.Time `gorm:"column:update_time;type:DATETIME;not null;default:CURRENT_TIMESTAMP" json:"update_time"`
}

func (PoolRecord) TableName() string { return "pool" }
```

### 3. 操作数据（四种方式，按场景选）

#### 方式 A：全局 DB，自动读写分离（最常用）

```go
// 写：自动走主库
err := mysqlkit.DB().Create(&record).Error

// 读：自动走健康从库
var pools []PoolRecord
err := mysqlkit.DB().Where("chain_id = ?", 1).Order("id desc").Limit(10).Find(&pools).Error
```

#### 方式 B：显式指定主库/从库

```go
mysqlkit.Master().Create(&record)           // 强制主库
mysqlkit.Read().Where("chain_id = ?", 1)    // 强制从库
```

#### 方式 C：写后立即读（强一致）

主从复制有延迟，写完立刻读从库可能读不到刚写的数据。用上下文标记强制本次读走主库：

```go
ctx := mysqlkit.WithStrongConsistency(context.Background())
var pool PoolRecord
mysqlkit.DB().WithContext(ctx).First(&pool, 1)
```

> `WithStrongConsistency` 是 `WithPrimary` 的别名，语义更直观。

#### 方式 D：表模型上直接操作

```go
type PoolModel struct{ model.BaseModel }

var Pool PoolModel

func GetOnePool(chainId uint64, token0, token1 string) ([]*PoolRecord, error) {
    query := Pool.DB().Table(PoolRecord{}.TableName()).
        Where("chain_id = ?", chainId).
        Where("token0 = ?", token0).
        Where("token1 = ?", token1)
    var records []*PoolRecord
    if err := query.Order("id desc").Limit(1).Scan(&records).Error; err != nil {
        return nil, err
    }
    return records, nil
}
```

`model.BaseModel` 提供：`DB()`、`Master()`、`Read()`、`DBWithContext(ctx)`、
`StrongDB(ctx)`、`Transaction(fn)`、`TransactionContext(ctx, fn)`。

### 4. 事务

```go
err := mysqlkit.Transaction(func(tx *gorm.DB) error {
    if err := tx.Create(&record).Error; err != nil {
        return err   // 返回错误自动回滚
    }
    return tx.Model(&PoolRecord{}).Where("id = ?", record.ID).
        Update("reserve0", "999").Error
})
```

> 事务一定在主库；**回调内必须用 `tx`**，用 `mysqlkit.DB()` 会另开会话、不在事务内。

## API 速查

### 生命周期

| 函数 | 说明 |
| --- | --- |
| `Init(cfg)` / `InitContext(ctx, cfg)` | 初始化全局连接池（重复调用返回 `ErrAlreadyInit`） |
| `Close()` | 关闭所有连接池与健康检测协程 |

### DB 句柄

| 函数 | 说明 |
| --- | --- |
| `DB()` / `DBE()` | 读写分离的 gorm.DB（写主库、读从库） |
| `Master()` / `MasterE()` | 强制主库 |
| `Read()` / `ReadE()` | 强制从库 |
| `SQLDB()` | 主库的 `*sql.DB`（**绕过** DBResolver） |
| `ReplicaSQLDB(name)` | 指定从库的 `*sql.DB` |

> `*E` 版本返回 `(db, error)`，未初始化时可拿到 `ErrNotInitialized`；非 E 版本失败返回 nil。

### 事务 / 一致性

| 函数 | 说明 |
| --- | --- |
| `Transaction(fn)` / `TransactionContext(ctx, fn)` | 主库事务 |
| `WithStrongConsistency(ctx)` / `WithPrimary(ctx)` | 标记本次请求读走主库 |

### 运维

| 函数 | 说明 |
| --- | --- |
| `Stats()` | 各节点 `sql.DBStats`（连接数、等待次数等） |
| `Health()` | 各节点健康快照（健康、连续失败/成功次数、最后错误、最后检测时间） |
| `UpdatePoolConfig(c)` | 运行期动态调整连接池，无需重连 |
| `CurrentPoolConfig()` | 读取当前连接池配置 |

## 配置参考

### `Config`

| 字段 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `Master` | `NodeConfig` | 必填（`DSN`） | 主库；`Name` 缺省为 `master` |
| `Replicas` | `[]NodeConfig` | 空 | 从库；`Name` 缺省为 `replica-N` |
| `Pool` | `PoolConfig` | 见下 | 连接池 |
| `Health` | `HealthConfig` | 见下 | 健康检测 |
| `GORMConfig` | `*gorm.Config` | `&gorm.Config{}` | 透传 gorm 配置（会被浅拷贝） |
| `TraceResolverMode` | `bool` | false | DBResolver 调试模式 |

### `PoolConfig`

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `MaxIdleConns` | 10 | 最大空闲连接数 |
| `MaxOpenConns` | 100 | 最大打开连接数 |
| `ConnMaxLifetime` | 1h | 连接最大生命周期 |
| `ConnMaxIdleTime` | 10m | 连接最大空闲时间 |

### `HealthConfig`

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `Enabled` | false | 是否开启后台健康检测 |
| `Interval` | 5s | 检测间隔 |
| `ProbeTimeout` | 2s | 单次 Ping 超时 |
| `FailureThreshold` | 2 | 连续失败几次标记不健康 |
| `SuccessThreshold` | 2 | 连续成功几次标记恢复 |

## 实现与性能说明

- **读写分离**：由官方 `dbresolver` 插件在 gorm callback 层按操作类型切库；本模块额外注册
  `Before("gorm:db_resolver")` 回调，把带 `WithPrimary` 标记的读强制改为写库。
- **从库选择**：自定义 `healthPolicy` 实现 `dbresolver.Policy`，用 `atomic.Uint64` 做无锁轮询，
  跳过不健康从库；**全部从库不可用时回退主库**，保证读请求不失败。
- **健康检测**：后台单协程定时 **并发** Ping 所有节点（含主库），单轮耗时为最慢节点而非累加。
- **状态机**：`node` 用 `atomic.Bool` 存健康位、`atomic.Int32` 存连续成功/失败计数，
  阈值切换；`lastError`/`lastChecked` 用 `RWMutex` 保护。
- **错误上报**：gorm callback 在查询/Raw 之后检查是否为连接类错误
  （`ErrBadConn`/`ECONNRESET`/MySQL 2002/2003/2006/2013/2055 等），命中即累计失败计数，
  使从库故障能在一两次请求内被摘除，而不必等下一轮定时探测。
- **动态池配置**：`UpdatePoolConfig` 直接调用 `sql.DB.SetMax*`，无需重建连接，请求无中断。
- **上下文标记**：`context.WithValue` 携带 `consistencyKey{}`，零分配读取，几乎无开销。

### 仍存在的已知限制（使用需注意）

1. **主库不可用时读请求会失败**：`healthPolicy` 在全部从库不健康时回退主库；如果主库也不可用，
   请求直接报错，没有降级策略。这是有意的——读写分离不提供跨库容灾。
2. **`WithStrongConsistency` 只影响当前 ctx**：它不会传播到新的 goroutine，异步任务需自行带上 ctx。
3. **`Raw()` 的读写判定靠 SQL 前缀猜测**：`dbresolver` 对 `Raw` 用 `switchGuess`（判断是否以
   `select` 开头且不以 `for update` 结尾）。`SELECT ... FOR SHARE`、`WITH ... SELECT` 等写法可能被误判到主库。
4. **`UpdatePoolConfig` 不校验 `MaxOpenConns` 与 `MaxIdleConns` 的运行时语义**：仅做配置合法性校验，
   若把 `MaxOpenConns` 调小于当前已打开连接数，`database/sql` 会逐步回收而非立即断开。
5. **健康检测默认关闭**：`Health.Enabled` 零值为 `false`。生产环境请显式开启，否则从库故障只能靠
   请求错误被动摘除。

## 常见问题（FAQ）

**Q：读到了旧数据？**
主从复制延迟导致。写后立即读用 `WithStrongConsistency(ctx)`；或该读直接走 `Master()`。

**Q：`DB()` 返回 nil？**
未初始化或已 `Close()`。用 `DBE()` 拿错误，或检查启动流程是否调用了 `Init`。

**Q：`Init` 报 `ErrAlreadyInit`？**
全局单例只能初始化一次。测试中需重置请先 `Close()`。

**Q：事务里用 `mysqlkit.DB()` 为什么没生效？**
事务是主库上的独立会话，回调内必须使用传入的 `tx`。

**Q：如何新增一张表？**
参考 `model/example_models.go`：定义 struct + `TableName()`，再按需声明
`type XxxModel struct{ model.BaseModel }`，即可用 `Xxx.DB()` 链式操作。
