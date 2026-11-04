package api

import (
	"context"
	"testing"
)

// TestContextFieldsNil 验证传入nil Context时返回nil字段，不产生panic。
func TestContextFieldsNil(t *testing.T) {
	var ctx context.Context
	if fields := ContextFields(ctx); fields != nil {
		t.Fatalf("expected nil, got %v", fields)
	}
}

// TestContextFieldsEmptyNoAlloc 验证空Context提取字段时零堆分配：
// 类型化整型key消除了字符串key每次ctx.Value的装箱分配。
func TestContextFieldsEmptyNoAlloc(t *testing.T) {
	ctx := context.Background()

	allocs := testing.AllocsPerRun(100, func() {
		_ = ContextFields(ctx)
	})
	if allocs != 0 {
		t.Fatalf("expected 0 allocs for empty context, got %v", allocs)
	}
}

// TestContextFieldsValues 验证从标准Context提取到的链路字段key/值及顺序正确：
// 仅命中的字段被返回，且顺序与contextKeys定义顺序一致。
func TestContextFieldsValues(t *testing.T) {
	ctx := context.WithValue(context.Background(), ReqIDKey, "r-1")
	ctx = context.WithValue(ctx, TraceIDKey, "t-1")

	fields := ContextFields(ctx)
	if len(fields) != 2 {
		t.Fatalf("expected 2 fields, got %d: %+v", len(fields), fields)
	}

	// 顺序按 contextKeys 定义：ReqID 在 TraceID 之前。
	if fields[0].Key != "reqid" || fields[0].Interface != "r-1" {
		t.Errorf("fields[0] = %+v", fields[0])
	}
	if fields[1].Key != "traceid" || fields[1].Interface != "t-1" {
		t.Errorf("fields[1] = %+v", fields[1])
	}
}

// TestContextGetterFieldsNil 验证传入nil ContextGetter 时返回nil字段，不产生 panic。
func TestContextGetterFieldsNil(t *testing.T) {
	if fields := ContextGetterFields(nil); fields != nil {
		t.Fatalf("expected nil, got %v", fields)
	}
}

// fakeGetter 是ContextGetter接口的最小实现，用map模拟Gin Context的Get方法。
type fakeGetter map[string]any

func (g fakeGetter) Get(key string) (any, bool) {
	v, ok := g[key]
	return v, ok
}

// TestContextGetterFieldsValues 验证从字符串key 的Getter（Gin 风格）提取到的
// 字段 key/值及顺序正确，且仅命中字段被返回。
func TestContextGetterFieldsValues(t *testing.T) {
	g := fakeGetter{LogIDKeyString: "l-1", RefIDKeyString: "ref-1"}

	fields := ContextGetterFields(g)
	if len(fields) != 2 {
		t.Fatalf("expected 2 fields, got %d: %+v", len(fields), fields)
	}
	if fields[0].Key != "logid" || fields[0].Interface != "l-1" {
		t.Errorf("fields[0] = %+v", fields[0])
	}
	if fields[1].Key != "refid" || fields[1].Interface != "ref-1" {
		t.Errorf("fields[1] = %+v", fields[1])
	}
}
