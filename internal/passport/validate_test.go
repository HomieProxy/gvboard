package passport

import (
	"strings"
	"testing"
)

func TestEmailFormat(t *testing.T) {
	good := []string{"a@example.com", "a.b+c@sub.example.co", "A_B-1@x.io"}
	for _, e := range good {
		if err := emailFormat(e, true); err != nil {
			t.Errorf("%q 应合法: %v", e, err)
		}
	}
	bad := []string{
		"plain", "@example.com", "a@", "a@localhost", "a@.com", "a@x..com", "a@x.com.",
		"名字 <a@x.com>", " a@x.com", "a b@x.com", "a@x.com\n",
	}
	for _, e := range bad {
		if err := emailFormat(e, true); err != errEmailFormat {
			t.Errorf("%q 应判定为格式错误，实际 %v", e, err)
		}
	}
	if err := emailFormat("", true); err != errEmailRequired {
		t.Errorf("空邮箱应提示不能为空，实际 %v", err)
	}
	long := strings.Repeat("a", 60) + "@x.com"
	if emailFormat(long, true) == nil {
		t.Error("注册时超过 64 个字符的邮箱应被拒绝")
	}
	if err := emailFormat(long, false); err != nil {
		t.Errorf("登录时不限制邮箱长度（与原项目一致）: %v", err)
	}
	if emailFormat("ü@x.com", true) == nil {
		t.Error("注册时非 ASCII 邮箱应被拒绝")
	}
}

func TestPasswordFormat(t *testing.T) {
	if passwordFormat("", 0) != errPasswordRequired {
		t.Error("空密码应提示不能为空")
	}
	if passwordFormat("1234567", 0) != errPasswordMin {
		t.Error("7 位密码应被拒绝")
	}
	if passwordFormat("密码密码密码密码", 0) != nil {
		t.Error("8 个中文字符应视为 8 位（与 Laravel 按字符计数一致）")
	}
	if passwordFormat(strings.Repeat("a", 65), 64) != errPasswordMin {
		t.Error("找回密码时超过 64 位应被拒绝")
	}
	if passwordFormat(strings.Repeat("a", 200), 0) != nil {
		t.Error("注册时不限制最大长度（与原项目一致）")
	}
}

func TestGmailAlias(t *testing.T) {
	cases := map[string]bool{"a.b@gmail.com": true, "a+b@x.com": true, "ab@x.com": false, "ab@x.y.com": false}
	for e, want := range cases {
		if gmailAlias(e) != want {
			t.Errorf("gmailAlias(%q) 应为 %v", e, want)
		}
	}
}
