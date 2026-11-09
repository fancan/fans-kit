package chain

// 以太坊生态链的一些用法简单封装 提供一些常用的方法

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
)

// HeaderByNumber 按区块号返回区块头（number 为 nil 时取 latest）。
func (i *Instance) HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error) {
	block := "latest"
	if number != nil {
		block = hexutil.EncodeBig(number)
	}
	var header *types.Header
	err := i.Call(ctx, &header, "eth_getBlockByNumber", block, false)
	return header, err
}

// BlockNumber 返回最新区块号。
func (i *Instance) BlockNumber(ctx context.Context) (uint64, error) {
	var n hexutil.Uint64
	err := i.Call(ctx, &n, "eth_blockNumber")
	return uint64(n), err
}

// callArg 把 go-ethereum 的 CallMsg 转成 eth_call/debug_traceCall 的参数对象。
func callArg(msg ethereum.CallMsg) map[string]any {
	arg := map[string]any{"from": msg.From, "to": msg.To}
	if len(msg.Data) > 0 {
		arg["input"] = hexutil.Bytes(msg.Data)
	}
	if msg.Value != nil {
		arg["value"] = (*hexutil.Big)(msg.Value)
	}
	if msg.Gas != 0 {
		arg["gas"] = hexutil.Uint64(msg.Gas)
	}
	if msg.GasPrice != nil {
		arg["gasPrice"] = (*hexutil.Big)(msg.GasPrice)
	}
	if msg.GasFeeCap != nil {
		arg["maxFeePerGas"] = (*hexutil.Big)(msg.GasFeeCap)
	}
	if msg.GasTipCap != nil {
		arg["maxPriorityFeePerGas"] = (*hexutil.Big)(msg.GasTipCap)
	}
	if msg.AccessList != nil {
		arg["accessList"] = msg.AccessList
	}
	if msg.BlobGasFeeCap != nil {
		arg["maxFeePerBlobGas"] = (*hexutil.Big)(msg.BlobGasFeeCap)
	}
	if msg.BlobHashes != nil {
		arg["blobVersionedHashes"] = msg.BlobHashes
	}
	return arg
}

// EthCall 执行 eth_call，返回调用返回的原始字节。
func (i *Instance) EthCall(ctx context.Context, msg ethereum.CallMsg, block string) ([]byte, error) {
	if block == "" {
		block = "latest"
	}
	var out hexutil.Bytes
	err := i.Call(ctx, &out, "eth_call", callArg(msg), block)
	return out, err
}

// GetLogs 执行 eth_getLogs，返回匹配过滤条件的日志。
func (i *Instance) GetLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
	var logs []types.Log
	err := i.Call(ctx, &logs, "eth_getLogs", filterArg(q))
	return logs, err
}

// BalanceAt 返回某地址在指定区块的余额（block 为空时取 latest）。
func (i *Instance) BalanceAt(ctx context.Context, a common.Address, block string) (*big.Int, error) {
	if block == "" {
		block = "latest"
	}
	var v hexutil.Big
	err := i.Call(ctx, &v, "eth_getBalance", a, block)
	if err != nil {
		return nil, err
	}
	return (*big.Int)(&v), nil
}

// filterArg 把 go-ethereum 的 FilterQuery 转成 eth_getLogs 的过滤器参数。
func filterArg(q ethereum.FilterQuery) map[string]any {
	a := map[string]any{}
	if q.FromBlock != nil {
		a["fromBlock"] = hexutil.EncodeBig(q.FromBlock)
	}
	if q.ToBlock != nil {
		a["toBlock"] = hexutil.EncodeBig(q.ToBlock)
	}
	if q.BlockHash != nil {
		a["blockHash"] = q.BlockHash.Hex()
	}
	if len(q.Addresses) == 1 {
		a["address"] = q.Addresses[0].Hex()
	} else if len(q.Addresses) > 1 {
		a["address"] = q.Addresses
	}
	if len(q.Topics) > 0 {
		t := make([]any, len(q.Topics))
		for x, g := range q.Topics {
			if len(g) == 1 {
				t[x] = g[0].Hex()
			} else if len(g) > 1 {
				t[x] = g
			}
		}
		a["topics"] = t
	}
	return a
}

// SendRawTransaction 广播一笔已签名交易（0x 前缀 hex 编码），返回交易哈希。
// eth_sendRawTransaction 在方法策略中默认不可重试/不故障切换，避免重复广播。
func (i *Instance) SendRawTransaction(ctx context.Context, raw []byte) (common.Hash, error) {
	var hash common.Hash
	err := i.Call(ctx, &hash, "eth_sendRawTransaction", "0x"+hex.EncodeToString(raw))
	return hash, err
}

// rpcTxResult 解析 eth_getTransactionByHash 的返回：交易本体 + 是否 pending（无区块号）。
type rpcTxResult struct {
	tx      *types.Transaction
	pending bool
}

func (r *rpcTxResult) UnmarshalJSON(msg []byte) error {
	if err := json.Unmarshal(msg, &r.tx); err != nil {
		return err
	}
	var extra struct {
		BlockNumber *string `json:"blockNumber"`
	}
	if err := json.Unmarshal(msg, &extra); err != nil {
		return err
	}
	r.pending = extra.BlockNumber == nil
	return nil
}

// TransactionByHash 返回指定哈希的交易及其 pending 状态。
func (i *Instance) TransactionByHash(ctx context.Context, hash common.Hash) (*types.Transaction, bool, error) {
	var res rpcTxResult
	if err := i.Call(ctx, &res, "eth_getTransactionByHash", hash); err != nil {
		return nil, false, err
	}
	if res.tx == nil {
		return nil, false, ethereum.NotFound
	}
	return res.tx, res.pending, nil
}

// RawTransactionByHash 返回指定哈希的原始交易字节（0x 前缀 hex）。
func (i *Instance) RawTransactionByHash(ctx context.Context, hash common.Hash) (string, error) {
	var raw string
	err := i.Call(ctx, &raw, "eth_getRawTransactionByHash", hash)
	return raw, err
}

// PendingNonceAt 返回地址的 pending nonce（含 mempool 未确认交易）。
func (i *Instance) PendingNonceAt(ctx context.Context, addr common.Address) (uint64, error) {
	var nonce hexutil.Uint64
	err := i.Call(ctx, &nonce, "eth_getTransactionCount", addr, "pending")
	return uint64(nonce), err
}

// SuggestGasPrice 返回当前建议 gas 价格（eth_gasPrice）。
func (i *Instance) SuggestGasPrice(ctx context.Context) (*big.Int, error) {
	var gp hexutil.Big
	if err := i.Call(ctx, &gp, "eth_gasPrice"); err != nil {
		return nil, err
	}
	return (*big.Int)(&gp), nil
}

// TraceCall 执行 debug_traceCall（如 callTracer），返回原始 trace JSON。
func (i *Instance) TraceCall(ctx context.Context, msg ethereum.CallMsg, tracer string, tracerConfig any) (json.RawMessage, error) {
	var out json.RawMessage
	err := i.Call(ctx, &out, "debug_traceCall", callArg(msg), "latest", map[string]any{
		"tracer": tracer, "tracerConfig": tracerConfig,
	})
	return out, err
}
