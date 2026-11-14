// Package config 负责加载 Gvboard 的基础设施配置（数据库、Redis、HTTP 服务等）。
// 业务设置（站点名、邮件、支付等）存放在数据库中，不在此处。
package config

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// 部署模式
const (
	ModeAll = "all" // 不分离部署：API + 前端
	ModeAPI = "api" // 分离部署：仅 API
)

// 数据库驱动
const (
	DriverMySQL    = "mysql"
	DriverPostgres = "postgres"
)

type Config struct {
	App       AppConfig       `mapstructure:"app"`
	Server    ServerConfig    `mapstructure:"server"`
	API       APIConfig       `mapstructure:"api"`
	Database  DatabaseConfig  `mapstructure:"database"`
	Redis     RedisConfig     `mapstructure:"redis"`
	Log       LogConfig       `mapstructure:"log"`
	CORS      CORSConfig      `mapstructure:"cors"`
	Compat    CompatConfig    `mapstructure:"compat"`
	Node      NodeConfig      `mapstructure:"node"`
	Worker    WorkerConfig    `mapstructure:"worker"`
	Scheduler SchedulerConfig `mapstructure:"scheduler"`
	GeoIP     GeoIPConfig     `mapstructure:"geoip"`
}

// GeoIPConfig 离线 IP 库（ip2region xdb 文件），用于 Telegram 工单提醒中的用户位置（决策 D50）。
// 为空或文件不可用时位置显示为未知。
type GeoIPConfig struct {
	IPv4 string `mapstructure:"ip2region_v4"`
	IPv6 string `mapstructure:"ip2region_v6"`
}

type AppConfig struct {
	// Key 用于 JWT 签名等。接入原生产库时填写原项目 .env 中的 APP_KEY（含 "base64:" 前缀）。
	Key   string `mapstructure:"key"`
	Debug bool   `mapstructure:"debug"`
	// Timezone 业务时区，流量重置、统计等按此时区计算。
	Timezone string `mapstructure:"timezone"`
	// Locale 默认语言，请求未携带 Content-Language 或语言不受支持时使用。
	Locale string `mapstructure:"locale"`
}

type ServerConfig struct {
	Listen         string        `mapstructure:"listen"`
	Mode           string        `mapstructure:"mode"`
	TrustedProxies []string      `mapstructure:"trusted_proxies"`
	ReadTimeout    time.Duration `mapstructure:"read_timeout"`
	WriteTimeout   time.Duration `mapstructure:"write_timeout"`
	IdleTimeout    time.Duration `mapstructure:"idle_timeout"`
	// NotFoundPage 自定义 404 页面文件路径；为空时使用内置页面。
	NotFoundPage string `mapstructure:"not_found_page"`
	// ThemeDir 外部主题目录，每个子目录是一个主题；与内置主题同名时外部优先。
	ThemeDir string `mapstructure:"theme_dir"`
	// MaxBodyBytes 请求体大小上限。
	MaxBodyBytes int64 `mapstructure:"max_body_bytes"`
}

type APIConfig struct {
	// Prefix 原生 API 前缀，例如 /api/k3f9x2。
	Prefix string `mapstructure:"prefix"`
}

type DatabaseConfig struct {
	Driver          string        `mapstructure:"driver"`
	Host            string        `mapstructure:"host"`
	Port            int           `mapstructure:"port"`
	Name            string        `mapstructure:"name"`
	User            string        `mapstructure:"user"`
	Password        string        `mapstructure:"password"`
	Charset         string        `mapstructure:"charset"`
	SSLMode         string        `mapstructure:"sslmode"`
	MaxOpenConns    int           `mapstructure:"max_open_conns"`
	MaxIdleConns    int           `mapstructure:"max_idle_conns"`
	ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime"`
	SlowThreshold   time.Duration `mapstructure:"slow_threshold"`
}

type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
	// Prefix 缓存键前缀，避免与同一 Redis 中的其它应用冲突。
	Prefix   string `mapstructure:"prefix"`
	PoolSize int    `mapstructure:"pool_size"`
}

type LogConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"` // json / console
	File   string `mapstructure:"file"`   // 为空时输出到标准输出
}

type CORSConfig struct {
	// AllowOrigins 分离部署时允许的前端来源，例如 https://user.example.com。
	AllowOrigins []string `mapstructure:"allow_origins"`
}

type CompatConfig struct {
	V2boardAPI      bool `mapstructure:"v2board_api"`
	LegacySubscribe bool `mapstructure:"legacy_subscribe"`
	LegacyCallback  bool `mapstructure:"legacy_callback"`
}

type NodeConfig struct {
	// IPWhitelist 节点通信接口来源 IP 白名单（支持 CIDR），为空不限制。
	IPWhitelist []string `mapstructure:"ip_whitelist"`
}

// WorkerConfig 队列任务处理。多实例部署时可只在部分实例开启。
type WorkerConfig struct {
	Enabled     bool `mapstructure:"enabled"`
	Concurrency int  `mapstructure:"concurrency"`
}

