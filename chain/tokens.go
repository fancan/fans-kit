package chain

import (
	"context"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	chainabi "github.com/fancan/fans-kit/chain/abi"
)

// Token 是 ERC20 代币的元数据。
type Token struct {
	ChainID  uint64
	Address  string
	Symbol   string
	Name     string
	Decimals uint8
}

// TokenInfo 批量获取一批 ERC20 地址的 symbol/name/decimals。
// 底层通过链上 multicall 合约（tryAggregate 语义）执行，逐调用 revert 容忍；
// 个别非标准代币（缺 symbol/decimals 导致 revert）对应字段保持零值，不影响其它地址。
// 若该链未配置 multicall 地址，则返回错误。
func (i *Instance) TokenInfo(ctx context.Context, addrs []common.Address) (map[common.Address]*Token, error) {
	erc := chainabi.ERC20
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

	res, err := i.MulticallTryAggregate(ctx, calls)
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
