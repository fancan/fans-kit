package manager

import "database/sql"

// NodeStats 节点状态统计
type NodeStats struct {
	Name  string
	Stats sql.DBStats
}

// Stats 获取所有节点的统计数据
func (m *Manager) Stats() []NodeStats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]NodeStats, 0, 1+len(m.replicaOrder))
	if m.master != nil {
		out = append(out, NodeStats{Name: m.masterName, Stats: m.master.Stats()})
	}
	for _, name := range m.replicaOrder {
		if n := m.replicas[name]; n != nil {
			out = append(out, NodeStats{Name: name, Stats: n.db.Stats()})
		}
	}
	return out
}
