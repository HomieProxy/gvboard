package geoip

import (
	"testing"

	"gvboard/internal/config"
)

func TestFormat(t *testing.T) {
	for in, want := range map[string]string{
		"中国|广东省|深圳市|电信|CN":      "中国 广东省 深圳市 电信",
		"中国|0|上海|上海市|联通":        "中国 上海 上海市 联通",
		"中国|0|北京|北京|0":          "中国 北京",
		"美国|0|0|0|0":            "美国",
		"0|0|0|内网IP|内网IP":       "内网IP",
		"":                      "",
		"Japan|Tokyo|Tokyo||JP": "Japan Tokyo",
	} {
		if got := Format(in); got != want {
			t.Errorf("Format(%q) = %q，期望 %q", in, got, want)
		}
	}
}

func TestOpen(t *testing.T) {
	l, err := Open(config.GeoIPConfig{})
	if err != nil || l.Locate("1.1.1.1") != "" {
		t.Fatalf("未配置时应返回空查询：%v", err)
	}
	l.Close()
	if _, err := Open(config.GeoIPConfig{IPv4: "not-exist.xdb"}); err == nil {
		t.Fatal("文件不存在时应报错")
	}
}
