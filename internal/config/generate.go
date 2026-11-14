package config

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"text/template"
)

const randAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

// RandomString 生成指定长度的小写字母数字随机串（crypto/rand）。
func RandomString(n int) (string, error) {
	b := make([]byte, n)
	max := big.NewInt(int64(len(randAlphabet)))
	for i := range b {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = randAlphabet[idx.Int64()]
	}
	return string(b), nil
}

// RandomAppKey 生成 32 字节随机密钥，格式与原项目 APP_KEY 一致，便于互换。
func RandomAppKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "base64:" + base64.StdEncoding.EncodeToString(b), nil
}

// RandomAPIPrefix 生成随机原生 API 前缀。
func RandomAPIPrefix() (string, error) {
	s, err := RandomString(10)
	if err != nil {
		return "", err
	}
	return "/api/" + s, nil
}

type GenerateOptions struct {
	AppKey    string
	APIPrefix string
	Mode      string
	DBDriver  string
	// Migrated 是否为从原项目迁移的站点，迁移站点默认开启旧订阅路径兼容。
	Migrated bool

	// 以下为空时使用默认值（数据库连接信息留空待填写）。
	Listen        string
	DBHost        string
	DBPort        int
	DBName        string
	DBUser        string
	DBPassword    string
	RedisAddr     string
	RedisPassword string
	RedisDB       int
}

// Generate 生成配置文件内容，未指定的密钥与前缀随机生成。
func Generate(opt GenerateOptions) ([]byte, error) {
	var err error
	if opt.AppKey == "" {
		if opt.AppKey, err = RandomAppKey(); err != nil {
			return nil, err
		}
	}
	if opt.APIPrefix == "" {
		if opt.APIPrefix, err = RandomAPIPrefix(); err != nil {
			return nil, err
		}
	}
	opt.APIPrefix = NormalizePrefix(opt.APIPrefix)
	if opt.Mode == "" {
		opt.Mode = ModeAll
	}
	if opt.DBDriver == "" {
		opt.DBDriver = DriverMySQL
	}
	if opt.DBPort == 0 {
		opt.DBPort = 3306
		if opt.DBDriver == DriverPostgres {
			opt.DBPort = 5432
		}
	}
	if opt.Listen == "" {
		opt.Listen = "0.0.0.0:8080"
	}
	if opt.DBHost == "" {
		opt.DBHost = "127.0.0.1"
	}
	if opt.RedisAddr == "" {
		opt.RedisAddr = "127.0.0.1:6379"
	}
	if opt.DBPort < 1 || opt.DBPort > 65535 || opt.RedisDB < 0 || opt.RedisDB > 255 {
		return nil, fmt.Errorf("数据库端口或 Redis 库号无效")
	}

	var buf bytes.Buffer
	err = configTemplate.Execute(&buf, opt)
	if err != nil {
		return nil, fmt.Errorf("生成配置失败: %w", err)
	}
	return buf.Bytes(), nil
}

// yamlQuote 输出 YAML 双引号字符串（JSON 字符串是合法的 YAML）。
func yamlQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

var configTemplate = template.Must(template.New("config").Funcs(template.FuncMap{"q": yamlQuote}).Parse(`# Gvboard 配置文件
# 所有配置项都可以用环境变量覆盖，规则：GVB_ + 大写路径（点换成下划线），例如 GVB_DATABASE_PASSWORD

app:
  # 签名密钥。接入原生产库时请改为原项目 .env 中的 APP_KEY（保留 "base64:" 前缀）
  key: {{q .AppKey}}
  debug: false
  timezone: "Asia/Shanghai"
  # 默认语言：zh-CN / en-US
  locale: "zh-CN"

server:
  listen: {{q .Listen}}
  # all：不分离部署（API + 前端）；api：分离部署（仅 API）
  mode: {{q .Mode}}
  # 反向代理地址，用于获取真实客户端 IP
  trusted_proxies: ["127.0.0.1", "::1"]
  read_timeout: 30s
  write_timeout: 60s
  idle_timeout: 120s
  max_body_bytes: 8388608
  # 自定义 404 页面文件，为空使用内置页面
  not_found_page: ""
  # 外部主题目录（不分离部署时使用），每个子目录是一个主题，与内置主题同名时外部优先
  theme_dir: "themes"

api:
  # 原生 API 前缀（已随机生成）。修改后前端需同步
  prefix: {{q .APIPrefix}}

database:
  # mysql 或 postgres
  driver: {{q .DBDriver}}
  host: {{q .DBHost}}
  port: {{.DBPort}}
  name: {{q .DBName}}
  user: {{q .DBUser}}
  password: {{q .DBPassword}}
  charset: "utf8mb4"
  sslmode: "disable"
  max_open_conns: 100
  max_idle_conns: 20
  conn_max_lifetime: 1h
  slow_threshold: 500ms

redis:
  addr: {{q .RedisAddr}}
  password: {{q .RedisPassword}}
  db: {{.RedisDB}}
  prefix: "gvb:"
  pool_size: 50

log:
  # debug / info / warn / error
  level: "info"
  # console / json
  format: "console"
  # 为空输出到标准输出
  file: ""

cors:
  # 分离部署时填写前端域名，例如 ["https://user.example.com", "https://admin.example.com"]
  allow_origins: []

compat:
  # 原 /api/v1 用户、访客、管理接口兼容层（开启会暴露原项目特征）
  v2board_api: false
  # 原订阅路径 /api/v1/client/subscribe（迁移站点建议开启，保证老订阅链接可用）
  legacy_subscribe: {{.Migrated}}
  # 原支付回调与 Telegram Webhook 路径（迁移过渡期使用）
  legacy_callback: false

node:
  # 节点通信接口来源 IP 白名单（支持 CIDR），为空不限制
  ip_whitelist: []

worker:
  # 处理队列任务（邮件、订单开通、流量统计、Telegram 通知等）。多实例部署时至少一个实例开启
  enabled: true
  concurrency: 10

scheduler:
  # 定时任务（流量入库、订单检查、流量重置、到期提醒等）。多实例同时开启也只会执行一次
  enabled: true

geoip:
  # ip2region 离线库（xdb 文件路径），用于 Telegram 工单提醒显示用户位置，不访问外部网站。
  # 为空时位置显示为"未知位置"
  ip2region_v4: ""
  ip2region_v6: ""
`))
