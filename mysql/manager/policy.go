package manager

import (
	"database/sql"
	"sync/atomic"

	"gorm.io/gorm"
)

// healthPolicy 健康检测策略：优先选择健康的从库，实在没有了再选择主库
type healthPolicy struct {
	master *sql.DB
	nodes  map[*sql.DB]*node
	next   atomic.Uint64
}

// newHealthPolicy 创建一个健康检测策略
func newHealthPolicy(master *sql.DB, nodes []*node) *healthPolicy {
	byPool := make(map[*sql.DB]*node, len(nodes))
	for _, n := range nodes {
		byPool[n.db] = n
	}
	return &healthPolicy{master: master, nodes: byPool}
}

// Resolve 获取一个可用连接池：优先健康从库轮询，全部不可用时回退主库。
func (p *healthPolicy) Resolve(pools []gorm.ConnPool) gorm.ConnPool {
	if len(pools) == 0 {
		return p.master
	}
	start := p.next.Add(1) - 1
	for offset := 0; offset < len(pools); offset++ {
		pool := pools[(start+uint64(offset))%uint64(len(pools))]
		sqlPool := underlyingSQLDB(pool)
		if sqlPool == nil {
			continue
		}
		if n := p.nodes[sqlPool]; n != nil && n.healthy.Load() {
			return pool
		}
	}
	return p.master
}
