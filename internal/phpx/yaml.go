package phpx

import (
	"encoding/base64"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// YAMLParse 解析 YAML 为 PHP 值（映射与序列都转为 *Arr，保持顺序），对应 Symfony Yaml::parse。
func YAMLParse(src []byte) (any, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, err
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return nil, nil
	}
	return yamlValue(doc.Content[0], 0)
}

// ParseYAMLArr 解析 YAML，顶层必须是映射或序列。
func ParseYAMLArr(src []byte) (*Arr, bool) {
	v, err := YAMLParse(src)
	a, ok := v.(*Arr)
	return a, err == nil && ok
}

// ParseJSONArr 解析 JSON，顶层必须是对象或数组。
func ParseJSONArr(src []byte) (*Arr, bool) {
	v, err := JSONDecode(src)
	a, ok := v.(*Arr)
	return a, err == nil && ok
}

func yamlValue(n *yaml.Node, depth int) (any, error) {
	if depth > 100 {
		return nil, fmt.Errorf("YAML 嵌套过深")
	}
	switch n.Kind {
	case yaml.AliasNode:
		return yamlValue(n.Alias, depth+1)
	case yaml.SequenceNode:
		a := New()
		for _, c := range n.Content {
			v, err := yamlValue(c, depth+1)
			if err != nil {
				return nil, err
			}
			a.Push(v)
		}
		return a, nil
	case yaml.MappingNode:
		a := New()
		var merges []*Arr
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, vn := n.Content[i], n.Content[i+1]
			v, err := yamlValue(vn, depth+1)
			if err != nil {
				return nil, err
			}
			if k.Kind == yaml.ScalarNode && k.Tag == "!!merge" {
				switch x := v.(type) {
				case *Arr:
					if vn.Kind == yaml.SequenceNode {
						for _, m := range x.Values() {
							if ma, ok := m.(*Arr); ok {
								merges = append(merges, ma)
							}
						}
					} else {
						merges = append(merges, x)
					}
				}
				continue
			}
			kv, err := yamlValue(k, depth+1)
			if err != nil {
				return nil, err
			}
			a.Set(NormKey(kv), v)
		}
		for _, m := range merges {
			m.Each(func(k, v any) {
				if _, ok := a.Lookup(k); !ok {
					a.Set(k, v)
				}
			})
		}
		return a, nil
	case yaml.ScalarNode:
		switch n.ShortTag() {
		case "!!null":
			return nil, nil
		case "!!bool":
			var b bool
			if err := n.Decode(&b); err != nil {
				return n.Value, nil
			}
			return b, nil
		case "!!int":
			var i int64
			if err := n.Decode(&i); err != nil {
				var f float64
				if err := n.Decode(&f); err == nil {
					return f, nil
				}
				return n.Value, nil
			}
			return i, nil
		case "!!float":
			var f float64
			if err := n.Decode(&f); err != nil {
				return n.Value, nil
			}
			return f, nil
		case "!!binary":
			b, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(n.Value), ""))
			if err != nil {
				return n.Value, nil
			}
			return string(b), nil
		}
		return n.Value, nil
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, nil
		}
		return yamlValue(n.Content[0], depth+1)
	}
	return nil, nil
}

// YAMLDump 对应 Symfony Yaml::dump($v, $inline, $indent, $flags)：层级达到 inline 后用行内格式输出。
// emptyAsSeq 对应 Yaml::DUMP_EMPTY_ARRAY_AS_SEQUENCE（空数组输出为 []，否则为 {  }）。
func YAMLDump(v any, inline, indent int, emptyAsSeq bool) string {
	return yamlDump(v, inline, 0, indent, emptyAsSeq)
}

func yamlDump(v any, inline, indent, step int, emptyAsSeq bool) string {
	prefix := strings.Repeat(" ", indent)
	a, ok := v.(*Arr)
	if inline <= 0 || !ok || a.Len() == 0 {
		return prefix + yamlInline(v, emptyAsSeq)
	}
	asMap := !a.IsList()
	var b strings.Builder
	for _, k := range a.keys {
		val := a.vals[k]
		if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
			b.WriteByte('\n')
		}
		sub, isArr := val.(*Arr)
		inlined := inline-1 <= 0 || !isArr || sub.Len() == 0
		b.WriteString(prefix)
		if asMap {
			b.WriteString(yamlInline(k, emptyAsSeq) + ":")
		} else {
			b.WriteString("-")
		}
		if inlined {
			b.WriteString(" " + yamlDump(val, inline-1, 0, step, emptyAsSeq) + "\n")
		} else {
			b.WriteString("\n" + yamlDump(val, inline-1, indent+step, step, emptyAsSeq))
		}
	}
	return b.String()
}

