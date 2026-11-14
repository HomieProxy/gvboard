package phpx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// JSON 编码选项，对应 PHP 的 JSON_* 常量。
const (
	JSONUnescapedSlashes = 1 << iota
	JSONUnescapedUnicode
	JSONPretty
)

// MarshalJSON 供 encoding/json 使用，按 PHP json_encode（不转义斜杠与中文）输出。
func (a *Arr) MarshalJSON() ([]byte, error) {
	return JSONEncode(a, JSONUnescapedSlashes|JSONUnescapedUnicode), nil
}

// JSONEncode 按 PHP json_encode 输出：默认转义 "/" 与非 ASCII 字符，整数值的浮点数不带小数部分。
func JSONEncode(v any, flags int) []byte {
	var b bytes.Buffer
	encodeJSON(&b, v, flags, 0)
	return b.Bytes()
}

func encodeJSON(b *bytes.Buffer, v any, flags, depth int) {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if x {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case int:
		b.WriteString(strconv.Itoa(x))
	case int64:
		b.WriteString(strconv.FormatInt(x, 10))
	case float64:
		b.WriteString(jsonFloat(x))
	case string:
		jsonString(b, x, flags)
	case []string:
		encodeJSON(b, Strings(x...), flags, depth)
	case *Arr:
		if x == nil {
			b.WriteString("null")
			return
		}
		list := x.IsList()
		open, close := byte('{'), byte('}')
		if list {
			open, close = '[', ']'
		}
		b.WriteByte(open)
		if x.Len() == 0 {
			b.WriteByte(close)
			return
		}
		pretty := flags&JSONPretty != 0
		for i, k := range x.keys {
			if i > 0 {
				b.WriteByte(',')
			}
			if pretty {
				b.WriteByte('\n')
				b.WriteString(strings.Repeat("    ", depth+1))
			}
			if !list {
				jsonString(b, Str(k), flags)
				b.WriteByte(':')
				if pretty {
					b.WriteByte(' ')
				}
			}
			encodeJSON(b, x.vals[k], flags, depth+1)
		}
		if pretty {
			b.WriteByte('\n')
			b.WriteString(strings.Repeat("    ", depth))
		}
		b.WriteByte(close)
	default:
		b.WriteString("null")
	}
}

// jsonFloat serialize_precision=-1：最短可还原表示，整数值不带小数部分（如 12.0 为 12），
// 小数点位置小于 -3 或大于 17 时用科学计数法（如 1.0e+25）。
func jsonFloat(f float64) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "0"
	}
	if f == 0 {
		if math.Signbit(f) {
			return "-0"
		}
		return "0"
	}
	s := strconv.FormatFloat(f, 'e', -1, 64)
	neg := s[0] == '-'
	if neg {
		s = s[1:]
	}
	i := strings.IndexByte(s, 'e')
	digits := strings.Replace(s[:i], ".", "", 1)
	exp, _ := strconv.Atoi(s[i+1:])
	decpt := exp + 1
	var out string
	switch {
	case decpt < -3 || decpt > 17:
		mant := digits[:1] + "." + digits[1:]
		if len(digits) == 1 {
			mant += "0"
		}
		sign := "+"
		if exp < 0 {
			sign, exp = "-", -exp
		}
		out = mant + "e" + sign + strconv.Itoa(exp)
	case decpt <= 0:
		out = "0." + strings.Repeat("0", -decpt) + digits
	case decpt >= len(digits):
		out = digits + strings.Repeat("0", decpt-len(digits))
	default:
		out = digits[:decpt] + "." + digits[decpt:]
	}
	if neg {
		out = "-" + out
	}
	return out
}

func jsonString(b *bytes.Buffer, s string, flags int) {
	b.WriteByte('"')
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			switch c {
			case '"':
				b.WriteString(`\"`)
			case '\\':
				b.WriteString(`\\`)
			case '/':
				if flags&JSONUnescapedSlashes != 0 {
					b.WriteByte('/')
				} else {
					b.WriteString(`\/`)
				}
			case '\b':
				b.WriteString(`\b`)
			case '\f':
				b.WriteString(`\f`)
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			case '\t':
				b.WriteString(`\t`)
			default:
				if c < 0x20 {
					fmt.Fprintf(b, `\u%04x`, c)
				} else {
					b.WriteByte(c)
				}
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			r = utf8.RuneError
		}
		if flags&JSONUnescapedUnicode != 0 && r != '\u2028' && r != '\u2029' {
			b.WriteRune(r)
		} else if r > 0xFFFF {
			r1, r2 := utf16.EncodeRune(r)
			fmt.Fprintf(b, `\u%04x\u%04x`, r1, r2)
		} else {
			fmt.Fprintf(b, `\u%04x`, r)
		}
		i += size
	}
	b.WriteByte('"')
}

// JSONDecode 按 json_decode($s, true) 解码：对象与数组都转为 *Arr，整数为 int64，其余数字为 float64。
func JSONDecode(data []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	v, err := decodeJSON(d)
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); err == nil {
		return nil, fmt.Errorf("JSON 末尾有多余内容")
	}
	return v, nil
}

func decodeJSON(d *json.Decoder) (any, error) {
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch x := t.(type) {
	case json.Delim:
		a := New()
		switch x {
		case '{':
			for d.More() {
				kt, err := d.Token()
				if err != nil {
					return nil, err
				}
				v, err := decodeJSON(d)
				if err != nil {
					return nil, err
				}
				a.Set(kt.(string), v)
			}
		case '[':
			for d.More() {
				v, err := decodeJSON(d)
				if err != nil {
					return nil, err
				}
				a.Push(v)
			}
		}
		if _, err := d.Token(); err != nil {
			return nil, err
		}
		return a, nil
	case json.Number:
		s := x.String()
		if !strings.ContainsAny(s, ".eE") {
			if n, err := strconv.ParseInt(s, 10, 64); err == nil {
				return n, nil
			}
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil && !strings.Contains(err.Error(), "range") {
			return nil, err
		}
		return f, nil
	}
	return t, nil
}
