package legacy

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"mime"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"gvboard/internal/phpx"
)

const inputKey = "gvb.legacy.input"

// notTrimmed 不去除首尾空白的参数（原项目 TrimStrings 中间件的 except）。
var notTrimmed = map[string]bool{"password": true, "password_confirmation": true}

// input 请求参数，与 Laravel $request->input() 一致：合并查询参数与请求体（请求体优先），
// 字符串去除首尾空白，空字符串视为 null（TrimStrings、ConvertEmptyStringsToNull 中间件）。
type input map[string]any

// params 解析并缓存当前请求的参数（认证中间件与处理函数共用，请求体只读取一次）。
func params(c *gin.Context) input {
	if v, ok := c.Get(inputKey); ok {
		return v.(input)
	}
	in := formInput(c.Request.URL.Query())
	var body map[string]any
	switch mediaType(c.GetHeader("Content-Type")) {
	case "application/json":
		raw, err := io.ReadAll(c.Request.Body)
		if err == nil && len(bytes.TrimSpace(raw)) > 0 {
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.UseNumber()
			_ = dec.Decode(&body)
		}
	case "application/x-www-form-urlencoded":
		if c.Request.ParseForm() == nil {
			body = formInput(c.Request.PostForm)
		}
	case "multipart/form-data":
		if c.Request.ParseMultipartForm(1<<20) == nil && c.Request.MultipartForm != nil {
			body = formInput(c.Request.MultipartForm.Value)
		}
	}
	for k, v := range body {
		in[k] = v
	}
	for k, v := range in {
		in[k] = normalize(k, v)
	}
	c.Set(inputKey, in)
	return in
}

// formInput 按 PHP 规则解析表单参数：a[b][c]=v 为嵌套对象，a[]=v 追加到数组，下标连续（0、1、2…）的对象视为数组；
// 同名参数取最后一个值。原前端提交对象与数组时使用这种格式。
func formInput(form map[string][]string) input {
	tree := map[string]any{}
	for k, vs := range form {
		base, segs := splitKey(k)
		if base == "" || len(vs) == 0 {
			continue
		}
		if n := len(segs); n > 0 && segs[n-1] == "" {
			for _, v := range vs {
				assign(tree, base, segs, v)
			}
			continue
		}
		assign(tree, base, segs, vs[len(vs)-1])
	}
	in := input{}
	for k, v := range tree {
		in[k] = listify(v)
	}
	return in
}

// splitKey 把 a[b][] 拆为 a 与 ["b", ""]；方括号不完整时整体作为普通参数名。
func splitKey(k string) (string, []string) {
	i := strings.IndexByte(k, '[')
	if i <= 0 {
		return k, nil
	}
	base, rest := k[:i], k[i:]
	var segs []string
	for rest != "" {
		end := strings.IndexByte(rest, ']')
		if rest[0] != '[' || end < 0 {
			return k, nil
		}
		segs = append(segs, rest[1:end])
		rest = rest[end+1:]
	}
	return base, segs
}

func assign(node map[string]any, key string, segs []string, v string) {
	if len(segs) == 0 {
		node[key] = v
		return
	}
	child, ok := node[key].(map[string]any)
	if !ok {
		child = map[string]any{}
		node[key] = child
	}
	seg := segs[0]
	if seg == "" {
		next := 0
		for k := range child {
			if n, err := strconv.Atoi(k); err == nil && n >= next {
				next = n + 1
			}
		}
		seg = strconv.Itoa(next)
	}
	assign(child, seg, segs[1:], v)
}

// listify 把下标为 0..n-1 的对象转为数组。
func listify(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	for k, x := range m {
		m[k] = listify(x)
	}
	list := make([]any, len(m))
	for k, x := range m {
		n, err := strconv.Atoi(k)
		if err != nil || n < 0 || n >= len(m) || strconv.Itoa(n) != k {
			return m
		}
		list[n] = x
	}
	return list
}

func mediaType(ct string) string {
	t, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return ""
	}
	return t
}

