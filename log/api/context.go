package api

// log组件的对外api层封装
// 定义log组件的标准Context字段提取

import "context"

// contextKey 是标准context.Value的键类型。
// 使用未导出的整型类型（而非字符串），理由有二：
//  1. 整型值可内联到interface数据字而不触发堆分配（字符串key每次ctx.Value调用都会装箱一次）；
//  2. 类型本身即命名空间，天然避免与业务自定义字符串key碰撞。
type contextKey int

// 标准context链路字段key（配合context.WithValue使用）。
const (
	LogIDKey contextKey = iota + 1
	ReqIDKey
	RefIDKey
	TraceIDKey
	SpanIDKey
)

// 供Gin等基于字符串key的框架使用的key常量（配合c.Set / c.Get使用）。
const (
	LogIDKeyString   = "LOGID"
	ReqIDKeyString   = "REQID"
	RefIDKeyString   = "REFID"
	TraceIDKeyString = "TRACEID"
	SpanIDKeyString  = "SPANID"
)

// ContextGetter 兼容Gin等框架的Get方法。
type ContextGetter interface {
	Get(string) (any, bool)
}

type contextKeySpec struct {
	key      contextKey
	fieldKey string
}

// 固定为包级变量
var contextKeys = [...]contextKeySpec{
	{LogIDKey, "logid"},
	{ReqIDKey, "reqid"},
	{RefIDKey, "refid"},
	{TraceIDKey, "traceid"},
	{SpanIDKey, "spanid"},
}

type stringKeySpec struct {
	key      string
	fieldKey string
}

var contextGetterKeys = [...]stringKeySpec{
	{LogIDKeyString, "logid"},
	{ReqIDKeyString, "reqid"},
	{RefIDKeyString, "refid"},
	{TraceIDKeyString, "traceid"},
	{SpanIDKeyString, "spanid"},
}

// 返回一些常用log字段
func ContextFields(ctx context.Context) []Field {
	if ctx == nil {
		return nil
	}

	var fields []Field

	for i := range contextKeys {
		value := ctx.Value(contextKeys[i].key)
		if value == nil {
			continue
		}

		// 惰性分配：命中字段才分配切片，空 Context 路径零分配。
		if fields == nil {
			fields = make([]Field, 0, len(contextKeys))
		}
		fields = append(fields, Any(contextKeys[i].fieldKey, value))
	}

	return fields
}

// 返回一些通过Get()接口拿到的log字段
func ContextGetterFields(ctx ContextGetter) []Field {
	if ctx == nil {
		return nil
	}

	var fields []Field

	for i := range contextGetterKeys {
		value, ok := ctx.Get(contextGetterKeys[i].key)
		if !ok || value == nil {
			continue
		}

		if fields == nil {
			fields = make([]Field, 0, len(contextGetterKeys))
		}
		fields = append(fields, Any(contextGetterKeys[i].fieldKey, value))
	}

	return fields
}
