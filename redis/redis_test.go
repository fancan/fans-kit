package redis

import (
	"errors"
	"testing"
	"time"
)

func TestConfigValidation(t *testing.T) {
	_, err := (Config{Mode: ModeStandalone}).normalized()
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got %v", err)
	}

	_, err = (Config{Mode: ModeCluster, Addrs: []string{"127.0.0.1:7000"}, DB: 1}).normalized()
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected cluster DB validation error, got %v", err)
	}

	cfg, err := (Config{Addrs: []string{"127.0.0.1:6379"}}).normalized()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != ModeStandalone || cfg.PoolSize <= 0 || cfg.DialTimeout <= 0 {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
}

func TestConfigRetryAndCopySemantics(t *testing.T) {
	addrs := []string{"127.0.0.1:6379"}
	cfg, err := (Config{Addrs: addrs, MaxRetries: -1}).normalized()
	if err != nil {
		t.Fatal(err)
	}
	addrs[0] = "changed"
	if cfg.Addrs[0] != "127.0.0.1:6379" {
		t.Fatal("normalized config must copy address slice")
	}
	if cfg.MaxRetries != -1 {
		t.Fatalf("expected retries disabled, got %d", cfg.MaxRetries)
	}
	if _, err := (Config{Addrs: []string{"127.0.0.1:6379"}, MaxRetries: -2}).normalized(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected invalid retries, got %v", err)
	}
}

func TestMetricsSnapshotAndBoundedLabels(t *testing.T) {
	m := newMetrics()
	m.observe("get", time.Millisecond, nil)
	m.observe("GET", 10*time.Millisecond, Nil)
	m.observe("customer_defined_command", time.Second, errors.New("boom"))
	s := m.snapshot()
	if s.Commands != 3 || s.Errors != 1 {
		t.Fatalf("unexpected totals: %+v", s)
	}
	if s.ByCommand["get"].Count != 2 || s.ByCommand["get"].Errors != 0 {
		t.Fatalf("unexpected get metrics: %+v", s.ByCommand["get"])
	}
	if s.ByCommand["other"].Count != 1 {
		t.Fatalf("unknown command should be grouped: %+v", s.ByCommand)
	}
	// 快照必须自带桶边界，且每个命令的桶计数比边界多 1（+Inf）。
	if len(s.Buckets) != len(latencyBuckets) {
		t.Fatalf("snapshot buckets = %d, want %d", len(s.Buckets), len(latencyBuckets))
	}
	for cmd, v := range s.ByCommand {
		if len(v.Buckets) != len(s.Buckets)+1 {
			t.Fatalf("command %q bucket len = %d, want %d", cmd, len(v.Buckets), len(s.Buckets)+1)
		}
		var total uint64
		for _, b := range v.Buckets {
			total += b
		}
		if total != v.Count {
			t.Fatalf("command %q bucket sum = %d, want %d", cmd, total, v.Count)
		}
	}
}

// TestResolveDefaultName 覆盖默认实例的归属规则，重点是「具名实例作为唯一实例时
// 必须自动成为默认实例」——否则 redis.Get/Set 等包级函数全部不可用。
func TestResolveDefaultName(t *testing.T) {
	newM := func() *manager {
		return &manager{instances: map[string]*Instance{}, defaultName: defaultName}
	}

	// 场景 1：InitNamed("cache") 作为唯一实例，应自动补位为默认实例。
	m := newM()
	m.instances["cache"] = &Instance{name: "cache"}
	m.resolveDefaultName("cache", false)
	if m.defaultName != "cache" {
		t.Fatalf("sole named instance should become default, got %q", m.defaultName)
	}

	// 场景 2：已有默认实例时，新增具名实例不得抢占。
	m = newM()
	m.instances["default"] = &Instance{name: "default"}
	m.defaultName = "default"
	m.instances["cache"] = &Instance{name: "cache"}
	m.resolveDefaultName("cache", false)
	if m.defaultName != "default" {
		t.Fatalf("existing default must be kept, got %q", m.defaultName)
	}

	// 场景 3：显式 AsDefault 必须强制覆盖。
	m.resolveDefaultName("cache", true)
	if m.defaultName != "cache" {
		t.Fatalf("makeDefault should override, got %q", m.defaultName)
	}
}
