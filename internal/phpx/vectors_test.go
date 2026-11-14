package phpx

import (
	"math"
	"os"
	"testing"
	"time"
	_ "time/tzdata"
)

// testdata/vectors.json 由 php 8.2（symfony/yaml 4.4）生成，覆盖取整、浮点数转字符串、JSON 浮点数、
// 宽松比较、YAML 输出、PCRE 正则、http_build_query、URL 编码与日期格式化。

func loadVectors(t *testing.T) *Arr {
	t.Helper()
	b, err := os.ReadFile("testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	v, err := JSONDecode(b)
	if err != nil {
		t.Fatal(err)
	}
	return v.(*Arr)
}

func rows(t *testing.T, v *Arr, key string) []*Arr {
	t.Helper()
	list := v.Arr(key)
	if list.Len() == 0 {
		t.Fatalf("缺少向量 %s", key)
	}
	out := make([]*Arr, 0, list.Len())
	for _, x := range list.Values() {
		out = append(out, x.(*Arr))
	}
	return out
}

func toFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int64:
		return float64(x)
	}
	return math.NaN()
}

func TestVectorsNumbers(t *testing.T) {
	v := loadVectors(t)
	for _, r := range rows(t, v, "gb") {
		if got := FloatStr(GB(Int(r.Get(0)))); got != r.Str(1) {
			t.Errorf("GB(%v) = %q，期望 %q", r.Get(0), got, r.Str(1))
		}
	}
	for _, r := range rows(t, v, "traffic") {
		if got := TrafficConvert(Int(r.Get(0))); got != r.Str(1) {
			t.Errorf("TrafficConvert(%v) = %q，期望 %q", r.Get(0), got, r.Str(1))
		}
	}
	for _, r := range rows(t, v, "floatstr") {
		if got := FloatStr(toFloat(r.Get(0))); got != r.Str(1) {
			t.Errorf("FloatStr(%v) = %q，期望 %q", r.Get(0), got, r.Str(1))
		}
	}
	for _, r := range rows(t, v, "jsonfloat") {
		if got := string(JSONEncode(toFloat(r.Get(0)), 0)); got != r.Str(1) {
			t.Errorf("json(%v) = %q，期望 %q", r.Get(0), got, r.Str(1))
		}
	}
	for _, r := range rows(t, v, "round") {
		f, p, want := toFloat(r.Get(0)), int(Int(r.Get(1))), toFloat(r.Get(2))
		if got := Round(f, p); got != want {
			t.Errorf("Round(%v, %d) = %v，期望 %v", f, p, got, want)
		}
	}
}

func TestVectorsCompare(t *testing.T) {
	v := loadVectors(t)
	for _, r := range rows(t, v, "cmp") {
		a, b := r.Get(0), r.Get(1)
		if got := Compare(a, b); int64(got) != Int(r.Get(2)) {
			t.Errorf("%#v <=> %#v = %d，期望 %v", a, b, got, r.Get(2))
		}
		if got := LooseEq(a, b); got != (r.Get(3) == true) {
			t.Errorf("%#v == %#v = %v，期望 %v", a, b, got, r.Get(3))
		}
	}
}

func TestVectorsYAML(t *testing.T) {
	v := loadVectors(t)
	in := v.Get("yaml_input")
	want := v.Arr("yaml")
	cases := []struct {
		inline, indent int
		emptySeq       bool
	}{{2, 4, true}, {2, 4, false}, {4, 2, false}}
	for i, c := range cases {
		got := YAMLDump(in, c.inline, c.indent, c.emptySeq)
		if w := want.Str(i); got != w {
			t.Errorf("YAMLDump 第 %d 组不一致\n期望:\n%s\n实际:\n%s", i, w, got)
		}
	}
}

func TestVectorsPCRE(t *testing.T) {
	v := loadVectors(t)
	subjects := []string{"香港 01", "HK-1", "tuic-1", "abc", "a\nb", "123", "aa", "日本 JP", ""}
	for _, r := range rows(t, v, "pcre") {
		p := r.Str(0)
		re := CompileRegex(p)
		if valid := r.Get(1) == true; (re != nil) != valid {
			t.Errorf("CompileRegex(%q) 有效性 = %v，期望 %v", p, re != nil, valid)
			continue
		}
		if re == nil {
			continue
		}
		for i, s := range subjects {
			if want := Int(r.Get(2, i)) == 1; re.Match(s) != want {
				t.Errorf("preg_match(%q, %q) = %v，期望 %v", p, s, !want, want)
			}
		}
	}
}

func TestVectorsEncoding(t *testing.T) {
	v := loadVectors(t)
	for _, r := range rows(t, v, "query") {
		q := r.Arr(0)
		if got := HTTPBuildQuery(q, true); got != r.Str(1) {
			t.Errorf("http_build_query RFC3986 = %q，期望 %q", got, r.Str(1))
		}
		if got := HTTPBuildQuery(q, false); got != r.Str(2) {
			t.Errorf("http_build_query = %q，期望 %q", got, r.Str(2))
		}
	}
	for _, r := range rows(t, v, "url") {
		if got := RawURLEncode(r.Str(0)); got != r.Str(1) {
			t.Errorf("rawurlencode(%q) = %q，期望 %q", r.Str(0), got, r.Str(1))
		}
		if got := URLEncode(r.Str(0)); got != r.Str(2) {
			t.Errorf("urlencode(%q) = %q，期望 %q", r.Str(0), got, r.Str(2))
		}
	}
}

func TestVectorsDate(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Local
	time.Local = loc
	defer func() { time.Local = old }()
	v := loadVectors(t)
	for _, r := range rows(t, v, "date") {
		ts := Int(r.Get(0))
		if got := Date("Y-m-d H:i:s", ts); got != r.Str(1) {
			t.Errorf("date(%d) = %q，期望 %q", ts, got, r.Str(1))
		}
		if got := Date("Y-m-d", ts); got != r.Str(2) {
			t.Errorf("date(%d) = %q，期望 %q", ts, got, r.Str(2))
		}
	}
}
