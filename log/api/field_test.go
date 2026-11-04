package api

import (
	"math"
	"testing"
	"time"
)

// TestFieldConstructors 验证各字段构造器写入紧凑 Field 的 Key/Type 与值
// （Integer/String/Interface）是否正确，重点覆盖：
//   - Uint64 以位模式存入 Integer 并能无损还原；
//   - Float64 以 math.Float64bits 位模式存入 Integer 并能无损还原；
//   - Bool/Duration 以整型编码；
//   - Time 完整存入 Interface；
//   - NamedError(nil) 的 Interface 保持 nil。
func TestFieldConstructors(t *testing.T) {
	if f := String("k", "v"); f.Key != "k" || f.Type != StringType || f.String != "v" {
		t.Errorf("String = %+v", f)
	}
	if f := Int("k", -7); f.Integer != -7 || f.Type != IntType {
		t.Errorf("Int = %+v", f)
	}
	if f := Int64("k", math.MaxInt64); f.Integer != math.MaxInt64 || f.Type != Int64Type {
		t.Errorf("Int64 = %+v", f)
	}
	if f := Uint64("k", math.MaxUint64); uint64(f.Integer) != math.MaxUint64 || f.Type != Uint64Type {
		t.Errorf("Uint64 = %+v", f)
	}
	if f := Float64("k", 3.5); math.Float64frombits(uint64(f.Integer)) != 3.5 || f.Type != Float64Type {
		t.Errorf("Float64 = %+v", f)
	}
	if f := Bool("k", true); f.Integer != 1 || f.Type != BoolType {
		t.Errorf("Bool(true) = %+v", f)
	}
	if f := Bool("k", false); f.Integer != 0 || f.Type != BoolType {
		t.Errorf("Bool(false) = %+v", f)
	}
	if f := Duration("k", time.Second); f.Integer != int64(time.Second) || f.Type != DurationType {
		t.Errorf("Duration = %+v", f)
	}

	now := time.Now()
	if f := Time("k", now); f.Type != TimeType {
		t.Errorf("Time = %+v", f)
	} else if got, ok := f.Interface.(time.Time); !ok || !got.Equal(now) {
		t.Errorf("Time value = %v", f.Interface)
	}

	if f := NamedError("k", nil); f.Type != ErrorType || f.Interface != nil {
		t.Errorf("NamedError(nil) = %+v", f)
	}
	if f := Any("k", 42); f.Type != AnyType || f.Interface != 42 {
		t.Errorf("Any = %+v", f)
	}
}
