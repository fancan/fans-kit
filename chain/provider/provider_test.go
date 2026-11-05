package provider

import (
	"errors"
	"testing"
	"time"

	"github.com/fancan/fans-kit/chain/config"
)

func TestCircuitBreakerHalfOpenAllowsOneProbe(t *testing.T) {
	ep := &Endpoint{cfg: config.ProviderConfig{Name: "p"}, status: Healthy}
	ep.Record(errors.New("network down"), time.Millisecond, 2, 2, time.Second)
	ep.Record(errors.New("network down"), time.Millisecond, 2, 2, time.Second)
	if ep.Snapshot().Status != Open {
		t.Fatal("expected open circuit")
	}
	ep.mu.Lock()
	ep.openUntil = time.Now().Add(-time.Millisecond)
	ep.mu.Unlock()
	if !ep.Acquire(time.Now()) {
		t.Fatal("first half-open probe should be admitted")
	}
	if ep.Acquire(time.Now()) {
		t.Fatal("concurrent half-open probe should be rejected")
	}
	ep.Record(nil, time.Millisecond, 2, 2, time.Second)
	if !ep.Acquire(time.Now()) {
		t.Fatal("second sequential probe should be admitted")
	}
	ep.Record(nil, time.Millisecond, 2, 2, time.Second)
	if ep.Snapshot().Status != Healthy {
		t.Fatal("expected circuit to recover")
	}
}

func TestPriorityLatencySelectorPrefersHealthyThenPriority(t *testing.T) {
	slowPrimary := &Endpoint{cfg: config.ProviderConfig{Name: "primary", Priority: 1, Weight: 1}, status: Healthy, latency: 100 * time.Millisecond}
	fastBackup := &Endpoint{cfg: config.ProviderConfig{Name: "backup", Priority: 2, Weight: 1}, status: Healthy, latency: 5 * time.Millisecond}
	degraded := &Endpoint{cfg: config.ProviderConfig{Name: "degraded", Priority: 0, Weight: 1}, status: Degraded, latency: time.Millisecond}
	ordered := (PriorityLatencySelector{}).Order([]*Endpoint{fastBackup, degraded, slowPrimary})
	if ordered[0].ID() != "primary" {
		t.Fatalf("priority should break ties among healthy providers, got %s", ordered[0].ID())
	}
	if ordered[2].ID() != "degraded" {
		t.Fatalf("degraded provider should be last, got %s", ordered[2].ID())
	}
}
