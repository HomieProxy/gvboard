package passport

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"gvboard/internal/util"
)

var sixDigits = regexp.MustCompile(`^\d{6}$`)

// emailFormat 校验邮箱格式（原项目 email:strict、max:64、regex:/^[\x21-\x7E]+$/）。
// strict 为 true 时额外限制长度不超过 64 且只含可见 ASCII 字符（注册、找回密码、发送验证码）。
func emailFormat(email string, strict bool) error {
	if email == "" {
		return errEmailRequired
	}
	if !util.EmailValid(email, strict) {
		return errEmailFormat
	}
	return nil
}

// passwordFormat 密码至少 8 个字符；max 大于 0 时限制最大长度（找回密码为 64）。
func passwordFormat(password string, max int) error {
	if password == "" {
		return errPasswordRequired
	}
	n := utf8.RuneCountInString(password)
	if n < 8 || (max > 0 && n > max) {
		return errPasswordMin
	}
	return nil
}

// cacheEmail 验证码等缓存键使用的邮箱形式（与原项目一致：去空格并转小写）。
func cacheEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// gmailAlias 原项目对任意邮箱都检查前缀是否含 "." 或 "+"。
func gmailAlias(email string) bool {
	prefix, _, _ := strings.Cut(email, "@")
	return strings.ContainsAny(prefix, ".+")
}
