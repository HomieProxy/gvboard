// Package phpx 按 PHP 8 语义处理动态数据，供需要与原项目逐字节一致的输出（订阅下发等）使用。
//
// 值的表示：nil（null）、bool、int64、float64、string、*Arr（PHP 数组）。
// 访问不存在的键一律得到 nil（原项目在 Laravel 下会因 Undefined array key 报 500，这里按 null 处理）。
package phpx

import (
	"math"
	"strconv"
	"strings"
)

// Arr 有序数组，对应 PHP array。键为 int64 或 string，数字字符串键按 PHP 规则转为整数。
type Arr struct {
	keys []any
	vals map[any]any
	next int64
}

// New 空数组。
func New() *Arr { return &Arr{vals: map[any]any{}} }

// List 以 0..n-1 为键的数组。
func List(vals ...any) *Arr {
	a := New()
	for _, v := range vals {
		a.Push(v)
	}
	return a
}

// Strings 字符串列表。
func Strings(vals ...string) *Arr {
	a := New()
	for _, v := range vals {
		a.Push(v)
	}
	return a
}

// Map 按顺序的键值对构造数组：Map("a", 1, "b", 2)。
func Map(kv ...any) *Arr {
	a := New()
	for i := 0; i+1 < len(kv); i += 2 {
		a.Set(kv[i], kv[i+1])
	}
	return a
}

// NormKey PHP 数组键规范化：整数、规范十进制整数字符串转为 int64，bool 转为 0/1，null 转为空串。
func NormKey(k any) any {
	switch x := k.(type) {
	case int:
		return int64(x)
	case int64:
		return x
	case bool:
		if x {
			return int64(1)
		}
		return int64(0)
	case nil:
		return ""
	case float64:
		return int64(x)
	case string:
		if n, ok := canonicalInt(x); ok {
			return n
		}
		return x
	}
	return Str(k)
}

