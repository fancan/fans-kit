package log

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fancan/fans-kit/log/api"
)

// resetGlobal 将全局 Logger 重置为 nil，使后续 Global()/facade() 落到兜底 Logger，
// 避免测试之间通过全局状态相互污染。
func resetGlobal() {
	global.Store(nil)
}

// newFileLogger 创建一个写入指定文件的 JSON 文件 Logger（开启 Caller、Debug 级别），
// 用于在测试中捕获日志输出并断言结构化字段。
func newFileLogger(t *testing.T, filename string) Logger {
	t.Helper()
	lg, err := New(Config{
		Service: "test",
		Level:   DebugLevel,
		Format:  "json",
		Output:  []string{"file"},
		Caller:  true,
		File:    FileConfig{Filename: filename},
	})
	if err != nil {
		t.Fatal(err)
	}
	return lg
}

// readJSONLines 读取日志文件并按行解析为 JSON map，便于对结构化字段逐项断言。
func readJSONLines(t *testing.T, filename string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}

	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("unmarshal %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

// mockLogger 只覆盖 SetGlobal 用到的 Close / WithCallerSkip，其余方法由内嵌接口提供。
type mockLogger struct {
	api.Logger
	closed bool
}

func (m *mockLogger) WithCallerSkip(int) api.Logger { return m }
func (m *mockLogger) Close() error                  { m.closed = true; return nil }

// TestCallerAnnotationFacade 验证全局门面函数 log.Info() 的 caller 注解
// 指向业务调用点（本测试文件），而非模块内部代码（zap/logger.go、log/log.go）。
func TestCallerAnnotationFacade(t *testing.T) {
	resetGlobal()

	f := filepath.Join(t.TempDir(), "app.log")
	lg := newFileLogger(t, f)
	if err := SetGlobal(lg); err != nil {
		t.Fatal(err)
	}

	Info("facade-call") // 期望 caller 指向这一行（本测试文件），而非模块内部
	if err := Sync(); err != nil {
		t.Logf("sync err (ignored): %v", err)
	}
	_ = lg.Close()

	lines := readJSONLines(t, f)
	if len(lines) != 1 {
		t.Fatalf("expected 1 log line, got %d", len(lines))
	}

	caller, _ := lines[0]["caller"].(string)
	if strings.Contains(caller, "zap/logger.go") || strings.Contains(caller, "log/log.go") {
		t.Fatalf("caller points to internal code: %q", caller)
	}
	if !strings.Contains(caller, "log_test.go") {
		t.Fatalf("caller does not point to test file: %q", caller)
	}
}

// TestCallerAnnotationDirectMethod 验证直接方法调用 logger.Info() 的 caller 注解
// 同样指向业务调用点，而非模块内部代码。
func TestCallerAnnotationDirectMethod(t *testing.T) {
	f := filepath.Join(t.TempDir(), "app.log")
	lg := newFileLogger(t, f)

	lg.Info("direct-call") // 直接方法调用，caller 应指向这一行
	_ = lg.Close()

	lines := readJSONLines(t, f)
	caller, _ := lines[0]["caller"].(string)
	if strings.Contains(caller, "zap/logger.go") {
		t.Fatalf("caller points to internal code: %q", caller)
	}
	if !strings.Contains(caller, "log_test.go") {
		t.Fatalf("caller does not point to test file: %q", caller)
	}
}

// assertCallerPointsToTestFile 断言 caller 注解指向本测试文件，而非模块内部实现。
func assertCallerPointsToTestFile(t *testing.T, caller string) {
	t.Helper()
	if strings.Contains(caller, "zap/logger.go") || strings.Contains(caller, "log/log.go") {
		t.Fatalf("caller points to internal code: %q", caller)
	}
	if !strings.Contains(caller, "log_test.go") {
		t.Fatalf("caller does not point to test file: %q", caller)
	}
}

// TestCallerAnnotationInfof 验证格式化门面函数 log.Infof() 的 caller 注解
// 同样指向业务调用点（格式化方法内部不再额外经过一层普通方法）。
func TestCallerAnnotationInfof(t *testing.T) {
	resetGlobal()

	f := filepath.Join(t.TempDir(), "app.log")
	lg := newFileLogger(t, f)
	if err := SetGlobal(lg); err != nil {
		t.Fatal(err)
	}

	Infof("fmt-call %d", 1) // 期望 caller 指向这一行
	if err := Sync(); err != nil {
		t.Logf("sync err (ignored): %v", err)
	}
	_ = lg.Close()

	lines := readJSONLines(t, f)
	caller, _ := lines[0]["caller"].(string)
	assertCallerPointsToTestFile(t, caller)
}

// TestCallerAnnotationInfoContext 验证 Context 门面函数 log.InfoContext() 的 caller 注解
// 指向业务调用点（Context 字段合并在 write 之前完成，不额外占用调用栈帧）。
func TestCallerAnnotationInfoContext(t *testing.T) {
	resetGlobal()

	f := filepath.Join(t.TempDir(), "app.log")
	lg := newFileLogger(t, f)
	if err := SetGlobal(lg); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	InfoContext(ctx, "ctx-call") // 期望 caller 指向这一行
	if err := Sync(); err != nil {
		t.Logf("sync err (ignored): %v", err)
	}
	_ = lg.Close()

	lines := readJSONLines(t, f)
	caller, _ := lines[0]["caller"].(string)
	assertCallerPointsToTestFile(t, caller)
}

// TestSetGlobalClosesOldLogger 验证 SetGlobal 重复调用时会关闭被替换的旧 Logger，
// 且不会关闭当前正在使用的新 Logger。
func TestSetGlobalClosesOldLogger(t *testing.T) {
	resetGlobal()

	m1 := &mockLogger{}
	m2 := &mockLogger{}

	if err := SetGlobal(m1); err != nil {
		t.Fatal(err)
	}
	if err := SetGlobal(m2); err != nil {
		t.Fatal(err)
	}

	if !m1.closed {
		t.Fatal("replaced logger was not closed")
	}
	if m2.closed {
		t.Fatal("current logger should not be closed")
	}
}

// TestSetGlobalSameLoggerNotClosed 验证对同一 Logger 重复 SetGlobal 时不会误关闭它。
func TestSetGlobalSameLoggerNotClosed(t *testing.T) {
	resetGlobal()

	m := &mockLogger{}
	_ = SetGlobal(m)
	_ = SetGlobal(m)

	if m.closed {
		t.Fatal("same logger should not be closed on re-set")
	}
}

// TestFieldRoundTrip 端到端验证所有结构化字段类型经 api.Field -> zap.Field -> JSON 编码
// 后的值正确，覆盖 Uint64 溢出边界、Float64 位模式、Time 时间还原，
// 以及 nil error 字段被忽略等关键分支。
func TestFieldRoundTrip(t *testing.T) {
	f := filepath.Join(t.TempDir(), "app.log")
	lg := newFileLogger(t, f)

	now := time.Unix(1700000000, 123456789).UTC()

	lg.Info("fields",
		String("s", "v"),
		Int("i", 42),
		Int64("i64", int64(1)<<40),
		Uint64("u", math.MaxUint64),
		Float64("f", 3.5),
		Bool("b", true),
		Duration("d", time.Second),
		Time("t", now),
		Err(nil), // 应与 zap 一致被忽略
		Any("any", 7),
	)
	_ = lg.Close()

	lines := readJSONLines(t, f)
	if len(lines) != 1 {
		t.Fatalf("expected 1 log line, got %d", len(lines))
	}
	m := lines[0]

	if m["s"] != "v" {
		t.Errorf("s = %v", m["s"])
	}
	if m["i"] != float64(42) {
		t.Errorf("i = %v", m["i"])
	}
	if m["i64"] != float64(int64(1)<<40) {
		t.Errorf("i64 = %v", m["i64"])
	}
	if m["u"] != float64(math.MaxUint64) {
		t.Errorf("u = %v", m["u"])
	}
	if m["f"] != 3.5 {
		t.Errorf("f = %v", m["f"])
	}
	if m["b"] != true {
		t.Errorf("b = %v", m["b"])
	}
	if m["d"] != "1s" {
		t.Errorf("d = %v", m["d"])
	}

	gotTime, err := time.Parse(time.RFC3339Nano, m["t"].(string))
	if err != nil {
		t.Fatalf("parse time %v: %v", m["t"], err)
	}
	if !gotTime.Equal(now) {
		t.Errorf("t = %v, want %v", gotTime, now)
	}

	if _, exists := m["error"]; exists {
		t.Error("nil error field should be dropped")
	}
	if m["any"] != float64(7) {
		t.Errorf("any = %v", m["any"])
	}
}
