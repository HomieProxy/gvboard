package setting

import (
	"reflect"
	"strings"
	"testing"
)

// 原项目 ConfigController::save 使用 var_export 生成的真实格式样本
const samplePHP = `<?php
 return array (
  'invite_force' => '0',
  'invite_commission' => '10',
  'invite_gen_limit' => '5',
  'commission_withdraw_method' => 
  array (
    0 => '支付宝',
    1 => 'USDT',
  ),
  'deposit_bounus' => 
  array (
    0 => NULL,
  ),
  'app_name' => 'My \'Site\' \\ 名称',
  'app_url' => 'https://panel.example.com',
  'logo' => NULL,
  'try_out_hour' => '1.5',
  'commission_distribution_l1' => NULL,
  'commission_distribution_l2' => '20',
  'stop_register' => 0,
  'email_whitelist_suffix' => 'gmail.com, qq.com',
  'email_port' => '465',
  'secure_path' => 'abcd1234',
  'server_token' => 'abcdefghijklmnopqrstuvwxyz',
  'telegram_discuss_id' => NULL,
  'custom_from_fork' => 
  array (
    'k' => 'v',
  ),
  'force_https' => true,
  'reset_traffic_method' => -0,
) ;`

func TestParsePHPConfig(t *testing.T) {
	m, err := ParsePHPConfig([]byte(samplePHP))
	if err != nil {
		t.Fatal(err)
	}
	if m["app_name"] != `My 'Site' \ 名称` {
		t.Errorf("单引号转义解析错误: %q", m["app_name"])
	}
	if !reflect.DeepEqual(m["commission_withdraw_method"], []any{"支付宝", "USDT"}) {
		t.Errorf("顺序数组解析错误: %#v", m["commission_withdraw_method"])
	}
	if !reflect.DeepEqual(m["deposit_bounus"], []any{nil}) {
		t.Errorf("NULL 元素解析错误: %#v", m["deposit_bounus"])
	}
	if !reflect.DeepEqual(m["custom_from_fork"], map[string]any{"k": "v"}) {
		t.Errorf("关联数组解析错误: %#v", m["custom_from_fork"])
	}
	if m["stop_register"] != int64(0) || m["force_https"] != true || m["logo"] != nil {
		t.Errorf("字面量解析错误: %#v %#v %#v", m["stop_register"], m["force_https"], m["logo"])
	}
}

func TestParsePHPConfigVariants(t *testing.T) {
	ok := []string{
		`<?php return ['a' => 1, 'b' => [1, 2,], "c" => "x\n\"y\""];`,
		"<?php\n// 注释\n# 注释\n/* 块注释 */ return array('a' => 1.5e2);",
		`<?php return array();`,
		"\uFEFF<?php\r\nreturn array (\r\n  'a' => 1,\r\n);", // Windows 编辑过的文件：BOM + CRLF
	}
	for _, s := range ok {
		if _, err := ParsePHPConfig([]byte(s)); err != nil {
			t.Errorf("应解析成功: %s\n%v", s, err)
		}
	}
	bad := []string{
		`<?php return array('a' => env('X'));`, // 不执行函数调用
		`<?php return array('a' => "$x");`,     // 不支持变量插值
		`<?php return array('a' => 'unclosed);`,
		`<?php $a = 1;`,
		`<?php return 'string';`,
		`<?php return array('a' => 1 'b' => 2);`,
	}
	for _, s := range bad {
		if _, err := ParsePHPConfig([]byte(s)); err == nil {
			t.Errorf("应解析失败: %s", s)
		}
	}
}

func TestPrepareImport(t *testing.T) {
	m, err := ParsePHPConfig([]byte(samplePHP))
	if err != nil {
		t.Fatal(err)
	}
	values, rep := PrepareImport(m)

	want := map[string]any{
		"invite_force":               int64(0),
		"invite_commission":          int64(10),
		"commission_withdraw_method": []string{"支付宝", "USDT"},
		"deposit_bounus":             []string{},
		"try_out_hour":               1.5,
		"commission_distribution_l2": float64(20),
		"email_whitelist_suffix":     []string{"gmail.com", "qq.com"},
		"email_port":                 int64(465),
		"force_https":                int64(1),
		"reset_traffic_method":       int64(0),
		"server_token":               "abcdefghijklmnopqrstuvwxyz",
	}
	for k, v := range want {
		if !reflect.DeepEqual(values[k], v) {
			t.Errorf("%s = %#v，期望 %#v", k, values[k], v)
		}
	}
	if _, ok := values["logo"]; ok {
		t.Error("NULL 值不应导入（读取时使用默认值）")
	}
	if _, ok := values["secure_path"]; ok {
		t.Error("不足 16 位的管理端路径不应导入")
	}
	if !containsSubstr(rep.Notices, "管理端路径") {
		t.Errorf("应提示管理端路径将重新生成: %v", rep.Notices)
	}
	if !reflect.DeepEqual(rep.Unknown, []string{"custom_from_fork"}) {
		t.Errorf("未知项: %v", rep.Unknown)
	}
	if _, ok := values["custom_from_fork"]; !ok {
		t.Error("未知项应原样保存")
	}
	if len(rep.Skipped) != 0 {
		t.Errorf("不应有跳过项: %v", rep.Skipped)
	}
}

