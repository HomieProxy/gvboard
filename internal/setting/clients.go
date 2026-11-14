package setting

import (
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"
)

// ClientPlatforms 客户端下载支持的平台（决策 D67）。
var ClientPlatforms = []string{"windows", "macos", "ios", "android", "linux", "harmony"}

// Client 客户端下载项，存放在设置 client_downloads（JSON 数组）中。
type Client struct {
	Platform string `json:"platform"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	Icon     string `json:"icon"` // 为空时使用平台预置图标
}

const maxClients = 60

// ParseClients 解析 client_downloads，空字符串返回空列表。
func ParseClients(s string) ([]Client, error) {
	out := []Client{}
	if strings.TrimSpace(s) == "" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, fmt.Errorf("客户端列表不是有效的 JSON")
	}
	return out, nil
}

func validLink(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func validateClients(v any) error {
	s, _ := v.(string)
	list, err := ParseClients(s)
	if err != nil {
		return err
	}
	if len(list) > maxClients {
		return fmt.Errorf("客户端最多 %d 个", maxClients)
	}
	for i, c := range list {
		n := i + 1
		if !slices.Contains(ClientPlatforms, c.Platform) {
			return fmt.Errorf("第 %d 个客户端的平台不正确", n)
		}
		if l := utf8.RuneCountInString(strings.TrimSpace(c.Name)); l == 0 || l > 64 {
			return fmt.Errorf("第 %d 个客户端的名称不能为空且不超过 64 个字符", n)
		}
		if !validLink(c.URL) {
			return fmt.Errorf("第 %d 个客户端的下载地址必须是 http:// 或 https:// 开头的地址", n)
		}
		if c.Icon != "" && !validLink(c.Icon) {
			return fmt.Errorf("第 %d 个客户端的图标必须是 http:// 或 https:// 开头的地址", n)
		}
	}
	return nil
}
