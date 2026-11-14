// Package geoip 离线 IP 归属地查询（ip2region xdb，决策 D50），不访问外部网站。
package geoip

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/lionsoul2014/ip2region/binding/golang/service"
	"github.com/lionsoul2014/ip2region/binding/golang/xdb"

	"gvboard/internal/config"
)

// Locator 查询 IP 归属地，查不到时返回空串。
type Locator interface {
	Locate(ip string) string
	Close()
}

// None 未配置离线库时使用，总是返回空串。
type None struct{}

func (None) Locate(string) string { return "" }
func (None) Close()               {}

// DB ip2region 查询服务，并发安全。
type DB struct {
	svc *service.Ip2Region
}

// Open 按配置加载 v4 / v6 xdb 文件（索引常驻内存，数据按需读取）。两个都未配置时返回 None。
func Open(c config.GeoIPConfig) (Locator, error) {
	if c.IPv4 == "" && c.IPv6 == "" {
		return None{}, nil
	}
	var v4, v6 *service.Config
	var err error
	if c.IPv4 != "" {
		if err = xdb.VerifyFromFile(c.IPv4); err != nil {
			return nil, fmt.Errorf("ip2region IPv4 库无效: %w", err)
		}
		if v4, err = service.NewV4Config(service.VIndexCache, c.IPv4, 8); err != nil {
			return nil, fmt.Errorf("加载 ip2region IPv4 库失败: %w", err)
		}
	}
	if c.IPv6 != "" {
		if err = xdb.VerifyFromFile(c.IPv6); err != nil {
			return nil, fmt.Errorf("ip2region IPv6 库无效: %w", err)
		}
		if v6, err = service.NewV6Config(service.VIndexCache, c.IPv6, 8); err != nil {
			return nil, fmt.Errorf("加载 ip2region IPv6 库失败: %w", err)
		}
	}
	svc, err := service.NewIp2Region(v4, v6)
	if err != nil {
		return nil, fmt.Errorf("创建 ip2region 查询服务失败: %w", err)
	}
	return &DB{svc: svc}, nil
}

// Locate 返回 "国家 省份 城市 运营商" 形式的归属地，查不到或出错时返回空串。
func (d *DB) Locate(ip string) string {
	addr, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return ""
	}
	region, err := d.svc.Search(addr.Unmap().String())
	if err != nil {
		return ""
	}
	return Format(region)
}

// Close 释放查询服务。
func (d *DB) Close() { d.svc.Close() }

// Format 把 ip2region 的 "国家|省份|城市|运营商|国家代码"（旧版为 "国家|区域|省份|城市|运营商"）
// 整理为空格分隔的文字，去掉占位的 "0"、重复项和末尾的两位国家代码。
func Format(region string) string {
	parts := strings.Split(region, "|")
	if n := len(parts); n >= 5 && len(parts[n-1]) == 2 && strings.ToUpper(parts[n-1]) == parts[n-1] {
		parts = parts[:n-1]
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || p == "0" || (len(out) > 0 && out[len(out)-1] == p) {
			continue
		}
		out = append(out, p)
	}
	return strings.Join(out, " ")
}
