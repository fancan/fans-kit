package zap

// 这一层就是API和Zap的转换边界。 注意：未来增加字段类型需要修改api/field.go、zap/convert.go

import (
	"math"
	"time"

	"github.com/fancan/fans-kit/log/api"
	uzap "go.uber.org/zap"
)

// 将api层的公共Field转换成Zap自己的Field
func convertFields(fields []api.Field) []uzap.Field {
	if len(fields) == 0 {
		return nil
	}

	result := make([]uzap.Field, 0, len(fields))

	for _, f := range fields {
		if f.Key == "" {
			continue
		}

		switch f.Type {
		case api.StringType:
			result = append(result, uzap.String(f.Key, f.String))

		case api.IntType:
			result = append(result, uzap.Int(f.Key, int(f.Integer)))

		case api.Int64Type:
			result = append(result, uzap.Int64(f.Key, f.Integer))

		case api.Uint64Type:
			result = append(result, uzap.Uint64(f.Key, uint64(f.Integer)))

		case api.Float64Type:
			result = append(
				result,
				uzap.Float64(f.Key, math.Float64frombits(uint64(f.Integer))),
			)

		case api.BoolType:
			result = append(result, uzap.Bool(f.Key, f.Integer == 1))

		case api.DurationType:
			result = append(result, uzap.Duration(f.Key, time.Duration(f.Integer)))

		case api.TimeType:
			if t, ok := f.Interface.(time.Time); ok {
				result = append(result, uzap.Time(f.Key, t))
			}

		case api.ErrorType:
			// 与 zap 一致：nil error 视为 no-op，不输出该字段。
			if f.Interface != nil {
				if err, ok := f.Interface.(error); ok {
					result = append(result, uzap.NamedError(f.Key, err))
				}
			}

		case api.AnyType:
			result = append(result, uzap.Any(f.Key, f.Interface))
		}
	}

	return result
}
