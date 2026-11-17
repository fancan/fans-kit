# Chain —— 多提供商以太坊 RPC 基础设施（中文使用文档）

`chain` 是一个面向以太坊兼容 JSON-RPC 的 Go 基础设施包。它提供**零注入的包级门面**、
多链实例、独立的 HTTP/WS 提供商池、健康检查、熔断、故障自动切换、批处理、Prometheus
指标暴露，以及可恢复的日志订阅。

> 说明：HTTP 指「以太坊 JSON-RPC over HTTP(S)」，并非 REST 资源 API。SDK 完整保留
> JSON-RPC 方法模型，因此 `debug_*`、`trace_*` 等自定义命名空间无需修改 SDK 即可调用。

## 目录结构

```text
chain/
├── chain.go                 # 对外门面、全局 Manager、多链 API
├── instance.go              # 单链生命周期与组合根
├── executor.go              # RPC 执行、重试、Batch 与批量 eth_call（BatchCall）
├── ethereum.go              # 类型化以太坊辅助方法与参数编码
├── tokens.go                # ERC20 代币元数据（symbol/name/decimals）
├── abi/
│   ├── abi.go               # //go:embed 编译期加载 ABI JSON，init 中一次性解析
│   ├── erc20.json           # ERC20 ABI（name/symbol/decimals）
│   └── multicall.json       # multicall ABI（aggregate/tryAggregate）
├── range_logs.go            # eth_getLogs 区间自适应拆分
├── monitor.go               # 提供商健康与区块高度监控
├── provider_admin.go        # 运行期提供商增删与选择器钩子
├── subscriptions.go         # WS 重连、日志订阅与类型化订阅流
├── policy.go                # 方法级重试/故障切换策略注册表
├── config/
│   ├── config.go            # YAML 解析与环境变量展开
│   └── example.yaml         # 配置示例
├── provider/
│   ├── provider.go          # 端点、状态、熔断器与池
│   └── selector.go          # 可插拔的优先级/延迟/权重选择器
├── transport/
│   └── rpc.go               # go-ethereum rpc.Client 适配器
├── metrics/
│   └── metrics.go           # Prometheus 文本暴露
├── subscription/
│   └── subscription.go      # 有界事件去重
└── 
```

## 安装

请把模块路径 `github.com/fancan/fans-kit/chain` 改成你自己的仓库路径，然后：

```bash
go mod tidy
go test ./...
```

要求 Go 1.25+ 与 go-ethereum v1.17.x。

## 配置与初始化

```bash
export ALCHEMY_API_KEY=...
export INFURA_API_KEY=...
```

```go
if err := chain.InitFile("config/example.yaml"); err != nil {
    return err
}
defer chain.Close()
```

配置支持多链。每个提供商可同时配置 HTTP 与 WS 地址、优先级、权重、自定义请求头与
API Key 头。请把密钥放进环境变量，不要提交真实密钥。

URL 路径中嵌入的 API Key 用 `${ENV_VAR}` 展开；需要请求头的提供商：

```yaml
- name: private-rpc
  priority: 1
  http_url: https://rpc.example.com
  ws_url: wss://rpc.example.com/ws
  api_key: ${RPC_API_KEY}
  api_key_header: Authorization
```

如需 `Authorization: Bearer ...`，请通过 `headers` 配置完整值，例如
`headers: { Authorization: "Bearer ${RPC_API_KEY}" }`。

## 零侵入使用

### 第一步：接入（只需三步）

```go
func main() {
	// 1) 初始化：传入配置文件路径，内部完成 YAML 解析、提供商拨号、
	//    链 ID 校验、健康检查/区块监控协程启动。
	if err := chain.InitFile("config/example.yaml"); err != nil {
		panic(err)
	}
	// 2) 退出时统一释放：关闭所有 provider 连接与后台协程。
	defer chain.Close()

	// 3) （可选）暴露 Prometheus 指标。
	mux := http.NewServeMux()
	_ = chain.RegisterMetrics(mux)
	go http.ListenAndServe(":9090", mux)
}
```

> 也可以直接传配置结构体 `chain.Init(cfg)` 或 YAML 字节 `chain.InitYAML(data)`。

### 第二步：零侵入调用（无需注入实例）

