package subscription

import "testing"

func TestDeduperBoundedAndForget(t *testing.T) {
	d := NewDeduper(2)
	if d.Seen("a") {
		t.Fatal("first a must be new")
	}
	if !d.Seen("a") {
		t.Fatal("second a must be duplicate")
	}
	d.Forget("a")
	if d.Seen("a") {
		t.Fatal("forgotten a must be new")
	}
	d.Seen("b")
	d.Seen("c") // evicts the oldest a
	if d.Seen("a") {
		t.Fatal("a should have been evicted")
	}
}