func canonicalInt(s string) (int64, bool) {
	if s == "" || len(s) > 20 {
		return 0, false
	}
	if s == "0" {
		return 0, true
	}
	b := s
	if b[0] == '-' {
		b = b[1:]
	}
	if b == "" || b[0] < '1' || b[0] > '9' {
		return 0, false
	}
	for i := 0; i < len(b); i++ {
		if b[i] < '0' || b[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// Len 元素个数，nil 数组为 0。
func (a *Arr) Len() int {
	if a == nil {
		return 0
	}
	return len(a.keys)
}

// Set 设置键值，已有的键保持原位置。
func (a *Arr) Set(k, v any) *Arr {
	k = NormKey(k)
	if _, ok := a.vals[k]; !ok {
		a.keys = append(a.keys, k)
		if n, ok := k.(int64); ok && n >= a.next {
			a.next = n + 1
		}
	}
	a.vals[k] = v
	return a
}

// Push 追加元素（$a[] = v）。
func (a *Arr) Push(v any) *Arr {
	return a.Set(a.next, v)
}

// Unshift 在开头插入元素并重排整数键（array_unshift）。
func (a *Arr) Unshift(v any) {
	old := a.keys
	vals := a.vals
	a.keys, a.vals, a.next = nil, map[any]any{}, 0
	a.Push(v)
	for _, k := range old {
		if _, ok := k.(int64); ok {
			a.Push(vals[k])
		} else {
			a.Set(k, vals[k])
		}
	}
}

// Lookup 读取键，返回是否存在（array_key_exists）。
func (a *Arr) Lookup(k any) (any, bool) {
	if a == nil {
		return nil, false
	}
	v, ok := a.vals[NormKey(k)]
	return v, ok
}

// Del 删除键（unset）。
func (a *Arr) Del(k any) {
	if a == nil {
		return
	}
	k = NormKey(k)
	if _, ok := a.vals[k]; !ok {
		return
	}
	delete(a.vals, k)
	for i, x := range a.keys {
		if x == k {
			a.keys = append(a.keys[:i:i], a.keys[i+1:]...)
			break
		}
	}
}

// Keys 键列表（按顺序）。
func (a *Arr) Keys() []any {
	if a == nil {
		return nil
	}
	return append([]any(nil), a.keys...)
}

// Values 值列表（按顺序）。
func (a *Arr) Values() []any {
	if a == nil {
		return nil
	}
	out := make([]any, len(a.keys))
	for i, k := range a.keys {
		out[i] = a.vals[k]
	}
	return out
}

// Each 按顺序遍历。
func (a *Arr) Each(fn func(k, v any)) {
	if a == nil {
		return
	}
	for _, k := range a.keys {
		fn(k, a.vals[k])
	}
}

// IsList 键是否恰为 0..n-1（json_encode 输出为 JSON 数组的条件）。
func (a *Arr) IsList() bool {
	for i, k := range a.Keys() {
		if n, ok := k.(int64); !ok || n != int64(i) {
			return false
		}
	}
	return true
}

// Clone 深拷贝（PHP 数组是值类型，修改前需拷贝以免影响原数组）。
func (a *Arr) Clone() *Arr {
	if a == nil {
		return nil
	}
	b := &Arr{keys: append([]any(nil), a.keys...), vals: make(map[any]any, len(a.vals)), next: a.next}
	for k, v := range a.vals {
		if x, ok := v.(*Arr); ok {
			v = x.Clone()
		}
		b.vals[k] = v
	}
	return b
}

// SetPath 按路径赋值，中间层不存在时创建数组（$a['x']['y'] = v）。
func (a *Arr) SetPath(v any, path ...any) {
	cur := a
	for _, k := range path[:len(path)-1] {
		next, ok := cur.Get(k).(*Arr)
		if !ok || next == nil {
			next = New()
			cur.Set(k, next)
		}
		cur = next
	}
	cur.Set(path[len(path)-1], v)
}

// Get 按路径取值，任一层不存在或不是数组时返回 nil（$a['x']['y'] ?? null）。
func (a *Arr) Get(path ...any) any { return Get(a, path...) }

// Str 按路径取值并转为字符串（"{$a['x']}"）。
func (a *Arr) Str(path ...any) string { return Str(Get(a, path...)) }

// Isset 按路径取值是否存在且不为 null。
func (a *Arr) Isset(path ...any) bool { return Get(a, path...) != nil }

// Arr 按路径取子数组，不是数组时返回 nil。
func (a *Arr) Arr(path ...any) *Arr {
	x, _ := Get(a, path...).(*Arr)
	return x
}

// Get 按路径取值，v 不是数组或键不存在时返回 nil。
func Get(v any, path ...any) any {
	for _, k := range path {
		a, ok := v.(*Arr)
		if !ok || a == nil {
			return nil
		}
		v = a.vals[NormKey(k)]
	}
	return v
}

// Coalesce 第一个不为 null 的值（??）。
func Coalesce(vals ...any) any {
	for _, v := range vals {
		if v != nil {
			if a, ok := v.(*Arr); ok && a == nil {
				continue
			}
			return v
		}
	}
	return nil
}

// Empty PHP empty()：null、false、0、0.0、""、"0"、空数组。
func Empty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case bool:
		return !x
	case int64:
		return x == 0
	case int:
		return x == 0
	case float64:
		return x == 0
	case string:
		return x == "" || x == "0"
	case *Arr:
		return x.Len() == 0
	}
	return false
}

// Truthy 转为 bool。
func Truthy(v any) bool { return !Empty(v) }

// Str PHP 字符串转换。
func Str(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case bool:
		if x {
			return "1"
		}
		return ""
	case int64:
		return strconv.FormatInt(x, 10)
	case int:
		return strconv.Itoa(x)
	case float64:
		return FloatStr(x)
	case string:
		return x
	case *Arr:
		return "Array"
	}
	return ""
}

// FloatStr 浮点数转字符串（PHP precision=14，如 0.1+0.2 为 "0.3"，2.0 为 "2"）。
func FloatStr(f float64) string { return gcvt(f, 14, 'E') }