核心思想：`import "github.com/fancan/fans-kit/chain"` 后直接调用**包级函数**即可，
内部自动使用全局管理器 + 默认链，并在多提供商之间自动故障切换。业务代码里
**不 new 任何 client、不注入任何 Logger/实例、不手动管理 provider**。

```go
import (
	"context"
	"encoding/json"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/fancan/fans-kit/chain"
)

// 说明：下面示例中的 ctx、userAddr、txHash、signedRawTx、pairABI、usdcAddr 等
// 均为业务代码里已经定义好的变量/占位符，重点是展示「直接用 chain.XXX 包级函数」。

// ① 读最新区块号：最常用的健康探针 / 进度锚点。
block, err := chain.BlockNumber(ctx)

// ② 查余额：block 为空字符串时表示 latest。
balance, err := chain.BalanceAt(ctx, userAddr, "latest")

// ③ 调用合约只读方法（eth_call）：查 pair 储备、价格、状态等。
calldata, _ := pairABI.Pack("getReserves")
reserves, err := chain.EthCall(ctx, ethereum.CallMsg{To: &pairAddr, Data: calldata}, "latest")

// ④ 查历史日志（eth_getLogs）：拉取某个事件的区间日志。
logs, err := chain.GetLogs(ctx, ethereum.FilterQuery{
	FromBlock: big.NewInt(100),
	ToBlock:   big.NewInt(200),
	Addresses: []common.Address{contractAddr},
})

// ⑤ 批量 eth_call：一次请求读多份状态，个别 revert 只置空对应条目、不拖垮整批。
results, err := chain.BatchCall(ctx, []chain.CallArg{
	{To: pairA, Data: getReservesA},
	{To: pairB, Data: getReservesB},
	{To: tokenC, Data: symbolCalldata},
})

// ⑤′ 链上 multicall（推荐）：单次 eth_call 打到 multicall 合约，按链自动切换地址。
results, err = chain.Multicall(ctx, []chain.CallArg{
	{To: pairA, Data: getReservesA},
	{To: pairB, Data: getReservesB},
})

// ⑥ 查交易与账户状态：交易详情、原始字节、pending nonce、gas 价格。
tx, isPending, err := chain.TransactionByHash(ctx, txHash)   // isPending 区分 mempool 未确认
raw, err := chain.RawTransactionByHash(ctx, txHash)
nonce, err := chain.PendingNonceAt(ctx, senderAddr)
gasPrice, err := chain.SuggestGasPrice(ctx)

// ⑦ 广播已签名交易：eth_send* 默认不重试/不切换，避免重复广播。
txHash, err := chain.SendRawTransaction(ctx, signedRawTx)

// ⑧ 订阅新区块 / pending 交易：类型化通道，自动重连。
heads, err := chain.HeadStream(ctx)        // <-chan uint64
for n := range heads { /* 处理新块号 */ }
pending, err := chain.PendingTxStream(ctx) // <-chan common.Hash
for h := range pending { /* 处理 pending tx */ }

// ⑨ 订阅日志（可恢复、带回填与重组回退）。
logCh, err := chain.SubscribeLogs(ctx, ethereum.FilterQuery{FromBlock: big.NewInt(200)})
for lg := range logCh { /* 处理日志，注意 lg.Removed */ }

// ⑩ 代币元数据：批量读 ERC20 symbol/name/decimals。
tokens, err := chain.TokenInfo(ctx, []common.Address{usdcAddr, wethAddr})

// ⑪ 任意自定义方法：debug_* / trace_* / 厂商私有方法均可，无白名单。
var feeHistory json.RawMessage
err = chain.CallInto(ctx, &feeHistory, "eth_feeHistory", 10, "latest", []float64{25, 50, 75})

// ⑫ 指定链：所有读/写/订阅方法都提供 `*On` 版本（传入链名即可）。
arbBalance, err := chain.BalanceAtOn(ctx, "arbitrum", userAddr, "latest")
arbHeads, err := chain.HeadStreamOn(ctx, "arbitrum")
```

> 快速对照：默认链用 `chain.XXX(...)`；指定链用 `chain.XXXOn(ctx, chainName, ...)`。
> 需要拿到实例做更细粒度控制时，用 `chain.On(name)` / `chain.Default()` 获取 `*chain.Instance`。

## 类型化读写与交易方法

