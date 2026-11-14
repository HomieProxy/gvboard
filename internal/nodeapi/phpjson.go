package nodeapi

import (
	"bytes"
	"crypto/md5"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"gvboard/internal/model"
)

// field / obj 按顺序输出的 JSON 对象，字段顺序与原项目一致，便于对照。
type field struct {
	k string
	v any
}

type obj []field

func (o obj) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, f := range o {
		if i > 0 {
			buf.WriteByte(',')
		}
		k, _ := json.Marshal(f.k)
		buf.Write(k)
		buf.WriteByte(':')
		v, err := encode(f.v)
		if err != nil {
			return nil, err
		}
		buf.Write(v)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// encode 编码为 JSON，不转义 HTML 字符。
func encode(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func etag(b []byte) string {
	h := sha1.Sum(b)
	return hex.EncodeToString(h[:])
}

// phpArray 按原项目 Laravel array 转换后再 json_encode 的结果输出 JSON 列：
// 空对象输出为 []，键为 0..n-1 的对象输出为数组。列为空时输出 null。
func phpArray(j model.JSON) any {
	if len(j) == 0 {
		return nil
	}
	var v any
	d := json.NewDecoder(bytes.NewReader(j))
	d.UseNumber()
	if err := d.Decode(&v); err != nil {
		return nil
	}
	return phpify(v)
}

func phpify(v any) any {
	switch x := v.(type) {
	case map[string]any:
		if len(x) == 0 {
			return []any{}
		}
		if list, ok := sequential(x); ok {
			return phpify(list)
		}
		for k, e := range x {
			x[k] = phpify(e)
		}
		return x
	case []any:
		for i := range x {
			x[i] = phpify(x[i])
		}
		return x
	case json.Number:
		return phpNumber(x)
	}
	return v
}

// phpNumber PHP 解码再编码后，整数值的小数（如 1.0）输出为整数。
func phpNumber(n json.Number) json.Number {
	s := n.String()
	if !strings.ContainsAny(s, ".eE") {
		return n
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return n
	}
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return json.Number(strconv.FormatInt(int64(f), 10))
	}
	return json.Number(strconv.FormatFloat(f, 'g', -1, 64))
}

func sequential(m map[string]any) ([]any, bool) {
	out := make([]any, len(m))
	for i := range out {
		v, ok := m[strconv.Itoa(i)]
		if !ok {
			return nil, false
		}
		out[i] = v
	}
	return out, true
}

// serverKey Shadowsocks 2022 的服务端密钥，由节点创建时间生成（与原项目 Helper::getServerKey 一致）。
func serverKey(createdAt int64, n int) string {
	h := md5.Sum([]byte(strconv.FormatInt(createdAt, 10)))
	return base64.StdEncoding.EncodeToString([]byte(hex.EncodeToString(h[:])[:n]))
}

// ssServerKey 按加密方式返回服务端密钥，非 2022 加密返回空串。
func ssServerKey(cipher string, createdAt int64) string {
	switch cipher {
	case "2022-blake3-aes-128-gcm":
		return serverKey(createdAt, 16)
	case "2022-blake3-aes-256-gcm":
		return serverKey(createdAt, 32)
	}
	return ""
}
