package phpx

import (
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/dlclark/regexp2"
)

// Regex PHP preg_* 风格的正则（含定界符与修饰符，如 /香港|HK/i）。
type Regex struct{ re *regexp2.Regexp }

var regexCache sync.Map // string → *Regex（nil 表示不是合法正则）

// regexTimeout 单次匹配的最长时间，防止管理员配置的正则回溯过多。
const regexTimeout = 50 * time.Millisecond

// CompileRegex 按 PHP preg_match 的规则解析定界符与修饰符，不合法时返回 nil
// （对应 @preg_match($exp, ”) === false）。
func CompileRegex(exp string) *Regex {
	if v, ok := regexCache.Load(exp); ok {
		r, _ := v.(*Regex)
		return r
	}
	r := compileRegex(exp)
	regexCache.Store(exp, r)
	return r
}

func compileRegex(exp string) *Regex {
	p := strings.TrimLeft(exp, " \t\n\r\v\f")
	if p == "" || !utf8.ValidString(p) {
		return nil
	}
	delim := p[0]
	if isAlnum(delim) || delim == '\\' || delim >= utf8.RuneSelf {
		return nil
	}
	end := delim
	switch delim {
	case '(':
		end = ')'
	case '[':
		end = ']'
	case '{':
		end = '}'
	case '<':
		end = '>'
	}
	i, depth := 1, 1
	for ; i < len(p); i++ {
		c := p[i]
		if c == '\\' && i+1 < len(p) {
			i++
			continue
		}
		if c == end && (end == delim || depth <= 1) {
			break
		}
		if end != delim {
			if c == end {
				depth--
			} else if c == delim {
				depth++
			}
		}
	}
	if i >= len(p) {
		return nil
	}
	pattern, mods := p[1:i], p[i+1:]
	opts := regexp2.RegexOptions(0)
	anchored := false
	for _, m := range mods {
		switch m {
		case 'i':
			opts |= regexp2.IgnoreCase
		case 'm':
			opts |= regexp2.Multiline
		case 's':
			opts |= regexp2.Singleline
		case 'x':
			opts |= regexp2.IgnorePatternWhitespace
		case 'A':
			anchored = true
		case 'u', 'D', 'S', 'U', 'X', 'J', 'n', ' ', '\n', '\r':
		default:
			return nil
		}
	}
	if anchored {
		pattern = `\G(?:` + pattern + `)`
	}
	re, err := regexp2.Compile(posixClasses.Replace(pattern), opts)
	if err != nil {
		return nil
	}
	re.MatchTimeout = regexTimeout
	return &Regex{re: re}
}

// posixClasses PCRE 方括号内的 POSIX 字符类（regexp2 不支持），按 PCRE 的 ASCII 含义展开。
var posixClasses = strings.NewReplacer(
	"[:alpha:]", "a-zA-Z",
	"[:digit:]", "0-9",
	"[:alnum:]", "a-zA-Z0-9",
	"[:upper:]", "A-Z",
	"[:lower:]", "a-z",
	"[:space:]", `\t\n\v\f\r `,
	"[:blank:]", `\t `,
	"[:punct:]", `!-/:-@\[-`+"`"+`{-~`,
	"[:xdigit:]", "0-9A-Fa-f",
	"[:word:]", `\w`,
	"[:cntrl:]", `\x00-\x1f\x7f`,
	"[:print:]", `\x20-\x7e`,
	"[:graph:]", `\x21-\x7e`,
	"[:ascii:]", `\x00-\x7f`,
	"[:^digit:]", `\D`,
	"[:^space:]", `\S`,
	"[:^word:]", `\W`,
)

func isAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// Match 是否匹配（preg_match 返回 1），超时视为不匹配。
func (r *Regex) Match(s string) bool {
	if r == nil {
		return false
	}
	ok, err := r.re.MatchString(s)
	return err == nil && ok
}