针对高频场景内置了类型安全的辅助方法（均可自动故障切换，且都有 `*On` 指定链版本）：

```go
// 交易 / 写
hash, err := chain.SendRawTransaction(ctx, signedRawTx)

tx, isPending, err := chain.TransactionByHash(ctx, txHash)

raw, err := chain.RawTransactionByHash(ctx, txHash)

nonce, err := chain.PendingNonceAt(ctx, addr)

gasPrice, err := chain.SuggestGasPrice(ctx)

// trace（callTracer 等）
var trace json.RawMessage
trace, err = chain.TraceCall(ctx, ethereum.CallMsg{To: &pair, Data: calldata},
    "callTracer", map[string]any{"onlyTopCall": true})
```

说明：

- `SendRawTransaction` 走方法策略，`eth_send*` 默认**不重试/不故障切换**，避免重复广播；
- `TransactionByHash` 返回 `isPending` 用于区分 mempool 未确认交易；
- `TraceCall` 用于解析聚合器/multicall 间接调用等 calldata 无法还原的场景。

## 批处理

```go
var block hexutil.Uint64
var chainID hexutil.Uint64
err := chain.Batch(ctx, []chain.BatchElem{
    {Method: "eth_blockNumber", Result: &block},
    {Method: "eth_chainId", Result: &chainID},
})
// 需同时检查每个元素的 Error 与顶层 error。
```

批处理整体发往**一个**提供商，不跨提供商拆分。仅当批内所有方法都配置为可安全重试时，
传输层失败才允许故障切换。每个元素的 JSON-RPC 错误返回在该元素的 `Error` 字段。

## 批量 eth_call（BatchCall）

对「批量读取合约状态」这一高频场景，提供两种方式：**链上 multicall**（推荐，单次 RPC 请求）
与 **JSON-RPC 批处理**（无需部署 multicall 合约）。

JSON-RPC 批处理版本：

```go
res, err := chain.BatchCall(ctx, []chain.CallArg{
    {To: pairA, Data: getReservesCalldata},
    {To: pairB, Data: getReservesCalldata},
    {To: tokenC, Data: symbolCalldata},
})
// res[i] 与输入一一对应
```

语义要点：

- **逐元素 revert 容忍**：某个合约调用 revert 是合法结果，对应位置返回 `nil` 空条目，不影响整批；
- **网关整体拒绝识别**：若提供商对每个元素都返回限流类错误（无成功、无 revert），识别为
  provider 级失败并自动故障切换；
- 复用现有 HTTP 熔断器、选择器与重试/指标链路。

## 链上 Multicall

各链部署的 multicall 合约地址不同，按链配置 `multicall.address` 后自动切换；类型通过
`multicall.type` 选择（`try_aggregate` 默认 / `aggregate`）。

```go
// ① 使用配置的类型（默认 try_aggregate）
res, err := chain.Multicall(ctx, []chain.CallArg{
    {To: pairA, Data: getReservesA},
    {To: pairB, Data: getReservesB},
})

// ② 显式指定类型
res, err = chain.MulticallTryAggregate(ctx, calls) // 逐调用成功标志，失败位置返回 nil
res, err = chain.MulticallAggregate(ctx, calls)    // 任一子调用失败则整批失败

// ③ 指定链
res, err = chain.MulticallOn(ctx, "arbitrum", calls)
```

两种类型：

| 类型 | 方法 | 语义 |
| --- | --- | --- |
| `aggregate` | Multicall v1/v2 的 `aggregate` | 任一子调用失败则整批 revert |
| `try_aggregate` | Multicall2 的 `tryAggregate` | 逐调用返回 `(success, returnData)`，失败位置为 `nil` |

`TokenInfo` 已内部切换到 `tryAggregate` 语义的 multicall（个别非标准代币 revert 不影响其它地址）。

## 方法策略

读方法默认可重试。交易提交与账户/矿工方法默认不自动重放——这是刻意的：
`eth_sendRawTransaction` 超时不代表交易一定没有广播。

```go
chain.RegisterMethodPolicy("vendor_customRead", chain.MethodPolicy{
    Retryable: true, Failover: true, MaxRetries: 2,
})
```

自定义写方法应设 `Retryable: false`、`Failover: false`，除非能证明重放是安全的。

## 提供商管理与选择

