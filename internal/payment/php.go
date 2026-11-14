package payment

import (
	"bytes"
	"encoding/json"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Params 回调参数，与原项目 $request->input() 一致：查询参数与请求体（JSON 或表单）合并，请求体优先；
// 字符串去除首尾空白，空字符串转为 nil（Laravel TrimStrings、ConvertEmptyStringsToNull 中间件）。
// JSON 数字保留为 json.Number，以便按 PHP 的整数 / 浮点规则转换为字符串参与签名。
type Params map[string]any

// Str 取参数的字符串形式（PHP 字符串转换规则），不存在或为 nil 时返回空字符串。
func (p Params) Str(key string) string { return phpStr(p[key]) }

// Has 参数存在且不为 nil（PHP isset）。
func (p Params) Has(key string) bool { return p[key] != nil }

// without 返回去掉指定键后的副本。
func (p Params) without(keys ...string) Params {
	out := make(Params, len(p))
	for k, v := range p {
		out[k] = v
	}
	for _, k := range keys {
		delete(out, k)
	}
	return out
}

func parseParams(r *http.Request, body []byte) Params {
	p := Params{}
	for k, vs := range r.URL.Query() {
		p[k] = vs[len(vs)-1]
	}
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch {
	case strings.HasSuffix(ct, "/json") || strings.HasSuffix(ct, "+json"):
		var m map[string]any
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.UseNumber()
		if dec.Decode(&m) == nil {
			for k, v := range m {
				p[k] = v
			}
		}
	case ct == "application/x-www-form-urlencoded" && r.Method != http.MethodGet:
		if vs, err := url.ParseQuery(string(body)); err == nil {
			for k, v := range vs {
				p[k] = v[len(v)-1]
			}
		}
	}
	for k, v := range p {
		p[k] = normalize(k, v)
	}
	return p
}

func normalize(key string, v any) any {
	switch x := v.(type) {
	case string:
		if key != "password" && key != "password_confirmation" {
			x = phpTrim(x)
		}
		if x == "" {
			return nil
		}
		return x
	case map[string]any:
		for k, vv := range x {
			x[k] = normalize(k, vv)
		}
	case []any:
		for i, vv := range x {
			x[i] = normalize("", vv)
		}
	}
	return v
}

func phpTrim(s string) string { return strings.Trim(s, " \t\n\r\x00\x0B") }

// phpStr PHP 的字符串转换：nil 为空串，true 为 "1"，false 为空串，浮点按 precision=14 输出，数组为 "Array"。
func phpStr(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "1"
		}
		return ""
	case json.Number:
		if i, ok := phpInt(x); ok {
			return strconv.FormatInt(i, 10)
		}
		f, _ := x.Float64()
		return phpFloat(f)
	case float64:
		return phpFloat(x)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case map[string]any, []any:
		return "Array"
	}
	return ""
}

// phpInt json_decode 后是否为整数（不含小数点与指数且在 int64 范围内）。
func phpInt(n json.Number) (int64, bool) {
	if strings.ContainsAny(string(n), ".eE") {
		return 0, false
	}
	i, err := strconv.ParseInt(string(n), 10, 64)
	return i, err == nil
}

// phpFloat 浮点数转字符串（PHP precision=14，"%.14G"，指数形式的尾数至少带一位小数，如 1.0E+20）。
func phpFloat(f float64) string {
	s := strconv.FormatFloat(f, 'G', 14, 64)
	if i := strings.IndexByte(s, 'E'); i >= 0 && !strings.Contains(s[:i], ".") {
		s = s[:i] + ".0" + s[i:]
	}
	return s
}

// phpDiv100 模拟 PHP 的 $amount / 100：能整除时为整数，否则为浮点。
func phpDiv100(amount int64) any {
	if amount%100 == 0 {
		return amount / 100
	}
	return float64(amount) / 100
}

// phpURLEncode PHP urlencode：字母数字与 -_. 保留，空格为 +，其余 %XX（大写）。
func phpURLEncode(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9', c == '-', c == '_', c == '.':
			b.WriteByte(c)
		case c == ' ':
			b.WriteByte('+')
		default:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}

// pair 有序的查询参数。
type pair struct{ k, v string }

// flatten 按 http_build_query 的规则展开参数（键按字节序排序，跳过 nil，布尔为 1 / 0，嵌套数组展开为 a[b]）。
func flatten(p map[string]any, prefix string) []pair {
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []pair
	for _, k := range keys {
		name := k
		if prefix != "" {
			name = prefix + "[" + k + "]"
		}
		switch x := p[k].(type) {
		case nil:
		case bool:
			v := "0"
			if x {
				v = "1"
			}
			out = append(out, pair{name, v})
		case map[string]any:
			out = append(out, flatten(x, name)...)
		case []any:
			m := make(map[string]any, len(x))
			for i, v := range x {
				m[strconv.Itoa(i)] = v
			}
			out = append(out, flattenList(m, len(x), name)...)
		default:
			out = append(out, pair{name, phpStr(x)})
		}
	}
	return out
}

// flattenList 列表按下标顺序展开（字节序排序会把 10 排在 2 前面）。
func flattenList(m map[string]any, n int, name string) []pair {
	var out []pair
	for i := 0; i < n; i++ {
		k := strconv.Itoa(i)
		out = append(out, flatten(map[string]any{k: m[k]}, name)...)
	}
	return out
}

// rawQuery 等价于 stripslashes(urldecode(http_build_query(ksort($p))))：键排序后按 k=v 原样拼接。
func rawQuery(p map[string]any) string {
	ps := flatten(p, "")
	parts := make([]string, len(ps))
	for i, x := range ps {
		parts[i] = x.k + "=" + x.v
	}
	return stripslashes(strings.Join(parts, "&"))
}

// encodeQuery 等价于 http_build_query，按给定顺序编码。
func encodeQuery(ps []pair) string {
	parts := make([]string, len(ps))
	for i, x := range ps {
		parts[i] = phpURLEncode(x.k) + "=" + phpURLEncode(x.v)
	}
	return strings.Join(parts, "&")
}

// stripslashes PHP stripslashes：去掉反斜杠，"\\" 变为 "\"，"\0" 变为 NUL。
func stripslashes(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}
		i++
		if i < len(s) {
			if s[i] == '0' {
				b.WriteByte(0)
			} else {
				b.WriteByte(s[i])
			}
		}
	}
	return b.String()
}

// numberFormat2 number_format($v, 2, '.', ”)。
func numberFormat2(amount int64) string {
	return strconv.FormatFloat(float64(amount)/100, 'f', 2, 64)
}