// values 数组参数的元素；对象按键排序后取值。不是数组时 ok 为 false。
func (in input) values(k string) ([]any, bool) {
	switch x := in[k].(type) {
	case []any:
		return x, true
	case map[string]any:
		keys := make([]string, 0, len(x))
		for kk := range x {
			keys = append(keys, kk)
		}
		sort.Slice(keys, func(i, j int) bool {
			a, errA := strconv.Atoi(keys[i])
			b, errB := strconv.Atoi(keys[j])
			if errA == nil && errB == nil {
				return a < b
			}
			return keys[i] < keys[j]
		})
		out := make([]any, len(keys))
		for i, kk := range keys {
			out[i] = x[kk]
		}
		return out, true
	}
	return nil, false
}

// strList 数组参数转为字符串列表，不是数组时返回 nil。
func (in input) strList(k string) []string {
	list, ok := in.values(k)
	if !ok {
		return nil
	}
	out := make([]string, len(list))
	for i := range list {
		out[i] = input{"v": list[i]}.str("v")
	}
	return out
}

// intList 整数数组参数，不是数组或任一元素不是整数时 ok 为 false。
func (in input) intList(k string) ([]int64, bool) {
	list, ok := in.values(k)
	if !ok {
		return nil, false
	}
	out := make([]int64, len(list))
	for i := range list {
		n, isInt := input{"v": list[i]}.int("v")
		if !isInt {
			return nil, false
		}
		out[i] = n
	}
	return out, true
}

// isArray 参数是否为数组或对象（PHP array 规则）。
func (in input) isArray(k string) bool {
	switch in[k].(type) {
	case []any, map[string]any:
		return true
	}
	return false
}

func normalize(k string, v any) any {
	switch x := v.(type) {
	case string:
		if !notTrimmed[k] {
			x = strings.TrimSpace(x)
		}
		if x == "" {
			return nil
		}
		return x
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return n
		}
		f, _ := x.Float64()
		return f
	case []any:
		for i := range x {
			x[i] = normalize("", x[i])
		}
		return x
	case map[string]any:
		for kk := range x {
			x[kk] = normalize(kk, x[kk])
		}
		return x
	}
	return v
}

// has 参数是否存在（值可以为 null），对应 $request->has()。
func (in input) has(k string) bool {
	_, ok := in[k]
	return ok
}

// raw 参数原值，不存在为 nil。
func (in input) raw(k string) any { return in[k] }

// truthy 参数按 PHP 规则是否为真（if ($request->input(k))）。
func (in input) truthy(k string) bool {
	switch x := in[k].(type) {
	case nil:
		return false
	case string:
		return x != "" && x != "0"
	case bool:
		return x
	case int64:
		return x != 0
	case float64:
		return x != 0
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return true
}

// str 参数的字符串形式，null 为空字符串，数组与对象为空字符串。
func (in input) str(k string) string {
	switch x := in[k].(type) {
	case string:
		return x
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return phpx.FloatStr(x)
	case bool:
		if x {
			return "1"
		}
	}
	return ""
}

// isString 参数是否为字符串（Laravel string 规则）。数字也按字符串处理，与表单提交一致。
func (in input) isString(k string) bool {
	switch in[k].(type) {
	case string, int64, float64:
		return true
	}
	return false
}

// int 参数的整数值（Laravel integer 规则：整数或整数形式的字符串）。
func (in input) int(k string) (int64, bool) {
	switch x := in[k].(type) {
	case int64:
		return x, true
	case float64:
		if x == float64(int64(x)) {
			return int64(x), true
		}
	case string:
		n, err := strconv.ParseInt(x, 10, 64)
		return n, err == nil
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

// float 参数的数值（Laravel numeric 规则）。
func (in input) float(k string) (float64, bool) {
	switch x := in[k].(type) {
	case int64:
		return float64(x), true
	case float64:
		return x, true
	case string:
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
	}
	return 0, false
}

// optStr 参数存在时取参数值（null 为 nil），否则保留 cur。
func (in input) optStr(k string, cur *string) *string {
	if !in.has(k) {
		return cur
	}
	if in.raw(k) == nil {
		return nil
	}
	s := in.str(k)
	return &s
}

// intOr 整数参数，缺失或格式不正确时返回 def。
func (in input) intOr(k string, def int64) int64 {
	if n, ok := in.int(k); ok {
		return n
	}
	return def
}
