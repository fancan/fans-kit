package config

// 解析chain模块的配置，使用者根据example.yaml的配置配置，会按照规则解析出来

import (
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration 是对time.Duration的包装，支持在YAML中写作"10s"这样的字符串，
// 或写作纳秒整数。
type Duration time.Duration

// UnmarshalYAML 让 Duration 既兼容字符串（time.ParseDuration）也兼容整数（纳秒）。
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	if n.Tag == "!!int" {
		var ns int64
		if err := n.Decode(&ns); err != nil {
			return err
		}
		*d = Duration(time.Duration(ns))
		return nil
	}
	var v string
	if err := n.Decode(&v); err != nil {
		return err
	}
	x, err := time.ParseDuration(v)
	if err != nil {
		return err
	}
	*d = Duration(x)
	return nil
}

// Value 返回标准time.Duration。
func (d Duration) Value() time.Duration { return time.Duration(d) }

// Config 是链模块的顶层配置。
type Config struct {
	Default string                 `yaml:"default"` // 默认链名称
	Chains  map[string]ChainConfig `yaml:"chains"`  // 各链配置
	Metrics MetricsConfig          `yaml:"metrics"` // Prometheus 指标配置
}

// ChainConfig 是一条链的配置。
type ChainConfig struct {
	ChainID   uint64           `yaml:"chain_id"`
	Providers []ProviderConfig `yaml:"providers"`
	HTTP      HTTPConfig       `yaml:"http"`
	WebSocket WSConfig         `yaml:"websocket"`
	Health    HealthConfig     `yaml:"health_check"`
	Block     BlockConfig      `yaml:"block_monitor"`
	Recovery  RecoveryConfig   `yaml:"log_recovery"`
	Multicall MulticallConfig  `yaml:"multicall"`
}

// ProviderConfig 是单个RPC提供商的配置，可同时含 HTTP 与 WS 地址。
type ProviderConfig struct {
	Name         string            `yaml:"name"`
	Priority     int               `yaml:"priority"` // 优先级（越小越优先）
	Weight       int               `yaml:"weight"`   // 权重（延迟按权重折算）
	HTTPURL      string            `yaml:"http_url"`
	WSURL        string            `yaml:"ws_url"`
	APIKey       string            `yaml:"api_key"`
	APIKeyHeader string            `yaml:"api_key_header"` // APIKey 注入的自定义请求头名
	Headers      map[string]string `yaml:"headers"`        // 额外请求头
}

// HTTPConfig 是HTTP RPC的配置。
type HTTPConfig struct {
	Timeout      Duration `yaml:"timeout"`        // 单次调用超时
	MaxRetries   int      `yaml:"max_retries"`    // 最大重试次数
	MaxBatchSize int      `yaml:"max_batch_size"` // 批处理最大元素数
}

// WSConfig 是WebSocket的配置。
type WSConfig struct {
	ReconnectInterval Duration `yaml:"reconnect_interval"` // 重连间隔
	SendBuffer        int      `yaml:"send_buffer"`        // 日志发送缓冲
}

// HealthConfig 开启健康检查的配置。
type HealthConfig struct {
	Enabled          bool     `yaml:"enabled"`           // 是否可用
	Interval         Duration `yaml:"interval"`          // 端点健康检查间隔
	Timeout          Duration `yaml:"timeout"`           // 超时时间
	FailureThreshold int      `yaml:"failure_threshold"` // 连续失败多少次触发熔断
	SuccessThreshold int      `yaml:"success_threshold"` // 连续成功多少次恢复
	Cooldown         Duration `yaml:"cooldown"`          // 熔断冷却时长
}

// BlockConfig 开启区块监控的配置。
type BlockConfig struct {
	Enabled          bool     `yaml:"enabled"`           // 是否可用
	Interval         Duration `yaml:"interval"`          // 区块监控间隔
	MaxLag           uint64   `yaml:"max_lag"`           // 允许的最大落后区块数
	ConsistencyDepth uint64   `yaml:"consistency_depth"` // 一致性比较的确认深度
}

