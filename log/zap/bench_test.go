package zap

import (
	"testing"
	"time"

	"github.com/fancan/fans-kit/log/api"
)

// BenchmarkConvertFields 测量 api.Field -> zap.Field 的转换成本（含 []zap.Field 切片
// 分配），这是本层在“命中路径”上相对原生 zap 额外引入的主要开销。
func BenchmarkConvertFields(b *testing.B) {
	fields := []api.Field{
		api.String("s", "v"),
		api.Int("i", 1),
		api.Float64("f", 3.5),
		api.Bool("b", true),
		api.Duration("d", time.Second),
		api.Any("any", "x"),
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		_ = convertFields(fields)
	}
}

// BenchmarkConvertFieldsEmpty 测量空字段切片的转换成本：应短路返回 nil，零分配。
func BenchmarkConvertFieldsEmpty(b *testing.B) {
	var fields []api.Field

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		_ = convertFields(fields)
	}
}
