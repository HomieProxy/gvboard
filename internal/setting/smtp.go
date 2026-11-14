package setting

import (
	"encoding/json"
	"fmt"
	"net/mail"
	"strings"
	"unicode/utf8"
)

// MaxSMTPServers 最多可配置的 SMTP 服务器数量。
const MaxSMTPServers = 10

// SMTPServer 一个 SMTP 服务器（设置项 email_smtp_servers 的一项，决策 D72）。
type SMTPServer struct {
	Name        string `json:"name"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Encryption  string `json:"encryption"` // ssl、tls 或空
	Username    string `json:"username"`
	Password    string `json:"password"`
	FromAddress string `json:"from_address"`
	Enable      bool   `json:"enable"`
}

// ParseSMTPServers 解析 email_smtp_servers，空字符串返回空列表。
func ParseSMTPServers(raw string) ([]SMTPServer, error) {
	out := []SMTPServer{}
	if strings.TrimSpace(raw) == "" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("SMTP 服务器列表格式不正确")
	}
	return out, nil
}

func validateSMTPServers(v any) error {
	s, _ := v.(string)
	list, err := ParseSMTPServers(s)
	if err != nil {
		return err
	}
	if len(list) > MaxSMTPServers {
		return fmt.Errorf("最多配置 %d 个 SMTP 服务器", MaxSMTPServers)
	}
	for i, srv := range list {
		n := i + 1
		if utf8.RuneCountInString(srv.Name) > 32 {
			return fmt.Errorf("第 %d 个 SMTP 服务器名称不能超过 32 个字符", n)
		}
		if strings.TrimSpace(srv.Host) == "" || len(srv.Host) > 255 {
			return fmt.Errorf("第 %d 个 SMTP 服务器未填写服务器地址", n)
		}
		if srv.Port < 0 || srv.Port > 65535 {
			return fmt.Errorf("第 %d 个 SMTP 服务器端口取值范围为 0～65535", n)
		}
		if e := strings.ToLower(strings.TrimSpace(srv.Encryption)); e != "" && e != "ssl" && e != "tls" {
			return fmt.Errorf("第 %d 个 SMTP 服务器加密方式只能是 ssl、tls 或留空", n)
		}
		if _, err := mail.ParseAddress(strings.TrimSpace(srv.FromAddress)); err != nil {
			return fmt.Errorf("第 %d 个 SMTP 服务器发件地址无效", n)
		}
		if len(srv.Username) > 255 || len(srv.Password) > 1024 {
			return fmt.Errorf("第 %d 个 SMTP 服务器账号或密码过长", n)
		}
	}
	return nil
}
