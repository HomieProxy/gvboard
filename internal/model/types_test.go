package model

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestIntListScan(t *testing.T) {
	cases := []struct {
		src  any
		want IntList
	}{
		{`["1","2","3"]`, IntList{1, 2, 3}}, // 原项目管理端表单提交的字符串格式
		{`[1,2]`, IntList{1, 2}},
		{[]byte(`["5",6]`), IntList{5, 6}},
		{nil, nil},
		{"", nil},
		{"null", nil},
		{"[]", IntList{}},
		{`"[7,8]"`, IntList{7, 8}}, // 原项目礼品卡 used_user_ids 被重复编码的格式
		{`"[]"`, IntList{}},
	}
	for _, c := range cases {
		var l IntList
		if err := l.Scan(c.src); err != nil {
			t.Fatalf("Scan(%v) 出错: %v", c.src, err)
		}
		if !reflect.DeepEqual(l, c.want) {
			t.Errorf("Scan(%v) = %v，期望 %v", c.src, l, c.want)
		}
	}
	var l IntList
	if err := l.Scan(`["abc"]`); err == nil {
		t.Error("非数字元素应报错")
	}
}

func TestIntListValue(t *testing.T) {
	v, _ := IntList{1, 2}.Value()
	if v != "[1,2]" {
		t.Errorf("Value = %v", v)
	}
	v, _ = IntList(nil).Value()
	if v != nil {
		t.Errorf("nil 应写入 NULL，实际 %v", v)
	}
	if !(IntList{3, 4}).Contains(4) || (IntList{3}).Contains(1) {
		t.Error("Contains 结果错误")
	}
}

func TestStrListScan(t *testing.T) {
	var l StrList
	if err := l.Scan(`["a", 1, 2.5, null]`); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(l, StrList{"a", "1", "2.5"}) {
		t.Errorf("Scan 结果 %v", l)
	}
	if err := l.Scan(`"[\"x\"]"`); err != nil || !reflect.DeepEqual(l, StrList{"x"}) {
		t.Errorf("重复编码的字符串数组解析错误: %v %v", l, err)
	}
}

func TestJSONType(t *testing.T) {
	var j JSON
	if err := j.Scan(`{"a":1}`); err != nil {
		t.Fatal(err)
	}
	var out struct{ A int }
	if err := j.Decode(&out); err != nil || out.A != 1 {
		t.Errorf("Decode 失败: %v %+v", err, out)
	}
	if err := j.Scan("not json"); err == nil {
		t.Error("非法 JSON 应报错")
	}

	// 嵌入到结构体中序列化时应输出原始 JSON 而不是 base64
	type wrap struct {
		J JSON `json:"j"`
		N JSON `json:"n"`
	}
	b, _ := json.Marshal(wrap{J: JSON(`{"x":[1,2]}`)})
	if string(b) != `{"j":{"x":[1,2]},"n":null}` {
		t.Errorf("序列化结果 %s", b)
	}
}