// RecoveryConfig 是日志恢复（重放/去重/重组回退）的配置。
type RecoveryConfig struct {
	Enabled       bool   `yaml:"enabled"`        // 是否可用
	MaxRange      uint64 `yaml:"max_range"`      // eth_getLogs单段最大区间
	DedupCapacity int    `yaml:"dedup_capacity"` // 去重集合容量
	ReorgDepth    uint64 `yaml:"reorg_depth"`    // 重组回退窗口
}

// MetricsConfig 是Prometheus指标配置。
type MetricsConfig struct {
	Enabled bool   `yaml:"enabled"` // 是否启用
	Path    string `yaml:"path"`    // metrics路径开启的路径 默认值 /metrics
}

// MulticallConfig 是链上 multicall 合约的配置。
// 不同链部署的 multicall 合约地址不同，按链配置对应地址即可自动切换。
type MulticallConfig struct {
	Address string `yaml:"address"` // multicall 合约地址（0x 前缀）；为空表示未配置
	Type    string `yaml:"type"`    // aggregate / try_aggregate（默认 try_aggregate）
}

// LoadYAML 从文件路径读取并解析YAML配置。
func LoadYAML(path string) (Config, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return Config{}, e
	}
	return ParseYAML(b)
}

// ParseYAML 解析YAML配置字节
// 1、应用默认值
// 2、展开环境变量并做基本校验。
func ParseYAML(b []byte) (Config, error) {
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return c, err
	}
	for name, cc := range c.Chains {
		d := defaults(cc)
		if len(d.Providers) == 0 {
			return c, fmt.Errorf("chain %s: no providers", name)
		}
		for i := range d.Providers {
			p := &d.Providers[i]
			p.HTTPURL = os.ExpandEnv(p.HTTPURL)
			p.WSURL = os.ExpandEnv(p.WSURL)
			p.APIKey = os.ExpandEnv(p.APIKey)
			if p.Headers == nil {
				p.Headers = map[string]string{}
			}
			for k, v := range p.Headers {
				p.Headers[k] = os.ExpandEnv(v)
			}
			if p.APIKey != "" && p.APIKeyHeader != "" {
				p.Headers[p.APIKeyHeader] = p.APIKey
			}
			if p.Name == "" {
				p.Name = fmt.Sprintf("provider-%d", i+1)
			}
			if p.HTTPURL == "" && p.WSURL == "" {
				return c, fmt.Errorf("chain %s provider %s: http_url or ws_url required", name, p.Name)
			}
			if p.Weight <= 0 {
				p.Weight = 1
			}
		}
		c.Chains[name] = d
	}
	if c.Default == "" {
		names := make([]string, 0, len(c.Chains))
		for n := range c.Chains {
			names = append(names, n)
		}
		sort.Strings(names)
		if len(names) > 0 {
			c.Default = names[0]
		}
	}
	if c.Metrics.Path == "" {
		c.Metrics.Path = "/metrics"
	}
	return c, nil
}

