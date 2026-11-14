package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, content []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func generated(t *testing.T, opt GenerateOptions) []byte {
	t.Helper()
	b, err := Generate(opt)
	if err != nil {
		t.Fatal(err)
	}
	// 生成的模板中数据库名与用户为空，测试时补全。
	s := strings.Replace(string(b), `name: ""`, `name: "v2board"`, 1)
	s = strings.Replace(s, `user: ""`, `user: "root"`, 1)
	return []byte(s)
}

func TestGenerateAndLoad(t *testing.T) {
	cfg, err := Load(writeConfig(t, generated(t, GenerateOptions{Migrated: true})))
	if err != nil {
		t.Fatalf("加载生成的配置失败: %v", err)
	}
	if !strings.HasPrefix(cfg.API.Prefix, "/api/") || len(cfg.API.Prefix) != len("/api/")+10 {
		t.Errorf("随机 API 前缀格式不正确: %s", cfg.API.Prefix)
	}
	if !strings.HasPrefix(cfg.App.Key, "base64:") {
		t.Errorf("随机密钥格式不正确: %s", cfg.App.Key)
	}
	if cfg.Server.Mode != ModeAll {
		t.Errorf("默认部署模式应为 all，实际 %s", cfg.Server.Mode)
	}
	if !cfg.Compat.LegacySubscribe || cfg.Compat.V2boardAPI || cfg.Compat.LegacyCallback {
		t.Errorf("迁移站点兼容开关默认值不正确: %+v", cfg.Compat)
	}
	if cfg.App.Locale != "zh-CN" || !cfg.Worker.Enabled || cfg.Worker.Concurrency != 10 || !cfg.Scheduler.Enabled {
		t.Errorf("语言、队列、定时任务默认值不正确: locale=%s worker=%+v scheduler=%+v", cfg.App.Locale, cfg.Worker, cfg.Scheduler)
	}
}

// 安装脚本通过 init 参数写入连接信息，特殊字符（引号、反斜杠、#、换行）必须原样保留。
func TestGenerateConnection(t *testing.T) {
	pass := "p\"a#s\\s: x\n'y"
	cfg, err := Load(writeConfig(t, generated(t, GenerateOptions{
		Listen: "127.0.0.1:9000", DBHost: "db.local", DBPort: 3307, DBName: "gv", DBUser: "u#1",
		DBPassword: pass, RedisAddr: "127.0.0.1:6380", RedisPassword: "r'p", RedisDB: 5,
	})))
	if err != nil {
		t.Fatalf("加载生成的配置失败: %v", err)
	}
	d, r := cfg.Database, cfg.Redis
	if cfg.Server.Listen != "127.0.0.1:9000" || d.Host != "db.local" || d.Port != 3307 || d.Name != "gv" || d.User != "u#1" ||
		d.Password != pass || r.Addr != "127.0.0.1:6380" || r.Password != "r'p" || r.DB != 5 {
		t.Fatalf("连接信息不正确: listen=%s db=%+v redis=%+v", cfg.Server.Listen, d, r)
	}
	if _, err := Generate(GenerateOptions{DBPort: 70000}); err == nil {
		t.Fatal("端口超出范围应报错")
	}
}

func TestGenerateRandomness(t *testing.T) {
	a, _ := Generate(GenerateOptions{})
	b, _ := Generate(GenerateOptions{})
	if string(a) == string(b) {
		t.Error("两次生成的配置完全相同，随机值未生效")
	}
}

func TestEnvOverride(t *testing.T) {
	p := writeConfig(t, generated(t, GenerateOptions{}))
	t.Setenv("GVB_DATABASE_PASSWORD", "from-env")
	t.Setenv("GVB_SERVER_MODE", "api")
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Database.Password != "from-env" || cfg.Server.Mode != ModeAPI {
		t.Errorf("环境变量未覆盖配置: password=%s mode=%s", cfg.Database.Password, cfg.Server.Mode)
	}
}

func TestNormalizePrefix(t *testing.T) {
	cases := map[string]string{
		"api/abc":   "/api/abc",
		"/api/abc/": "/api/abc",
		"  /x  ":    "/x",
		"":          "",
		"///":       "",
	}
	for in, want := range cases {
		if got := NormalizePrefix(in); got != want {
			t.Errorf("NormalizePrefix(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

func TestValidate(t *testing.T) {
	base := func() *Config {
		return &Config{
			App:      AppConfig{Key: "base64:0123456789abcdef", Timezone: "Asia/Shanghai"},
			Server:   ServerConfig{Mode: ModeAll},
			API:      APIConfig{Prefix: "/api/abc"},
			Database: DatabaseConfig{Driver: DriverMySQL, Name: "db", User: "u"},
		}
	}
	if err := base().Validate(); err != nil {
		t.Fatalf("合法配置校验失败: %v", err)
	}

	bad := []struct {
		name string
		mut  func(c *Config)
	}{
		{"密钥过短", func(c *Config) { c.App.Key = "short" }},
		{"时区无效", func(c *Config) { c.App.Timezone = "Mars/Base" }},
		{"模式无效", func(c *Config) { c.Server.Mode = "both" }},
		{"前缀为空", func(c *Config) { c.API.Prefix = "" }},
		{"前缀非法字符", func(c *Config) { c.API.Prefix = "/api/a b" }},
		{"前缀使用保留 /api/v1", func(c *Config) { c.API.Prefix = "/api/v1" }},
		{"前缀落在 /api/v1 下", func(c *Config) { c.API.Prefix = "/api/v1/x" }},
		{"前缀使用保留 /api/v2", func(c *Config) { c.API.Prefix = "/api/v2" }},
		{"数据库驱动无效", func(c *Config) { c.Database.Driver = "sqlite" }},
		{"数据库名为空", func(c *Config) { c.Database.Name = "" }},
		{"CORS 使用通配符", func(c *Config) { c.CORS.AllowOrigins = []string{"*"} }},
		{"CORS 地址无效", func(c *Config) { c.CORS.AllowOrigins = []string{"example.com"} }},
		{"队列并发为 0", func(c *Config) { c.Worker = WorkerConfig{Enabled: true} }},
		{"队列并发过大", func(c *Config) { c.Worker = WorkerConfig{Enabled: true, Concurrency: 1001} }},
	}
	for _, tc := range bad {
		c := base()
		tc.mut(c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s：应校验失败但通过了", tc.name)
		}
	}

	ok := base()
	ok.API.Prefix = "/api/v1x"
	if err := ok.Validate(); err != nil {
		t.Errorf("/api/v1x 不应视为保留前缀: %v", err)
	}

	ok = base()
	ok.Worker = WorkerConfig{Enabled: false, Concurrency: 0}
	if err := ok.Validate(); err != nil {
		t.Errorf("关闭队列时不应校验并发数: %v", err)
	}
}
