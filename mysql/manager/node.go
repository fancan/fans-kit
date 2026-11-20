package manager

import (
	"database/sql"
	"sync"
	"sync/atomic"
	"time"
)

// node 管理者管理的节点抽象 一般是一个mysql实例
type node struct {
	name         string       // 名称
	db           *sql.DB      // db
	healthy      atomic.Bool  // 是否健康
	failures     atomic.Int32 // 失败次数
	successes    atomic.Int32 // 成功次数
	transitionMu sync.Mutex   // failures、successes锁
	mu           sync.RWMutex // healthy锁
	lastError    string       // 最新错误
	lastChecked  time.Time    // 最新检测时间
}

// newNode 创建一个节点
func newNode(name string, db *sql.DB) *node {
	n := &node{name: name, db: db}
	n.healthy.Store(true)
	return n
}

// markFailure 标记失败
func (n *node) markFailure(threshold int, err error) {
	n.transitionMu.Lock()
	defer n.transitionMu.Unlock()
	n.successes.Store(0)
	if n.failures.Add(1) >= int32(threshold) {
		n.healthy.Store(false)
	}
	n.setCheck(err)
}

// markSuccess 标记成功
func (n *node) markSuccess(threshold int) {
	n.transitionMu.Lock()
	defer n.transitionMu.Unlock()
	n.failures.Store(0)
	if n.successes.Add(1) >= int32(threshold) {
		n.healthy.Store(true)
	}
	n.setCheck(nil)
}

// setCheck
func (n *node) setCheck(err error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.lastChecked = time.Now()
	if err == nil {
		n.lastError = ""
	} else {
		n.lastError = err.Error()
	}
}

// NodeHealth 节点健康度抽象
type NodeHealth struct {
	Name        string
	Healthy     bool
	Failures    int32
	Successes   int32
	LastError   string
	LastChecked time.Time
}

// snapshot 返回当前节点的健康度快照
func (n *node) snapshot() NodeHealth {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return NodeHealth{Name: n.name, Healthy: n.healthy.Load(), Failures: n.failures.Load(), Successes: n.successes.Load(), LastError: n.lastError, LastChecked: n.lastChecked}
}
