package provider

import "sort"

// Selector 对已经具备资格的端点排序。实现不得修改端点状态。
type Selector interface {
	Order(candidates []*Endpoint) []*Endpoint
}

// PriorityLatencySelector 依次按：健康状态、配置优先级、按权重折算的延迟、
// 在途请求数排序，返回副本切片。
type PriorityLatencySelector struct{}

type scoredEndpoint struct {
	ep   *Endpoint
	snap Snapshot
	rank int
}

// Order 端点排序 排序策略：优先端点状态是健康的，再按照端点优先级，再按照延迟低，再按照在途请求数排序
func (PriorityLatencySelector) Order(candidates []*Endpoint) []*Endpoint {
	// 每个端点只取一次快照，避免在排序比较器中反复加锁取 Snapshot（O(n log n) 次 → O(n) 次）。
	items := make([]scoredEndpoint, len(candidates))
	for i, ep := range candidates {
		s := ep.Snapshot()
		items[i] = scoredEndpoint{ep: ep, snap: s, rank: statusRank(s.Status)}
	}

	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		if a.snap.Priority != b.snap.Priority {
			return a.snap.Priority < b.snap.Priority
		}
		as := float64(a.snap.Latency) / float64(maxInt(a.snap.Weight, 1))
		bs := float64(b.snap.Latency) / float64(maxInt(b.snap.Weight, 1))
		if as != bs {
			return as < bs
		}
		return a.snap.InFlight < b.snap.InFlight
	})

	out := make([]*Endpoint, len(items))
	for i := range items {
		out[i] = items[i].ep
	}
	return out
}

// maxInt 返回两整数较大值。
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// statusRank 把状态映射为排序优先级（健康最靠前）。
func statusRank(s Status) int {
	switch s {
	case Healthy:
		return 0
	case Degraded:
		return 1
	case HalfOpen:
		return 2
	case Open:
		return 3
	default:
		return 4
	}
}
