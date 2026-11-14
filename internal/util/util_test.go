package util

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestEmailValid(t *testing.T) {
	long := strings.Repeat("a", 320) + "@example.com"
	cases := []struct {
		email  string
		strict bool
		want   bool
	}{
		{"user@example.com", false, true},
		{"user+tag@mail.example.co", true, true},
		{"", false, false},
		{"user@example", false, false},
		{"user@.example.com", false, false},
		{"user@example..com", false, false},
		{"Name <user@example.com>", false, false},
		{`"a b"@example.com`, false, false},
		{"a@b.com\r\nRCPT TO:<x@y.com>", false, false},
		{"用户@example.com", false, true},
		{"用户@example.com", true, false},
		{long, false, false},
		{strings.Repeat("a", 60) + "@example.com", true, false},
	}
	for _, c := range cases {
		if got := EmailValid(c.email, c.strict); got != c.want {
			t.Errorf("EmailValid(%.40q, %v) = %v", c.email, c.strict, got)
		}
	}
}

func TestTrafficConvert(t *testing.T) {
	// 期望值由 PHP 8.2 运行原项目 Helper::trafficConvert 得到
	cases := map[int64]string{
		0:                       "0 B",
		512:                     "512 B",
		1024:                    "1024 B", // 原项目用 >，恰好 1024 仍显示 B
		1536:                    "1.5 KB",
		1048576 + 1:             "1 MB",
		1073741824:              "1024 MB",
		1073741824 * 3 / 2:      "1.5 GB",
		1073741824*100 + 123456: "100 GB",
		5368709120 + 53687091:   "5.05 GB",
		-1:                      "0",
	}
	for in, want := range cases {
		if got := TrafficConvert(in); got != want {
			t.Errorf("TrafficConvert(%d) = %q，期望 %q", in, got, want)
		}
	}
}

func TestURLEncoding(t *testing.T) {
	// 期望值由 PHP 8.2 的 rawurlencode 及原项目 Helper::encodeURIComponent 得到
	in := "香港 01|x+y=z&a~b!*'()"
	if got, want := RawURLEncode(in), "%E9%A6%99%E6%B8%AF%2001%7Cx%2By%3Dz%26a~b%21%2A%27%28%29"; got != want {
		t.Errorf("RawURLEncode = %s", got)
	}
	if got, want := EncodeURIComponent(in), "%E9%A6%99%E6%B8%AF%2001%7Cx%2By%3Dz%26a~b!*'()"; got != want {
		t.Errorf("EncodeURIComponent = %s", got)
	}
}

func TestBase64URLSafe(t *testing.T) {
	data := []byte{0xfb, 0xff, 0xfe, 0x01}
	enc := Base64EncodeURLSafe(data)
	if enc != "-__-AQ" {
		t.Errorf("编码结果 %s", enc)
	}
	for _, s := range []string{"-__-AQ", "-__-AQ=="} {
		dec, err := Base64DecodeURLSafe(s)
		if err != nil || string(dec) != string(data) {
			t.Errorf("解码 %s 失败: %v", s, err)
		}
	}
}

func TestRandomAndIDs(t *testing.T) {
	if s := RandomChar(32, false); len(s) != 32 || !regexp.MustCompile(`^[A-Za-z0-9]+$`).MatchString(s) {
		t.Errorf("RandomChar = %s", s)
	}
	if RandomChar(16, false) == RandomChar(16, false) {
		t.Error("随机串重复")
	}
	if u := UUIDv4(); !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(u) {
		t.Errorf("UUIDv4 = %s", u)
	}
	if g := GUID(); !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(g) {
		t.Errorf("GUID = %s", g)
	}
	for i := 0; i < 1000; i++ {
		if p := RandomInt(5, 7); p < 5 || p > 7 {
			t.Fatalf("RandomInt 越界: %d", p)
		}
	}
	if p := RandomPort("10000-10010"); p < 10000 || p > 10010 {
		t.Errorf("RandomPort = %d", p)
	}
	if RandomPort("443") != 443 {
		t.Error("单个端口应原样返回")
	}
}

