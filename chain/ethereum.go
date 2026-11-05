package chain

import (
	"context"
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

// EthCall 执行 eth_call，返回调用返回的原始字节。
func (i *Instance) EthCall(ctx context.Context, msg ethereum.CallMsg, block string) ([]byte, error) {
	if block == "" {
		block = "latest"
	}
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
	var out hexutil.Bytes
	err := i.Call(ctx, &out, "eth_call", arg, block)
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
