package payment

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const maxResponse = 2 << 20

// call 发送请求，返回 HTTP 状态码与响应体。网络错误原样返回。
func call(e *Env, method, url string, body []byte, header map[string]string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(e.Ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := e.HTTP.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	return resp.StatusCode, b, err
}

// decodeObject 解析 JSON 对象（数字保留为 json.Number），不是对象时返回 nil。
func decodeObject(b []byte) map[string]any {
	var m map[string]any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if dec.Decode(&m) != nil {
		return nil
	}
	return m
}

// dig 按路径取嵌套对象中的值。
func dig(m map[string]any, path ...string) any {
	var cur any = m
	for _, k := range path {
		o, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = o[k]
	}
	return cur
}

// digStr 按路径取值并转为字符串。
func digStr(m map[string]any, path ...string) string { return phpStr(dig(m, path...)) }

// phpEmpty PHP empty()：nil、""、"0"、0、false、空数组均为空。
func phpEmpty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == "" || x == "0"
	case bool:
		return !x
	case json.Number:
		f, err := x.Float64()
		return err == nil && f == 0
	case float64:
		return x == 0
	case int:
		return x == 0
	case int64:
		return x == 0
	case map[string]any:
		return len(x) == 0
	case []any:
		return len(x) == 0
	}
	return false
}

// phpNum 把 PHP 数值或数字字符串转为浮点（PHP 弱类型比较、(int) 转换使用）。
func phpNum(v any) (float64, bool) {
	s := strings.TrimSpace(phpStr(v))
	f, err := strconv.ParseFloat(s, 64)
	return f, err == nil
}

// jsonFloat PHP json_encode 浮点数（serialize_precision=-1，未设置 JSON_PRESERVE_ZERO_FRACTION，10.0 输出为 10）。
type jsonFloat float64

func (f jsonFloat) MarshalJSON() ([]byte, error) {
	return []byte(strconv.FormatFloat(float64(f), 'f', -1, 64)), nil
}

// jsonAmount 把 phpDiv100 的结果编码为 PHP json_encode 的数字形式。
func jsonAmount(v any) any {
	if f, ok := v.(float64); ok {
		return jsonFloat(f)
	}
	return v
}

// round2 PHP round($v, 2)。
func round2(amount int64) float64 {
	f, _ := strconv.ParseFloat(strconv.FormatFloat(float64(amount)/100, 'f', 2, 64), 64)
	return f
}

func md5hex(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

// hashEquals 常量时间比较（PHP hash_equals）。
func hashEquals(known, user string) bool {
	return hmac.Equal([]byte(known), []byte(user))
}

// marshalJSON 编码 JSON，不转义斜杠、HTML 字符与非 ASCII 字符（JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE）。
func marshalJSON(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return bytes.TrimRight(b.Bytes(), "\n")
}

func httpStatusText(status int) string { return fmt.Sprintf("HTTP %d", status) }
