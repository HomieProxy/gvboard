package phpx

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"math"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

const upperHex = "0123456789ABCDEF"

func urlEscape(s string, rfc3986 bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
			b.WriteByte(c)
		case c == '~' && rfc3986:
			b.WriteByte(c)
		case c == ' ' && !rfc3986:
			b.WriteByte('+')
		default:
			b.WriteByte('%')
			b.WriteByte(upperHex[c>>4])
			b.WriteByte(upperHex[c&15])
		}
	}
	return b.String()
}

// RawURLEncode rawurlencode（RFC 3986）。
func RawURLEncode(s string) string { return urlEscape(s, true) }

// URLEncode urlencode（空格为 +，~ 也编码）。
func URLEncode(s string) string { return urlEscape(s, false) }

// EncodeURIComponent 原项目 Helper::encodeURIComponent：rawurlencode 后还原 ! * ' ( )。
func EncodeURIComponent(s string) string {
	r := strings.NewReplacer("%21", "!", "%2A", "*", "%27", "'", "%28", "(", "%29", ")")
	return r.Replace(RawURLEncode(s))
}

// HTTPBuildQuery http_build_query：跳过 null，true/false 为 1/0，嵌套数组展开为 k[i]=v。
// rfc3986 为 true 时对应 PHP_QUERY_RFC3986。
func HTTPBuildQuery(a *Arr, rfc3986 bool) string {
	var parts []string
	buildQuery(&parts, a, "", rfc3986)
	return strings.Join(parts, "&")
}

func buildQuery(parts *[]string, a *Arr, prefix string, rfc3986 bool) {
	a.Each(func(k, v any) {
		if v == nil {
			return
		}
		key := urlEscape(Str(k), rfc3986)
		if prefix != "" {
			key = prefix + "%5B" + key + "%5D"
		}
		switch x := v.(type) {
		case *Arr:
			buildQuery(parts, x, key, rfc3986)
			return
		case bool:
			if x {
				*parts = append(*parts, key+"=1")
			} else {
				*parts = append(*parts, key+"=0")
			}
			return
		case float64:
			*parts = append(*parts, key+"="+urlEscape(jsonFloat(x), rfc3986))
			return
		}
		*parts = append(*parts, key+"="+urlEscape(Str(v), rfc3986))
	})
}

// Base64 base64_encode。
func Base64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// Round PHP 8.2 round()（含预舍入，如 round(1.005, 2) 为 1.01）。
func Round(value float64, places int) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value == 0 {
		return value
	}
	const maxPrecision = 4 * 15
	precisionPlaces := 14 - int(math.Floor(math.Log10(math.Abs(value))))
	f1 := math.Pow10(absInt(places))
	var tmp float64
	if precisionPlaces > places && precisionPlaces-15 < places {
		use := precisionPlaces
		if use < -maxPrecision {
			use = -maxPrecision
		}
		tmp = roundHalfUp(roundBasic(value, use))
		use = places - use
		if use < -maxPrecision {
			use = -maxPrecision
		}
		tmp = tmp / math.Pow10(absInt(use))
	} else {
		if places >= 0 {
			tmp = value * f1
		} else {
			tmp = value / f1
		}
		if math.Abs(tmp) >= 1e15 {
			return value
		}
	}
	tmp = roundHalfUp(tmp)
	if places > 0 {
		return tmp / f1
	}
	return tmp * f1
}

func roundBasic(value float64, places int) float64 {
	f := math.Pow10(absInt(places))
	if places >= 0 {
		return value * f
	}
	return value / f
}

func roundHalfUp(v float64) float64 {
	if v >= 0 {
		return math.Floor(v + 0.5)
	}
	return math.Ceil(v - 0.5)
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// TrafficConvert 原项目 Helper::trafficConvert（如 "1.5 GB"），负数返回 "0"。
func TrafficConvert(b int64) string {
	const kb, mb, gb = 1024, 1048576, 1073741824
	f := float64(b)
	switch {
	case b > gb:
		return FloatStr(Round(f/gb, 2)) + " GB"
	case b > mb:
		return FloatStr(Round(f/mb, 2)) + " MB"
	case b > kb:
		return FloatStr(Round(f/kb, 2)) + " KB"
	case b < 0:
		return "0"
	}
	return FloatStr(Round(f, 2)) + " B"
}

// GB 原项目 round($bytes / (1024*1024*1024), 2)。
func GB(b int64) float64 { return Round(float64(b)/(1024*1024*1024), 2) }

// IsIPv6 filter_var($host, FILTER_VALIDATE_IP, FILTER_FLAG_IPV6)。
func IsIPv6(host string) bool {
	if !strings.Contains(host, ":") || strings.ContainsAny(host, "%[] ") {
		return false
	}
	a, err := netip.ParseAddr(host)
	return err == nil && a.Is6()
}

// FormatHost IPv6 地址加方括号（原项目 Helper::formatHost）。
func FormatHost(host string) string {
	if IsIPv6(host) {
		return "[" + host + "]"
	}
	return host
}

// ServerKey 原项目 Helper::getServerKey：base64(md5(时间戳) 前 n 位)。
func ServerKey(createdAt any, n int) string {
	h := md5.Sum([]byte(Str(createdAt)))
	return Base64(hex.EncodeToString(h[:])[:n])
}

// UUIDToBase64 原项目 Helper::uuidToBase64：base64(uuid 前 n 位)。
func UUIDToBase64(uuid string, n int) string {
	if n > len(uuid) {
		n = len(uuid)
	}
	return Base64(uuid[:n])
}

// Date PHP date()，只支持 Y m d H i s，使用本地时区。
func Date(format string, ts int64) string {
	t := time.Unix(ts, 0)
	var b strings.Builder
	for _, c := range format {
		switch c {
		case 'Y':
			b.WriteString(strconv.Itoa(t.Year()))
		case 'm':
			b.WriteString(pad2(int(t.Month())))
		case 'd':
			b.WriteString(pad2(t.Day()))
		case 'H':
			b.WriteString(pad2(t.Hour()))
		case 'i':
			b.WriteString(pad2(t.Minute()))
		case 's':
			b.WriteString(pad2(t.Second()))
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// Strpos strpos：返回位置，未找到为 -1。
func Strpos(haystack, needle string) int { return strings.Index(haystack, needle) }

// FirstPort 原项目取端口列表首个端口：explode(',') 的第一段，若为端口段取起始端口。
func FirstPort(port any) string {
	first := strings.SplitN(Str(port), ",", 2)[0]
	if i := strings.IndexByte(first, '-'); i >= 0 {
		return first[:i]
	}
	return first
}

// MultiPort 端口是否为多端口或端口段：count(explode(',')) !== 1 || 第一段含 '-'。
func MultiPort(port any) bool {
	s := Str(port)
	return strings.Contains(s, ",") || strings.Contains(strings.SplitN(s, ",", 2)[0], "-")
}
