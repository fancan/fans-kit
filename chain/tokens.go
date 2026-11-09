package chain

import (
	"context"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// Token 是 ERC20 代币的元数据。
type Token struct {
	ChainID  uint64
	Address  string
	Symbol   string
	Name     string
	Decimals uint8
}

// erc20ABIJSON 仅包含 name/symbol/decimals 三个视图方法的最小 ERC20 ABI。
const erc20ABIJSON = `[
  {"inputs":[],"name":"name","outputs":[{"internalType":"string","name":"","type":"string"}],"stateMutability":"view","type":"function"},
  {"inputs":[],"name":"symbol","outputs":[{"internalType":"string","name":"","type":"string"}],"stateMutability":"view","type":"function"},
  {"inputs":[],"name":"decimals","outputs":[{"internalType":"uint8","name":"","type":"uint8"}],"stateMutability":"view","type":"function"}
]`

var (
	erc20ABI     abi.ABI
	erc20ABIOnce sync.Once
)

// getERC20ABI 惰性解析并缓存 ERC20 ABI。
func getERC20ABI() abi.ABI {
	erc20ABIOnce.Do(func() {
		a, err := abi.JSON(strings.NewReader(erc20ABIJSON))
		if err != nil {
			panic("chain: invalid erc20 abi: " + err.Error())
		}
		erc20ABI = a
	})
	return erc20ABI
}

// TokenInfo 批量获取一批 ERC20 地址的 symbol/name/decimals，复用 BatchCall 的
// 逐元素 revert 容忍与故障切换。解析失败的字段保持零值，不影响其它地址。
func (i *Instance) TokenInfo(ctx context.Context, addrs []common.Address) (map[common.Address]*Token, error) {
	erc := getERC20ABI()
	packedSym, err := erc.Pack("symbol")
	if err != nil {
		return nil, err
	}
	packedName, err := erc.Pack("name")
	if err != nil {
		return nil, err
	}
	packedDec, err := erc.Pack("decimals")
	if err != nil {
		return nil, err
	}

	calls := make([]CallArg, 0, len(addrs)*3)
	for _, a := range addrs {
		calls = append(calls,
			CallArg{To: a, Data: packedSym},
			CallArg{To: a, Data: packedName},
			CallArg{To: a, Data: packedDec},
		)
	}

	res, err := i.BatchCall(ctx, calls)
	if err != nil {
		return nil, err
	}

	out := make(map[common.Address]*Token, len(addrs))
	for j, a := range addrs {
		tok := &Token{ChainID: i.ChainID(), Address: strings.ToLower(a.Hex())}
		if j*3 < len(res) && res[j*3] != nil {
			if vals, err := erc.Unpack("symbol", res[j*3]); err == nil && len(vals) > 0 {
				tok.Symbol, _ = vals[0].(string)
			}
		}
		if j*3+1 < len(res) && res[j*3+1] != nil {
			if vals, err := erc.Unpack("name", res[j*3+1]); err == nil && len(vals) > 0 {
				tok.Name, _ = vals[0].(string)
			}
		}
		if j*3+2 < len(res) && res[j*3+2] != nil {
			if vals, err := erc.Unpack("decimals", res[j*3+2]); err == nil && len(vals) > 0 {
				if d, ok := vals[0].(uint8); ok {
					tok.Decimals = d
				}
			}
		}
		out[a] = tok
	}
	return out, nil
}
