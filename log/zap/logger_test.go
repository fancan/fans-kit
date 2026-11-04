package zap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fancan/fans-kit/log/api"
)

// TestDerivedCloseIsNoop 验证派生 Logger（With）的 Close 是无操作：
// 派生 Logger 共享根 Logger 的文件 writer，其 Close 不应关闭共享句柄，
// 之后根 Logger 仍能正常写入。
func TestDerivedCloseIsNoop(t *testing.T) {
	f := filepath.Join(t.TempDir(), "app.log")
	lg, err := New(Config{
		Service: "test",
		Level:   api.DebugLevel,
		Format:  "json",
		Output:  []string{"file"},
		File:    FileConfig{Filename: f},
	})
	if err != nil {
		t.Fatal(err)
	}

	derived := lg.With(api.String("m", "pay"))
	if err := derived.Close(); err != nil {
		t.Fatalf("derived Close should be no-op: %v", err)
	}

	// 派生 Logger Close 后，根 Logger 仍可正常写入。
	lg.Info("still-works")

	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(strings.Split(strings.TrimSpace(string(data)), "\n")); got != 1 {
		t.Fatalf("expected 1 log line, got %d: %q", got, data)
	}
}

// TestSeparateLevelFilesValidation 验证 SeparateLevelFiles 的配置校验逻辑：
//   - 开启分级文件但 Output 无 "file" 时返回错误；
//   - Output 含 "file" 但未设置 Filename 时返回错误；
//   - 配置合法时成功创建 Logger。
func TestSeparateLevelFilesValidation(t *testing.T) {
	t.Run("no file output", func(t *testing.T) {
		_, err := New(Config{
			Service:            "t",
			Level:              api.InfoLevel,
			Output:             []string{"stdout"},
			SeparateLevelFiles: true,
		})
		if err == nil {
			t.Fatal("expected error when separate files enabled without file output")
		}
	})

	t.Run("missing filename", func(t *testing.T) {
		_, err := New(Config{
			Service:            "t",
			Level:              api.InfoLevel,
			Output:             []string{"file"},
			SeparateLevelFiles: true,
		})
		if err == nil {
			t.Fatal("expected error when separate files enabled without filename")
		}
	})

	t.Run("valid", func(t *testing.T) {
		lg, err := New(Config{
			Service:            "t",
			Level:              api.DebugLevel,
			Output:             []string{"file"},
			SeparateLevelFiles: true,
			File: FileConfig{
				Filename: filepath.Join(t.TempDir(), "app.log"),
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if lg == nil {
			t.Fatal("expected logger")
		}
		_ = lg.Close()
	})
}