// gcvt 对应 PHP zend_gcvt：precision 位有效数字，指数小于 -4 或不小于 precision 时用科学计数法（如 1.0E+25）。
func gcvt(f float64, precision int, e byte) string {
	switch {
	case math.IsNaN(f):
		return "NAN"
	case math.IsInf(f, 1):
		return "INF"
	case math.IsInf(f, -1):
		return "-INF"
	}
	s := strconv.FormatFloat(f, 'g', precision, 64)
	i := strings.IndexByte(s, 'e')
	if i < 0 {
		return s
	}
	mant, exp := s[:i], s[i+1:]
	if !strings.Contains(mant, ".") {
		mant += ".0"
	}
	sign := exp[0]
	exp = strings.TrimLeft(exp[1:], "0")
	if exp == "" {
		exp = "0"
	}
	return mant + string(e) + string(sign) + exp
}

// Int PHP (int) 转换。
func Int(v any) int64 {
	switch x := v.(type) {
	case nil:
		return 0
	case bool:
		if x {
			return 1
		}
		return 0
	case int64:
		return x
	case int:
		return int64(x)
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return 0
		}
		return int64(x)
	case string:
		if f, isInt, n, ok := numeric(x, true); ok {
			if isInt {
				return n
			}
			return Int(f)
		}
		return 0
	case *Arr:
		if x.Len() > 0 {
			return 1
		}
		return 0
	}
	return 0
}

// IsNumeric PHP is_numeric（允许前导与尾随空白）。
func IsNumeric(s string) bool {
	_, _, _, ok := numeric(s, false)
	return ok && strings.TrimSpace(s) != ""
}

// numeric 解析数字字符串。prefix 为 true 时允许只取前导数字部分（(int)"443,8443" 为 443）。
func numeric(s string, prefix bool) (f float64, isInt bool, n int64, ok bool) {
	t := strings.TrimLeft(s, " \t\n\r\v\f")
	end := 0
	if end < len(t) && (t[end] == '+' || t[end] == '-') {
		end++
	}
	digits := 0
	for end < len(t) && t[end] >= '0' && t[end] <= '9' {
		end++
		digits++
	}
	isInt = true
	if end < len(t) && t[end] == '.' {
		j := end + 1
		frac := 0
		for j < len(t) && t[j] >= '0' && t[j] <= '9' {
			j++
			frac++
		}
		if digits+frac > 0 {
			end = j
			digits += frac
			isInt = false
		}
	}
	if digits == 0 {
		return 0, false, 0, false
	}
	if end < len(t) && (t[end] == 'e' || t[end] == 'E') {
		j := end + 1
		if j < len(t) && (t[j] == '+' || t[j] == '-') {
			j++
		}
		k := j
		for k < len(t) && t[k] >= '0' && t[k] <= '9' {
			k++
		}
		if k > j {
			end = k
			isInt = false
		}
	}
	rest := strings.TrimRight(t[end:], " \t\n\r\v\f")
	if rest != "" && !prefix {
		return 0, false, 0, false
	}
	num := t[:end]
	if isInt {
		if v, err := strconv.ParseInt(num, 10, 64); err == nil {
			return float64(v), true, v, true
		}
		isInt = false
	}
	v, err := strconv.ParseFloat(num, 64)
	if err != nil && !strings.Contains(err.Error(), "range") {
		return 0, false, 0, false
	}
	return v, false, 0, true
}

// toNumber 把数字字符串或数值转为 float64 / int64。
func toNumber(v any) (f float64, n int64, isInt, ok bool) {
	switch x := v.(type) {
	case int64:
		return float64(x), x, true, true
	case int:
		return float64(x), int64(x), true, true
	case float64:
		return x, 0, false, true
	case string:
		f, isInt, n, ok := numeric(x, false)
		if !ok || strings.TrimSpace(x) == "" {
			return 0, 0, false, false
		}
		return f, n, isInt, true
	}
	return 0, 0, false, false
}

// LooseEq PHP 8 的 == 比较（不支持数组之间的比较）。
func LooseEq(a, b any) bool { return Compare(a, b) == 0 }

