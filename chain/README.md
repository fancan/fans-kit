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
├── executor.go              # RPC 执行、重试与 Batch
├── range_logs.go            # eth_getLogs 区间自适应拆分
├── ethereum.go              # 类型化以太坊辅助方法与参数编码
├── monitor.go               # 提供商健康与区块高度监控
├── provider_admin.go        # 运行期提供商增删与选择器钩子
├── subscriptions.go         # WS 重连与可靠日志订阅
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
└── examples/basic/main.go
```

## 安装

请把模块路径 `github.com/fancan/fans-kit/chain` 改成你自己的仓库路径，然后：

```bash
go mod tidy
go test ./...
```

要求 Go 1.22+ 与 go-ethereum v1.14.12。

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

```go
block, err := chain.BlockNumber(ctx)

result, err := chain.EthCall(ctx, ethereum.CallMsg{
    To:   &contract,
    Data: calldata,
}, "latest")

logs, err := chain.GetLogs(ctx, ethereum.FilterQuery{
    FromBlock: big.NewInt(100),
    ToBlock:   big.NewInt(200),
})

var feeHistory json.RawMessage
err = chain.CallInto(ctx, &feeHistory, "eth_feeHistory", 10, "latest", []float64{25, 50, 75})
```

任意方法用 `Call` / `CallInto` 调用，**没有**以太坊方法白名单。

指定链用 `CallOn`、`BlockNumberOn`、`EthCallOn`、`GetLogsOn`、`BalanceAtOn`、
`SubscribeOn` 或 `SubscribeLogsOn`。

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

## 已知限制与待办

> ⚠️ 本模块当前存在一个**阻断性**问题：内部 import 仍使用脚手架占位路径
> `github.com/fancan/fans-kit/chain/...`，且 `go.mod` 缺少 `github.com/ethereum/go-ethereum` 与
> `gopkg.in/yaml.v3` 依赖，因此**当前无法直接编译**。使用前需把内部 import 统一改为
> `github.com/fancan/fans-kit/chain/...` 并 `go mod tidy` 补齐依赖。
>
> 其余已知问题详见代码评审结论（bug 分析），包括：
> `transport.Client.Subscribe` 的 `namespace` 参数实际被当作 `eth_subscribe` 子类型使用
> （非 `eth` 命名空间会被静默忽略）等。
