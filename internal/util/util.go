// Package util 通用工具函数，对应原项目 App\Utils\Helper 中与具体业务无关的部分。
package util

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math"
	"math/big"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	alnum   = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	special = "!@#$?|{/:%^&*()-_[]}<>=+,."
)

// RandomInt 返回 [min, max] 区间的安全随机整数。
func RandomInt(min, max int64) int64 {
	if max <= min {
		return min
	}
	n, err := rand.Int(rand.Reader, big.NewInt(max-min+1))
	if err != nil {
		panic(err) // crypto/rand 不可用属于系统级故障
	}
	return min + n.Int64()
}

// RandomChar 随机字符串，special 为 true 时包含特殊字符。
func RandomChar(n int, withSpecial bool) string {
	chars := alnum
	if withSpecial {
		chars += special
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = chars[RandomInt(0, int64(len(chars)-1))]
	}
	return string(b)
}

// UUIDv4 生成标准格式 UUID（对应原项目 Helper::guid(true)，用于用户 uuid）。
func UUIDv4() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// GUID 32 位十六进制随机串（对应原项目 Helper::guid()，用于订阅 token 等）。
func GUID() string {
	sum := md5.Sum([]byte(UUIDv4() + "-" + strconv.FormatInt(time.Now().Unix(), 10)))
	return hex.EncodeToString(sum[:])
}

// GenerateOrderNo 订单号，格式与原项目一致：date('YmdHms') + 6 位微秒 + 5 位随机数。
// 原项目格式串中第二个 m 是月份而不是分钟，这里保持相同输出。
func GenerateOrderNo() string {
	now := time.Now()
	return fmt.Sprintf("%s%02d%02d%02d%06d%d",
		now.Format("20060102"), now.Hour(), int(now.Month()), now.Second(), now.Nanosecond()/1000, RandomInt(10000, 99999))
}

var printableASCII = regexp.MustCompile(`^[\x21-\x7E]+$`)

// EmailValid 邮箱格式（原项目 email:strict）。strict 为 true 时另外限制长度不超过 64 且只含可见 ASCII 字符。
func EmailValid(email string, strict bool) bool {
	if email == "" || len(email) > 320 || (strict && (utf8.RuneCountInString(email) > 64 || !printableASCII.MatchString(email))) {
		return false
	}
	// 长度在解析前检查：mail.ParseAddress 对超长输入耗时过高（GO-2025-4006）
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Name != "" || addr.Address != email {
		return false
	}
	at := strings.LastIndex(email, "@")
	domain := email[at+1:]
	return at > 0 && strings.Contains(domain, ".") && !strings.HasPrefix(domain, ".") && !strings.HasSuffix(domain, ".") && !strings.Contains(domain, "..")
}

// EmailSuffixVerify 判断邮箱后缀是否在白名单内。
func EmailSuffixVerify(email string, suffixes []string) bool {
	i := strings.LastIndex(email, "@")
	if i < 0 || i == len(email)-1 {
		return false
	}
	suffix := email[i+1:]
	for _, s := range suffixes {
		if s == suffix {
			return true
		}
	}
	return false
}

// TrafficConvert 字节数转可读格式，与原项目输出一致（如 "1.5 GB"）。
func TrafficConvert(b int64) string {
	const (
		kb = 1024
		mb = 1048576
		gb = 1073741824
	)
	f := func(v float64, unit string) string {
		return strconv.FormatFloat(math.Round(v*100)/100, 'f', -1, 64) + " " + unit
	}
	switch {
	case b > gb:
		return f(float64(b)/gb, "GB")
	case b > mb:
		return f(float64(b)/mb, "MB")
	case b > kb:
		return f(float64(b)/kb, "KB")
	case b < 0:
		return "0"
	default:
		return f(float64(b), "B")
	}
}

// Base64EncodeURLSafe URL 安全的 base64，去掉填充。
func Base64EncodeURLSafe(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

// Base64DecodeURLSafe 解码 URL 安全的 base64，兼容带或不带填充。
func Base64DecodeURLSafe(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}

// RawURLEncode 与 PHP rawurlencode 一致（RFC 3986）。
func RawURLEncode(s string) string {
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == '~' {
			sb.WriteByte(c)
		} else {
			fmt.Fprintf(&sb, "%%%02X", c)
		}
	}
	return sb.String()
}

// EncodeURIComponent 与 JavaScript encodeURIComponent 一致（原项目 Helper::encodeURIComponent）。
func EncodeURIComponent(s string) string {
	return strings.NewReplacer("%21", "!", "%2A", "*", "%27", "'", "%28", "(", "%29", ")").Replace(RawURLEncode(s))
}

// RandomPort 从 "起始-结束" 端口范围中随机取一个，格式错误时原样返回解析出的第一个数。
func RandomPort(r string) int64 {
	parts := strings.SplitN(r, "-", 2)
	lo, _ := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	if len(parts) < 2 {
		return lo
	}
	hi, _ := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	return RandomInt(lo, hi)
}

// GetServerKey Shadowsocks 2022 节点密钥：base64(md5(创建时间) 的前 length 位)。
func GetServerKey(createdAt int64, length int) string {
	sum := md5.Sum([]byte(strconv.FormatInt(createdAt, 10)))
	h := hex.EncodeToString(sum[:])
	if length < len(h) {
		h = h[:length]
	}
	return base64.StdEncoding.EncodeToString([]byte(h))
}

// UUIDToBase64 Shadowsocks 2022 用户密钥：base64(uuid 的前 length 位)。
func UUIDToBase64(uuid string, length int) string {
	if length < len(uuid) {
		uuid = uuid[:length]
	}
	return base64.StdEncoding.EncodeToString([]byte(uuid))
}