// Compare PHP 8 的 <=> 比较（标量）。
func Compare(a, b any) int {
	if x, ok := a.(int); ok {
		a = int64(x)
	}
	if x, ok := b.(int); ok {
		b = int64(x)
	}
	switch x := a.(type) {
	case nil:
		switch y := b.(type) {
		case nil:
			return 0
		case string:
			return strings.Compare("", y)
		}
		return cmpBool(false, Truthy(b))
	case bool:
		return cmpBool(x, Truthy(b))
	case string:
		switch y := b.(type) {
		case nil:
			return strings.Compare(x, "")
		case bool:
			return cmpBool(Truthy(x), y)
		case string:
			fa, na, ia, oka := toNumber(x)
			fb, nb, ib, okb := toNumber(y)
			if oka && okb {
				return cmpNum(fa, na, ia, fb, nb, ib)
			}
			return sign(strings.Compare(x, y))
		case int64, float64:
			if fa, na, ia, ok := toNumber(x); ok {
				fb, nb, ib, _ := toNumber(y)
				return cmpNum(fa, na, ia, fb, nb, ib)
			}
			return sign(strings.Compare(x, Str(y)))
		}
	case int64, float64:
		switch b.(type) {
		case nil, bool, string:
			return -Compare(b, a)
		}
		fa, na, ia, _ := toNumber(a)
		fb, nb, ib, ok := toNumber(b)
		if ok {
			return cmpNum(fa, na, ia, fb, nb, ib)
		}
	case *Arr:
		if y, ok := b.(*Arr); ok {
			return cmpInt(int64(x.Len()), int64(y.Len()))
		}
		if b == nil || isBool(b) {
			return cmpBool(Truthy(a), Truthy(b))
		}
		return 1
	}
	return 1
}

func isBool(v any) bool { _, ok := v.(bool); return ok }

func cmpNum(fa float64, na int64, ia bool, fb float64, nb int64, ib bool) int {
	if ia && ib {
		return cmpInt(na, nb)
	}
	switch {
	case fa < fb:
		return -1
	case fa > fb:
		return 1
	}
	return 0
}

func cmpInt(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func cmpBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case !a:
		return -1
	}
	return 1
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}

// InArray in_array（宽松比较）。
func InArray(v any, a *Arr) bool {
	for _, x := range a.Values() {
		if LooseEq(v, x) {
			return true
		}
	}
	return false
}

// StrIn 字符串是否在列表中（in_array 对纯字符串列表的常见用法）。
func StrIn(v any, list ...string) bool {
	for _, s := range list {
		if LooseEq(v, s) {
			return true
		}
	}
	return false
}

// Merge array_merge：整数键重新编号，字符串键后者覆盖前者。nil 视为空数组。
func Merge(arrs ...*Arr) *Arr {
	out := New()
	for _, a := range arrs {
		a.Each(func(k, v any) {
			if _, ok := k.(int64); ok {
				out.Push(v)
			} else {
				out.Set(k, v)
			}
		})
	}
	return out
}

// ReplaceRecursive array_replace_recursive。
func ReplaceRecursive(base, repl *Arr) *Arr {
	out := base.Clone()
	if out == nil {
		out = New()
	}
	repl.Each(func(k, v any) {
		if rv, ok := v.(*Arr); ok {
			if bv, ok := out.Get(k).(*Arr); ok {
				out.Set(k, ReplaceRecursive(bv, rv))
				return
			}
			out.Set(k, rv.Clone())
			return
		}
		out.Set(k, v)
	})
	return out
}

// Filter array_filter（无回调）：去掉转为 bool 为 false 的元素，保留键。
func Filter(a *Arr) *Arr {
	out := New()
	a.Each(func(k, v any) {
		if Truthy(v) {
			out.Set(k, v)
		}
	})
	return out
}

// Implode implode。
func Implode(sep string, a *Arr) string {
	vals := a.Values()
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = Str(v)
	}
	return strings.Join(parts, sep)
}

// Column array_column（只取值）：跳过不含该键的元素。
func Column(rows []*Arr, key string) *Arr {
	out := New()
	for _, r := range rows {
		if v, ok := r.Lookup(key); ok {
			out.Push(v)
		}
	}
	return out
}
