package provider

import "sort"

// Selector 对已经具备资格的端点排序。实现不得修改端点状态。
type Selector interface {
	Order(candidates []*Endpoint) []*Endpoint
}

// PriorityLatencySelector 依次按：健康状态、配置优先级、按权重折算的延迟、
// 在途请求数排序，返回副本切片。
type PriorityLatencySelector struct{}

func (PriorityLatencySelector) Order(candidates []*Endpoint) []*Endpoint {
	out := append([]*Endpoint(nil), candidates...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Snapshot(), out[j].Snapshot()
		ar, br := statusRank(a.Status), statusRank(b.Status)
		if ar != br {
			return ar < br
		}
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		as := float64(a.Latency) / float64(maxInt(a.Weight, 1))
		bs := float64(b.Latency) / float64(maxInt(b.Weight, 1))
		if as != bs {
			return as < bs
		}
		return a.InFlight < b.InFlight
	})
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