// SchedulerConfig 定时任务。多实例同时开启时通过 Redis 锁保证同一任务只执行一次。
type SchedulerConfig struct {
	Enabled bool `mapstructure:"enabled"`
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("app.debug", false)
	v.SetDefault("app.timezone", "Asia/Shanghai")
	v.SetDefault("app.locale", "zh-CN")

	v.SetDefault("server.listen", "0.0.0.0:8080")
	v.SetDefault("server.mode", ModeAll)
	v.SetDefault("server.trusted_proxies", []string{"127.0.0.1", "::1"})
	v.SetDefault("server.read_timeout", "30s")
	v.SetDefault("server.write_timeout", "60s")
	v.SetDefault("server.idle_timeout", "120s")
	v.SetDefault("server.max_body_bytes", 8<<20)
	v.SetDefault("server.theme_dir", "themes")

	v.SetDefault("database.driver", DriverMySQL)
	v.SetDefault("database.host", "127.0.0.1")
	v.SetDefault("database.port", 3306)
	v.SetDefault("database.charset", "utf8mb4")
	v.SetDefault("database.sslmode", "disable")
	v.SetDefault("database.max_open_conns", 100)
	v.SetDefault("database.max_idle_conns", 20)
	v.SetDefault("database.conn_max_lifetime", "1h")
	v.SetDefault("database.slow_threshold", "500ms")

	v.SetDefault("redis.addr", "127.0.0.1:6379")
	v.SetDefault("redis.db", 0)
	v.SetDefault("redis.prefix", "gvb:")
	v.SetDefault("redis.pool_size", 50)

	v.SetDefault("log.level", "info")
	v.SetDefault("log.format", "console")

	v.SetDefault("compat.v2board_api", false)
	v.SetDefault("compat.legacy_subscribe", false)
	v.SetDefault("compat.legacy_callback", false)

	v.SetDefault("worker.enabled", true)
	v.SetDefault("worker.concurrency", 10)
	v.SetDefault("scheduler.enabled", true)
}

// EnvPrefix 环境变量前缀，例如 GVB_DATABASE_PASSWORD 覆盖 database.password。
const EnvPrefix = "GVB"

// Load 从指定文件加载配置，环境变量优先级高于文件。
func Load(path string) (*Config, error) {
	v := viper.New()
	setDefaults(v)
	v.SetConfigFile(path)
	v.SetEnvPrefix(EnvPrefix)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}
	cfg.API.Prefix = NormalizePrefix(cfg.API.Prefix)
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// NormalizePrefix 统一为以 "/" 开头、不以 "/" 结尾的形式。
func NormalizePrefix(p string) string {
	p = strings.TrimSpace(p)
	p = strings.Trim(p, "/")
	if p == "" {
		return ""
	}
	return "/" + p
}

var prefixPattern = regexp.MustCompile(`^(/[A-Za-z0-9_-]+)+$`)

// 原项目使用的路径前缀，原生 API 不得落在其下，否则会与兼容层/节点接口冲突。
var reservedPrefixes = []string{"/api/v1", "/api/v2"}

func (c *Config) Validate() error {
	var errs []error

	if len(c.App.Key) < 16 {
		errs = append(errs, errors.New("app.key 长度不能少于 16 位"))
	}
	if _, err := time.LoadLocation(c.App.Timezone); err != nil {
		errs = append(errs, fmt.Errorf("app.timezone 无效: %s", c.App.Timezone))
	}

	if c.Worker.Enabled && (c.Worker.Concurrency < 1 || c.Worker.Concurrency > 1000) {
		errs = append(errs, errors.New("worker.concurrency 必须在 1～1000 之间"))
	}

	if c.Server.Mode != ModeAll && c.Server.Mode != ModeAPI {
		errs = append(errs, fmt.Errorf("server.mode 只能是 %s 或 %s", ModeAll, ModeAPI))
	}

	if c.API.Prefix == "" || !prefixPattern.MatchString(c.API.Prefix) {
		errs = append(errs, errors.New("api.prefix 不能为空，且只能包含字母、数字、下划线、短横线"))
	}
	for _, r := range reservedPrefixes {
		if c.API.Prefix == r || strings.HasPrefix(c.API.Prefix, r+"/") {
			errs = append(errs, fmt.Errorf("api.prefix 不能使用保留前缀 %s", r))
		}
	}

	switch c.Database.Driver {
	case DriverMySQL, DriverPostgres:
	default:
		errs = append(errs, fmt.Errorf("database.driver 只能是 %s 或 %s", DriverMySQL, DriverPostgres))
	}
	if c.Database.Name == "" || c.Database.User == "" {
		errs = append(errs, errors.New("database.name 与 database.user 不能为空"))
	}

	for _, o := range c.CORS.AllowOrigins {
		if o == "*" {
			errs = append(errs, errors.New("cors.allow_origins 不允许使用 *，请填写具体域名"))
			continue
		}
		u, err := url.Parse(o)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Errorf("cors.allow_origins 中的地址无效: %s", o))
		}
	}

	return errors.Join(errs...)
}

// IsSeparated 是否为分离部署（仅 API）。
func (c *Config) IsSeparated() bool { return c.Server.Mode == ModeAPI }