func TestGenerateOrderNo(t *testing.T) {
	no := GenerateOrderNo()
	if !regexp.MustCompile(`^\d{25}$`).MatchString(no) {
		t.Fatalf("订单号格式错误: %s", no)
	}
	now := time.Now()
	if !strings.HasPrefix(no, now.Format("20060102")) {
		t.Errorf("订单号应以当天日期开头: %s", no)
	}
	// 与原项目 date('YmdHms') 相同：时之后是月份
	if no[10:12] != now.Format("01") {
		t.Errorf("订单号第 11-12 位应为月份: %s", no)
	}
}

func TestEmailSuffixVerify(t *testing.T) {
	list := []string{"gmail.com", "qq.com"}
	for email, want := range map[string]bool{
		"a@gmail.com": true, "a@b@qq.com": true, "a@GMAIL.com": false,
		"a@163.com": false, "a@": false, "abc": false,
	} {
		if EmailSuffixVerify(email, list) != want {
			t.Errorf("EmailSuffixVerify(%s) 期望 %v", email, want)
		}
	}
}

func TestSS2022Keys(t *testing.T) {
	// 期望值由 PHP 8.2 运行原项目 Helper::getServerKey / uuidToBase64 得到
	if got := GetServerKey(1700000000, 16); got != "MjQ5MjBkZWNmODNmNjk2MA==" {
		t.Errorf("GetServerKey = %s", got)
	}
	if got := UUIDToBase64("12345678-90ab-cdef", 16); got != "MTIzNDU2NzgtOTBhYi1jZA==" {
		t.Errorf("UUIDToBase64 = %s", got)
	}
}

// 期望值均由 PHP 8.2 计算得到。
func TestVerifyPassword(t *testing.T) {
	cases := []struct{ algo, salt, pwd, hash string }{
		{"md5", "", "password", "5f4dcc3b5aa765d61d8327deb882cf99"},
		{"sha256", "", "password", "5e884898da28047151d0e56f8dc6292773603d0d6aabbdd62a11ef721d1542d8"},
		{"md5salt", "abc", "password", "8223fe8dc0533c6ebbb717e7fda2833c"},
		{"", "", "p@ss", "$2y$10$pAtFuTaE27j1aeQUsZZ4L.5BPqdZJJKbt0YH41abgbYCdwCQmLP7K"}, // PHP password_hash
	}
	for _, c := range cases {
		if !VerifyPassword(c.algo, c.salt, c.pwd, c.hash) {
			t.Errorf("%s 校验失败", c.algo)
		}
		if VerifyPassword(c.algo, c.salt, "wrong", c.hash) {
			t.Errorf("%s 错误密码通过", c.algo)
		}
	}

	h, err := HashPassword("p@ss")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword("", "", "p@ss", h) || VerifyPassword("", "", "x", h) {
		t.Error("bcrypt 校验错误")
	}

	// 超过 72 字节：PHP 只使用前 72 字节（哈希由 PHP 8.2 password_hash 生成）
	x72 := strings.Repeat("x", 72)
	phpLong := "$2y$10$9utE0zEF9HOpAAxys/0i0u5UqIS3uEhAcyIS2gbFPiNmYd9hWOgqi" // x*72 + "tail"
	if !VerifyPassword("", "", x72+"tail", phpLong) || !VerifyPassword("", "", x72, phpLong) || VerifyPassword("", "", x72[:71], phpLong) {
		t.Error("超长密码校验结果应与 PHP 一致")
	}
	h, err = HashPassword(x72 + "other")
	if err != nil {
		t.Fatalf("超长密码应能生成哈希: %v", err)
	}
	if !VerifyPassword("", "", x72, h) {
		t.Error("超长密码生成的哈希应与 PHP 一样只使用前 72 字节")
	}
}
