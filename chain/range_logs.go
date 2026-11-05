package chain

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
)

// getLogsAdaptive 在 eth_getLogs 返回“区间过大”类错误时，把区间递归对半拆分后重试。
// 它只针对区间过大错误做拆分，不会拆分鉴权失败、限流等其它错误。
func (i *Instance) getLogsAdaptive(ctx context.Context, base ethereum.FilterQuery, from, to uint64) ([]types.Log, error) {
	q := base
	q.FromBlock = new(big.Int).SetUint64(from)
	q.ToBlock = new(big.Int).SetUint64(to)
	logs, err := i.GetLogs(ctx, q)
	if err == nil {
		return logs, nil
	}
	if from >= to || !isLogRangeLimitError(err) {
		return nil, fmt.Errorf("eth_getLogs range %d-%d: %w", from, to, err)
	}
	mid := from + (to-from)/2
	left, err := i.getLogsAdaptive(ctx, base, from, mid)
	if err != nil {
		return nil, err
	}
	right, err := i.getLogsAdaptive(ctx, base, mid+1, to)
	if err != nil {
		return nil, err
	}
	return append(left, right...), nil
}

// isLogRangeLimitError 判断错误是否为“查询区间过大”一类（可拆分重试）。
func isLogRangeLimitError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, v := range []string{"block range is too wide", "block range too large", "too many results", "query returned more", "response size exceeded", "exceeds the max range", "maximum block range", "max block range"} {
		if strings.Contains(s, v) {
			return true
		}
	}
	return false
}