HTTP 与 WS 池相互独立。同时有 HTTP 与 WS 地址的提供商会创建两个独立端点对象、
独立的健康/熔断状态。提供商选择依次依据：

1. 健康 / 半开资格；
2. 开启区块监控时的落后区块过滤；
3. 配置优先级；
4. 延迟 ÷ 权重；
5. 在途请求数。

`block_monitor.enabled` 为真时，监控还会在共同高度（带 `consistency_depth` 个确认）
比较各提供商的区块哈希：只有**严格多数**不一致时才对少数派端点记一次一致性失败，
票数相持视为模糊、不任意惩罚。

可按传输类型安装自定义选择器：

```go
inst, _ := chain.On("ethereum")
inst.SetHTTPSelector(mySelector)
inst.SetWSSelector(myWSSelector)
```

运行期可用 `chain.AddProvider` / `chain.RemoveProvider` 增删提供商。移除会关闭该提供商的
连接；请避免在依赖某个在途操作时移除对应提供商。

## WebSocket 与日志恢复

`Subscribe` 会重连并重新订阅通用订阅。断线期间遗漏的通用订阅消息无法通用地重放。

`SubscribeLogs` 更强：

- 先建立 WS 订阅再做 HTTP 回填，消除「订阅/回填」竞态；
- 从配置的起始区块以有界区间重放 `eth_getLogs`，提供商报告区间过大时自动拆分；
- 按 `(blockHash, txHash, logIndex)` 去重；
- 重连时校验检查点区块哈希；
- 检测到重组时回退一个有限窗口，对已投递但不再规范的日志发送 `Removed=true`，再重放替换日志；
- 使用有界重组检测与有界去重；投递未确认日志，若近期日志变为非规范则发送 `Removed=true`。

使用时需 `log_recovery.enabled: true`。`FromBlock` 建立初始重放游标；实时订阅不接受
`ToBlock` 与 `BlockHash`。不同提供商的 `eth_getLogs` 区间上限不同，请按所用厂商调整 `max_range`。

这是**至少一次**的事件流，仅在配置窗口内去重，不是持久化消息队列。请自行持久化最后处理
区块与应用状态。关键索引器应使用外部持久化检查点，并让事件处理器幂等。超过 `reorg_depth`
的深重组需要应用层对账。

## 类型化订阅流

除了通用的 `Subscribe`/`SubscribeLogs`，还内置了两个类型化订阅（复用自动重连逻辑，
调用方取消或实例关闭时通道被关闭）：

```go
heads, err := chain.HeadStream(ctx)          // <-chan uint64，新块号
for n := range heads { ... }

hashes, err := chain.PendingTxStream(ctx)    // <-chan common.Hash，pending tx hash
for h := range hashes { ... }
```

无 WS 提供商时返回错误；需要多链时用 `HeadStreamOn` / `PendingTxStreamOn`。

## 代币元数据（TokenInfo）

批量读取 ERC20 的 symbol/name/decimals。底层通过链上 multicall（`tryAggregate` 语义）执行，
逐调用 revert 容忍（个别非标准代币不影响其它地址）；需先配置该链的 `multicall.address`：

```go
tokens, err := chain.TokenInfo(ctx, []common.Address{usdc, weth, dai})
for addr, t := range tokens {
    log.Printf("%s %s(%s) decimals=%d", addr, t.Name, t.Symbol, t.Decimals)
}
```

## Prometheus

在 YAML 中启用指标并挂载处理器：

```go
mux := http.NewServeMux()
if err := chain.RegisterMetrics(mux); err != nil { return err }
http.ListenAndServe(":9090", mux)
```

暴露 RPC 请求/错误/重试/延迟指标，以及提供商健康、最新区块、延迟、在途、总失败数、
区块一致性失败等 gauge/counter。若担心指标基数，应避免不可信的任意方法名（provider/method
标签为便于诊断而有意保留）。

## 配置参考

顶层：

| 字段 | 说明 |
| --- | --- |
| `default` | 默认链名称（省略时取字典序第一条） |
| `chains` | 各链配置 |
| `metrics` | Prometheus 指标配置 |

链配置 `chains.<name>`：