func TestPrepareImportAliasAndToken(t *testing.T) {
	values, rep := PrepareImport(map[string]any{"frontend_admin_path": "abcdefghijklmnop1234"})
	if values["secure_path"] != "abcdefghijklmnop1234" {
		t.Errorf("旧键名 frontend_admin_path 应映射为 secure_path: %v", values)
	}
	if !containsSubstr(rep.Notices, "server_token") {
		t.Error("缺少节点通信密钥时应提示")
	}
	_, rep = PrepareImport(map[string]any{"invite_commission": "abc"})
	if _, ok := rep.Skipped["invite_commission"]; !ok {
		t.Error("格式错误的值应跳过并说明原因")
	}
}

func TestCoerce(t *testing.T) {
	d := func(k string) Def { x, _ := Lookup(k); return x }
	cases := []struct {
		key  string
		in   any
		want any
		err  bool
	}{
		{"invite_commission", "15", int64(15), false},
		{"invite_commission", 15.0, int64(15), false},
		{"invite_commission", 1.5, nil, true},
		{"invite_commission", "", nil, false},
		{"stop_register", "true", int64(1), false},
		{"stop_register", int64(5), int64(1), false},
		{"stop_register", "maybe", nil, true},
		{"app_name", int64(5), "5", false},
		{"commission_withdraw_method", []any{"a", int64(1)}, []string{"a", "1"}, false},
		{"secure_path", "short", nil, true},
		{"secure_path", "abcdefghijklmnop/x", nil, true},
		{"secure_path", "abcdefghijklmnop_-1", "abcdefghijklmnop_-1", false},
		{"subscribe_path", "no-slash", nil, true},
		{"subscribe_path", "/sub/abc", "/sub/abc", false},
	}
	for _, c := range cases {
		got, err := Coerce(d(c.key), c.in)
		if (err != nil) != c.err {
			t.Errorf("Coerce(%s, %#v) 错误情况不符: %v", c.key, c.in, err)
			continue
		}
		if !c.err && !reflect.DeepEqual(got, c.want) {
			t.Errorf("Coerce(%s, %#v) = %#v，期望 %#v", c.key, c.in, got, c.want)
		}
	}
}

func TestDefaultsHaveNoFingerprint(t *testing.T) {
	for _, d := range Defs() {
		if s, ok := d.Default.(string); ok && strings.Contains(strings.ToLower(s), "v2board") {
			t.Errorf("设置项 %s 的默认值包含 v2board 字样: %s", d.Key, s)
		}
	}
}

func TestDefaultGetters(t *testing.T) {
	s := &Service{vals: map[string]any{"invite_commission": int64(20)}}
	if s.Int("invite_commission") != 20 {
		t.Error("应读取已设置的值")
	}
	if s.Int("invite_gen_limit") != 5 || !s.Bool("password_limit_enable") || s.String("currency") != "CNY" {
		t.Error("未设置时应返回默认值")
	}
	if s.FloatPtr("commission_distribution_l1") != nil {
		t.Error("可空设置未设置时应返回 nil")
	}
	if len(s.Strings("email_whitelist_suffix")) != 9 {
		t.Error("邮箱后缀白名单默认值不正确")
	}
}

func TestTemplateValidation(t *testing.T) {
	cases := []struct {
		fn   func(any) error
		in   string
		fail bool
	}{
		{validateYAMLTemplate, "", false},
		{validateYAMLTemplate, "proxies: []\nproxy-groups:\n  - name: a\n    proxies: [b]\n", false},
		{validateYAMLTemplate, "just text", true},
		{validateYAMLTemplate, "a: [1, 2", true},
		{validateJSONTemplate, "", false},
		{validateJSONTemplate, `{"outbounds":[]}`, false},
		{validateJSONTemplate, `"text"`, true},
		{validateJSONTemplate, `{"outbounds":`, true},
	}
	for _, c := range cases {
		if err := c.fn(c.in); (err != nil) != c.fail {
			t.Errorf("模板 %q 校验结果 %v，期望失败=%v", c.in, err, c.fail)
		}
	}
}

func TestClientsValidation(t *testing.T) {
	ok := `[{"platform":"windows","name":"Clash Verge","url":"https://a.example.com/c.exe","icon":""},` +
		`{"platform":"harmony","name":"X","url":"http://b.example.com","icon":"https://b.example.com/i.png"}]`
	cases := []struct {
		in   string
		fail bool
	}{
		{"", false},
		{"[]", false},
		{ok, false},
		{`{"platform":"windows"}`, true},
		{`[{"platform":"symbian","name":"A","url":"https://a.example.com"}]`, true},
		{`[{"platform":"ios","name":" ","url":"https://a.example.com"}]`, true},
		{`[{"platform":"ios","name":"A","url":"javascript:alert(1)"}]`, true},
		{`[{"platform":"ios","name":"A","url":"https://a.example.com","icon":"data:image/png;base64,AAAA"}]`, true},
	}
	for _, c := range cases {
		if err := validateClients(c.in); (err != nil) != c.fail {
			t.Errorf("客户端列表 %q 校验结果 %v，期望失败=%v", c.in, err, c.fail)
		}
	}
	if list, _ := ParseClients(ok); len(list) != 2 || list[1].Platform != "harmony" {
		t.Errorf("解析结果不正确: %+v", list)
	}
}

func containsSubstr(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