// defaults 对链配置应用默认值（副本语义，不修改入参）。
func defaults(c ChainConfig) ChainConfig {
	c.Providers = append([]ProviderConfig(nil), c.Providers...)
	for n := range c.Providers {
		p := &c.Providers[n]
		if p.Name == "" {
			p.Name = fmt.Sprintf("provider-%d", n+1)
		}
		if p.Weight <= 0 {
			p.Weight = 1
		}
		p.HTTPURL = os.ExpandEnv(p.HTTPURL)
		p.WSURL = os.ExpandEnv(p.WSURL)
		p.APIKey = os.ExpandEnv(p.APIKey)
		headers := make(map[string]string, len(p.Headers))
		for k, v := range p.Headers {
			headers[k] = v
		}
		p.Headers = headers
		for k, v := range p.Headers {
			p.Headers[k] = os.ExpandEnv(v)
		}
		if p.APIKey != "" && p.APIKeyHeader != "" {
			p.Headers[p.APIKeyHeader] = p.APIKey
		}
	}
	if c.HTTP.Timeout <= 0 {
		c.HTTP.Timeout = Duration(10 * time.Second)
	}
	if c.HTTP.MaxRetries < 0 {
		c.HTTP.MaxRetries = 0
	}
	if c.HTTP.MaxRetries == 0 {
		c.HTTP.MaxRetries = len(c.Providers) - 1
	}
	if c.HTTP.MaxBatchSize <= 0 {
		c.HTTP.MaxBatchSize = 100
	}
	if c.WebSocket.ReconnectInterval <= 0 {
		c.WebSocket.ReconnectInterval = Duration(2 * time.Second)
	}
	if c.WebSocket.SendBuffer <= 0 {
		c.WebSocket.SendBuffer = 256
	}
	if c.Health.Interval <= 0 {
		c.Health.Interval = Duration(10 * time.Second)
	}
	if c.Health.Timeout <= 0 {
		c.Health.Timeout = Duration(3 * time.Second)
	}
	if c.Health.FailureThreshold <= 0 {
		c.Health.FailureThreshold = 3
	}
	if c.Health.SuccessThreshold <= 0 {
		c.Health.SuccessThreshold = 2
	}
	if c.Health.Cooldown <= 0 {
		c.Health.Cooldown = Duration(20 * time.Second)
	}
	if c.Block.Interval <= 0 {
		c.Block.Interval = Duration(5 * time.Second)
	}
	if c.Block.MaxLag == 0 {
		c.Block.MaxLag = 5
	}
	if c.Block.ConsistencyDepth == 0 {
		c.Block.ConsistencyDepth = 2
	}
	if c.Recovery.MaxRange == 0 {
		c.Recovery.MaxRange = 2000
	}
	if c.Recovery.DedupCapacity <= 0 {
		c.Recovery.DedupCapacity = 100000
	}
	if c.Recovery.ReorgDepth == 0 {
		c.Recovery.ReorgDepth = 64
	}
	if c.Multicall.Type == "" {
		c.Multicall.Type = "try_aggregate"
	}
	return c
}

// Normalized 返回应用默认值后的配置副本。
func (c ChainConfig) Normalized() ChainConfig { return defaults(c) }

// Validate 校验链配置：至少一个提供商、名称唯一、HTTP/WS地址scheme合法。
func (c ChainConfig) Validate() error {
	if len(c.Providers) == 0 {
		return fmt.Errorf("at least one provider required")
	}
	seen := make(map[string]struct{}, len(c.Providers))
	for _, p := range c.Providers {
		if strings.TrimSpace(p.Name) == "" {
			return fmt.Errorf("provider name is required")
		}
		if _, ok := seen[p.Name]; ok {
			return fmt.Errorf("duplicate provider name %q", p.Name)
		}
		seen[p.Name] = struct{}{}
		if strings.TrimSpace(p.HTTPURL) == "" && strings.TrimSpace(p.WSURL) == "" {
			return fmt.Errorf("provider %q has no endpoint", p.Name)
		}
		if p.HTTPURL != "" {
			u, err := url.Parse(p.HTTPURL)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
				return fmt.Errorf("provider %q: http_url must use http or https", p.Name)
			}
		}
		if p.WSURL != "" {
			u, err := url.Parse(p.WSURL)
			if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") {
				return fmt.Errorf("provider %q: ws_url must use ws or wss", p.Name)
			}
		}
	}
	return nil
}

// ValidateProvider 校验单个提供商配置（运行期动态新增时使用）。
func ValidateProvider(p ProviderConfig) error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("provider name is required")
	}
	if p.HTTPURL == "" && p.WSURL == "" {
		return fmt.Errorf("provider %q has no endpoint", p.Name)
	}
	if p.HTTPURL != "" {
		u, err := url.Parse(p.HTTPURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("provider %q: http_url must use http or https", p.Name)
		}
	}
	if p.WSURL != "" {
		u, err := url.Parse(p.WSURL)
		if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") {
			return fmt.Errorf("provider %q: ws_url must use ws or wss", p.Name)
		}
	}
	return nil
}