var (
	yamlDigits    = regexp.MustCompile(`^[0-9]+[_0-9]*$`)
	yamlHex       = regexp.MustCompile(`(?i)^0x[0-9a-f_]+$`)
	yamlTimestamp = regexp.MustCompile(`^[0-9]{4}-[0-9][0-9]?-[0-9][0-9]?(?:(?:[Tt]|[ \t]+)[0-9][0-9]?:[0-9]{2}:[0-9]{2}(?:\.[0-9]*)?(?:[ \t]*(?:Z|[-+][0-9][0-9]?(?::[0-9]{2})?))?)?\n?$`)
	yamlSingle    = regexp.MustCompile(`[\s'":{}\[\],&*#?]|^[-?|<>=!%@` + "`" + `]`)
	yamlDouble    = regexp.MustCompile("[\x00-\x1f\x7f]|\u0085|\u00a0|\u2028|\u2029")
	yamlBinary    = regexp.MustCompile("[\x01-\x06\x0e-\x1a\x1c-\x1f]")
)

func yamlInline(v any, emptyAsSeq bool) string {
	switch x := v.(type) {
	case *Arr:
		if (x.Len() > 0 || emptyAsSeq) && x.IsList() {
			parts := make([]string, 0, x.Len())
			for _, e := range x.Values() {
				parts = append(parts, yamlInline(e, emptyAsSeq))
			}
			return "[" + strings.Join(parts, ", ") + "]"
		}
		parts := make([]string, 0, x.Len())
		x.Each(func(k, e any) {
			parts = append(parts, yamlInline(k, emptyAsSeq)+": "+yamlInline(e, emptyAsSeq))
		})
		return "{ " + strings.Join(parts, ", ") + " }"
	case nil:
		return "null"
	case bool:
		if x {
			return "true"
		}
		return "false"
	case int64:
		return strconv.FormatInt(x, 10)
	case int:
		return strconv.Itoa(x)
	case float64:
		repr := FloatStr(x)
		switch {
		case math.IsInf(x, 1):
			return ".Inf"
		case math.IsInf(x, -1):
			return "-.Inf"
		case math.IsNaN(x):
			return ".NaN"
		case math.Floor(x) == x && LooseEq(repr, x):
			return "!!float " + repr
		}
		return repr
	case string:
		return yamlString(x)
	}
	return yamlString(Str(v))
}

func yamlString(s string) string {
	switch {
	case s != "" && isDigits(s):
		return "'" + s + "'"
	case IsNumeric(s) && !strings.ContainsAny(s, "\f\n\r\t\v"):
		return "'" + s + "'"
	case s == "":
		return "''"
	case !utf8.ValidString(s) || yamlBinary.MatchString(s):
		return "!!binary " + base64.StdEncoding.EncodeToString([]byte(s))
	case yamlDouble.MatchString(s):
		return yamlDoubleQuote(s)
	case yamlNeedsSingle(s), yamlDigits.MatchString(s), yamlHex.MatchString(s), yamlTimestamp.MatchString(s):
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	return s
}

func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func yamlNeedsSingle(s string) bool {
	switch strings.ToLower(s) {
	case "null", "~", "true", "false", "y", "n", "yes", "no", "on", "off":
		return true
	}
	return yamlSingle.MatchString(s)
}

func yamlDoubleQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case 0:
			b.WriteString(`\0`)
		case '\a':
			b.WriteString(`\a`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\v':
			b.WriteString(`\v`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		case 0x1b:
			b.WriteString(`\e`)
		case 0x85:
			b.WriteString(`\N`)
		case 0xa0:
			b.WriteString(`\_`)
		case 0x2028:
			b.WriteString(`\L`)
		case 0x2029:
			b.WriteString(`\P`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\x%02x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