| 字段 | 说明 |
| --- | --- |
| `chain_id` | 链 ID（健康检查校验，0 表示跳过） |
| `providers` | 提供商列表 |
| `http` | HTTP 配置 |
| `websocket` | WebSocket 配置 |
| `health_check` | 健康检查配置 |
| `block_monitor` | 区块监控配置 |
| `log_recovery` | 日志恢复配置 |
| `multicall` | 链上 multicall 合约配置 |

提供商 `providers[]`：

| 字段 | 说明 |
| --- | --- |
| `name` | 唯一名称 |
| `priority` | 优先级（越小越优先） |
| `weight` | 权重（延迟按权重折算） |
| `http_url` | HTTP(S) 地址 |
| `ws_url` | WS(S) 地址 |
| `api_key` | API Key（支持 `${ENV}` 展开） |
| `api_key_header` | 注入 API Key 的请求头名 |
| `headers` | 额外请求头 |

multicall `multicall`：

| 字段 | 说明 |
| --- | --- |
| `address` | 该链部署的 multicall 合约地址（0x 前缀）；不同链地址不同，按链切换 |
| `type` | `aggregate` / `try_aggregate`（默认 `try_aggregate`） |

关键默认值（见 `config.go` 的 `defaults`）：HTTP 超时 10s、批处理上限 100、WS 重连 2s、
健康检查间隔 10s/超时 3s/失败阈值 3/成功阈值 2/冷却 20s、区块监控间隔 5s/最大落后 5/一致深度 2、
日志恢复 max_range 2000/去重容量 100000/重组深度 64。

## 重要运维注意

- `eth_call` 与 `eth_getLogs` 通常可安全重试，但各提供商链头不同时结果可能不同。建议开启区块监控并选合适的 `max_lag`。
- `eth_sendRawTransaction` 默认不跨提供商重试。若自定义重放，必须使用同一份已签名交易并按交易哈希对账。
- HTTP 与 WS 提供商健康相互独立：某提供商可能支持 HTTP 但不支持订阅，反之亦然。
- go-ethereum 的 RPC 客户端处理单条 WS 连接的重连；本包处理提供商级故障切换与逻辑重订阅。
- 本模块不承诺跨提供商状态一致。套利场景应比较区块号/哈希，并将相关读取固定到同一区块标签/号。
- 请在启动时调用一次 `chain.Init`，退出时调用 `chain.Close`；避免在有请求在途时反复替换全局管理器。

## 实现与性能说明

并发与性能设计要点（供压测/调优参考）：

- **低锁设计**：全局管理器用 `sync.RWMutex`；端点熔断状态用 `RWMutex`；在途请求数用
  `atomic.Int64`；指标计数用 `atomic.Uint64`。热点读取（选择器/快照）只走读锁或原子读。
- **选择器快照优化**：`Pool.Select` 与 `PriorityLatencySelector` 对每个端点**只取一次快照**
  再排序/过滤，避免在排序比较器中反复加锁取 `Snapshot`（从 O(n log n) 次快照降到 O(n) 次）。
- **熔断与半开**：熔断打开后进入冷却；半开状态只有一个「试探槽位」（`probeInFlight`），
  保证同一时刻只有一个请求在探测故障端点，避免雪崩。
- **后台监控生命周期**：健康检查/区块监控使用实例 `ctx`，`Close` 会立即取消在途探测并
  等待后台协程退出。
- **批处理**：`Batch`/`BatchCall` 复用同一套端点选择、熔断记录与指标链路；每个尝试都套
  `http.timeout`，避免单个请求挂死。
- **错误分类**：传输级失败（限流/超时/5xx 等）触发熔断与切换；应用级 revert 不误伤熔断器
  （`BatchCall` 中 revert 直接返回 nil 空条目）。

## 已知限制

- `BatchCall` / `TokenInfo` 使用 go-ethereum 的 `rpc.BatchCallContext` 做批处理，
  逐元素错误粒度与直连 HTTP 一致，但元素级错误分类依赖 `retryableError` 的字符串启发式；
  极少数厂商的限流文案若未覆盖到，可能无法被识别为「网关整体拒绝」。
- `TransactionByHash` 依赖 go-ethereum 的 `types.Transaction` 反序列化，个别私有链的
  非标交易字段可能导致解析失败（返回错误而非 panic）。
- 订阅流的区块号/哈希仅来自所选 WS 端点，跨提供商状态一致性仍需业务层对账（见运维注意）。
