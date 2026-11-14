package setting

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// ParsePHPConfig 解析原项目 config/v2board.php（var_export 生成的 "return array (...);" 格式）。
// 只解析字面量（数组、字符串、数字、布尔、NULL），不执行任何 PHP 代码；遇到函数调用等表达式直接报错。
func ParsePHPConfig(src []byte) (map[string]any, error) {
	p := &phpParser{s: strings.TrimPrefix(string(src), "\uFEFF")}
	p.skipSpace()
	if strings.HasPrefix(p.s[p.i:], "<?php") {
		p.i += len("<?php")
	}
	p.skipSpace()
	if !p.consumeWord("return") {
		return nil, p.errorf("缺少 return 语句")
	}
	v, err := p.value()
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if p.i < len(p.s) && p.s[p.i] == ';' {
		p.i++
	}
	m, ok := v.(map[string]any)
	if !ok {
		if arr, isArr := v.([]any); isArr && len(arr) == 0 {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("配置文件顶层必须是关联数组")
	}
	return m, nil
}

type phpParser struct {
	s string
	i int
}

func (p *phpParser) errorf(format string, args ...any) error {
	line := strings.Count(p.s[:min(p.i, len(p.s))], "\n") + 1
	return fmt.Errorf("配置文件第 %d 行: %s", line, fmt.Sprintf(format, args...))
}

func (p *phpParser) skipSpace() {
	for p.i < len(p.s) {
		c := p.s[p.i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			p.i++
		case c == '#' || (c == '/' && p.peek(1) == '/'):
			for p.i < len(p.s) && p.s[p.i] != '\n' {
				p.i++
			}
		case c == '/' && p.peek(1) == '*':
			end := strings.Index(p.s[p.i+2:], "*/")
			if end < 0 {
				p.i = len(p.s)
				return
			}
			p.i += end + 4
		default:
			return
		}
	}
}

func (p *phpParser) peek(off int) byte {
	if p.i+off < len(p.s) {
		return p.s[p.i+off]
	}
	return 0
}

// consumeWord 不区分大小写匹配关键字，且要求关键字后不是标识符字符。
func (p *phpParser) consumeWord(w string) bool {
	if len(p.s)-p.i < len(w) || !strings.EqualFold(p.s[p.i:p.i+len(w)], w) {
		return false
	}
	if next := p.peek(len(w)); next != 0 && (isIdentChar(next)) {
		return false
	}
	p.i += len(w)
	return true
}

func isIdentChar(c byte) bool {
	return c == '_' || c >= 0x80 || unicode.IsLetter(rune(c)) || unicode.IsDigit(rune(c))
}

func (p *phpParser) value() (any, error) {
	p.skipSpace()
	if p.i >= len(p.s) {
		return nil, p.errorf("意外的文件结尾")
	}
	c := p.s[p.i]
	switch {
	case c == '\'':
		return p.singleQuoted()
	case c == '"':
		return p.doubleQuoted()
	case c == '[':
		p.i++
		return p.arrayBody(']')
	case c == '-' || c == '+' || c == '.' || (c >= '0' && c <= '9'):
		return p.number()
	}
	switch {
	case p.consumeWord("array"):
		p.skipSpace()
		if p.peek(0) != '(' {
			return nil, p.errorf("array 后缺少 (")
		}
		p.i++
		return p.arrayBody(')')
	case p.consumeWord("true"):
		return true, nil
	case p.consumeWord("false"):
		return false, nil
	case p.consumeWord("null"):
		return nil, nil
	}
	return nil, p.errorf("不支持的表达式（只允许字面量）: %q", snippet(p.s[p.i:]))
}

func snippet(s string) string {
	if len(s) > 30 {
		return s[:30] + "..."
	}
	return s
}

// arrayBody 解析数组元素直到 closer。键连续为 0..n-1 时返回 []any，否则返回 map[string]any。
func (p *phpParser) arrayBody(closer byte) (any, error) {
	type entry struct {
		key any
		val any
	}
	var entries []entry
	nextIndex := int64(0)
	for {
		p.skipSpace()
		if p.peek(0) == closer {
			p.i++
			break
		}
		first, err := p.value()
		if err != nil {
			return nil, err
		}
		p.skipSpace()
		var key, val any
		if strings.HasPrefix(p.s[p.i:], "=>") {
			p.i += 2
			if val, err = p.value(); err != nil {
				return nil, err
			}
			key = first
			if n, ok := first.(int64); ok && n >= nextIndex {
				nextIndex = n + 1
			}
		} else {
			key, val = nextIndex, first
			nextIndex++
		}
		switch key.(type) {
		case int64, string:
		default:
			return nil, p.errorf("数组键只能是整数或字符串")
		}
		entries = append(entries, entry{key, val})
		p.skipSpace()
		switch p.peek(0) {
		case ',':
			p.i++
		case closer:
		default:
			return nil, p.errorf("数组元素之间缺少逗号")
		}
	}

	sequential := true
	for i, e := range entries {
		if n, ok := e.key.(int64); !ok || n != int64(i) {
			sequential = false
			break
		}
	}
	if sequential {
		out := make([]any, len(entries))
		for i, e := range entries {
			out[i] = e.val
		}
		return out, nil
	}
	out := make(map[string]any, len(entries))
	for _, e := range entries {
		switch k := e.key.(type) {
		case string:
			out[k] = e.val
		case int64:
			out[strconv.FormatInt(k, 10)] = e.val
		}
	}
	return out, nil
}

func (p *phpParser) singleQuoted() (any, error) {
	p.i++
	var b strings.Builder
	for p.i < len(p.s) {
		c := p.s[p.i]
		if c == '\\' && (p.peek(1) == '\'' || p.peek(1) == '\\') {
			b.WriteByte(p.peek(1))
			p.i += 2
			continue
		}
		if c == '\'' {
			p.i++
			return b.String(), nil
		}
		b.WriteByte(c)
		p.i++
	}
	return nil, p.errorf("字符串未闭合")
}

func (p *phpParser) doubleQuoted() (any, error) {
	p.i++
	var b strings.Builder
	for p.i < len(p.s) {
		c := p.s[p.i]
		switch c {
		case '"':
			p.i++
			return b.String(), nil
		case '$':
			return nil, p.errorf("双引号字符串中不支持变量插值")
		case '\\':
			esc := p.peek(1)
			switch esc {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '\\', '"', '$':
				b.WriteByte(esc)
			default:
				b.WriteByte('\\')
				b.WriteByte(esc)
			}
			p.i += 2
			continue
		}
		b.WriteByte(c)
		p.i++
	}
	return nil, p.errorf("字符串未闭合")
}

func (p *phpParser) number() (any, error) {
	start := p.i
	if c := p.peek(0); c == '-' || c == '+' {
		p.i++
	}
	isFloat := false
	for p.i < len(p.s) {
		c := p.s[p.i]
		if c >= '0' && c <= '9' {
			p.i++
		} else if c == '.' || c == 'e' || c == 'E' {
			isFloat = true
			p.i++
			if (c == 'e' || c == 'E') && (p.peek(0) == '-' || p.peek(0) == '+') {
				p.i++
			}
		} else {
			break
		}
	}
	lit := p.s[start:p.i]
	if isFloat {
		f, err := strconv.ParseFloat(lit, 64)
		if err != nil {
			return nil, p.errorf("数字格式错误: %s", lit)
		}
		return f, nil
	}
	n, err := strconv.ParseInt(lit, 10, 64)
	if err != nil {
		return nil, p.errorf("数字格式错误: %s", lit)
	}
	return n, nil
}
