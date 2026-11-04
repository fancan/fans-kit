package api

// log组件的对外api层封装
// 定义log组件的对外可使用结构化字段

import (
	"math"
	"time"
)

type FieldType uint8

// 把一些常用的结构化字段定义出来收口 减少类型断言
const (
	StringType FieldType = iota
	IntType
	Int64Type
	Uint64Type
	Float64Type
	BoolType
	DurationType
	TimeType
	ErrorType
	AnyType
)

// 结构化字段。
// 采用紧凑的联合体布局（对齐 zapcore.Field 的思路），
// 数值类字段（Int/Int64/Uint64/Float64/Bool/Duration）共用 Integer，
// 字符串共用 String，复杂值（Error/Any/Time）共用 Interface。
// 相比为每种类型都保留独立字段，可把 Field 从 136 字节压缩到 64 字节，
// 从而降低变参切片分配与拷贝成本。
type Field struct {
	Key  string
	Type FieldType

	Integer   int64
	String    string
	Interface any
}

func String(key, value string) Field {
	return Field{
		Key:    key,
		Type:   StringType,
		String: value,
	}
}

func Int(key string, value int) Field {
	return Field{
		Key:     key,
		Type:    IntType,
		Integer: int64(value),
	}
}

func Int64(key string, value int64) Field {
	return Field{
		Key:     key,
		Type:    Int64Type,
		Integer: value,
	}
}

func Uint64(key string, value uint64) Field {
	// int64/uint64 相互转换保持位模式不变，编码侧按 Uint64Type 还原。
	return Field{
		Key:     key,
		Type:    Uint64Type,
		Integer: int64(value),
	}
}

func Float64(key string, value float64) Field {
	// 用位模式存储 float64，避免引入 float64 字段；编码侧还原。
	return Field{
		Key:     key,
		Type:    Float64Type,
		Integer: int64(math.Float64bits(value)),
	}
}

func Bool(key string, value bool) Field {
	var i int64
	if value {
		i = 1
	}
	return Field{
		Key:     key,
		Type:    BoolType,
		Integer: i,
	}
}

func Duration(key string, value time.Duration) Field {
	return Field{
		Key:     key,
		Type:    DurationType,
		Integer: int64(value),
	}
}

func Time(key string, value time.Time) Field {
	// 完整保存 time.Time，编码侧直接还原，保证任意时间范围都正确。
	return Field{
		Key:       key,
		Type:      TimeType,
		Interface: value,
	}
}

func Err(err error) Field {
	return NamedError("error", err)
}

func NamedError(key string, err error) Field {
	return Field{
		Key:       key,
		Type:      ErrorType,
		Interface: err,
	}
}

func Any(key string, value any) Field {
	return Field{
		Key:       key,
		Type:      AnyType,
		Interface: value,
	}
}
