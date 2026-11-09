// Package subscription 提供逻辑订阅共享的基础原语（如去重集合）。
package subscription

import (
	"container/list"
	"sync"
)

// Deduper 是一个有界的FIFO去重集合，用于重连后避免重复投递。
type Deduper struct {
	mu    sync.Mutex
	cap   int
	set   map[string]*list.Element
	order *list.List
}

// NewDeduper 创建指定容量的去重集合（非正数取默认值 100000）。
func NewDeduper(capacity int) *Deduper {
	if capacity <= 0 {
		capacity = 100000
	}
	return &Deduper{cap: capacity, set: map[string]*list.Element{}, order: list.New()}
}

// Seen 判断key是否已存在；不存在则记录并（必要时）淘汰最旧项。
func (d *Deduper) Seen(key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.set[key]; ok {
		return true
	}
	el := d.order.PushBack(key)
	d.set[key] = el
	if d.order.Len() > d.cap {
		front := d.order.Front()
		delete(d.set, front.Value.(string))
		d.order.Remove(front)
	}
	return false
}

// Forget 移除key，使之后再次出现时可重新投递（例如重组后重新纳入规范链）。
func (d *Deduper) Forget(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if el, ok := d.set[key]; ok {
		delete(d.set, key)
		d.order.Remove(el)
	}
}
