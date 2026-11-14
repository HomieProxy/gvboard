package node

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"math"
	"strings"

	"gvboard/internal/httpx"
	"gvboard/internal/model"
	"gvboard/internal/util"
)

var (
	errInvalid        = httpx.Validation("参数有误")
	errTypeInvalid    = httpx.NotFoundErr("节点类型不存在")
	errServerNotExist = httpx.NotFoundErr("服务器不存在")
	errNodeNotExist   = httpx.NotFoundErr("节点ID不存在")
	errSaveFailed     = httpx.BadRequest("保存失败")
	errCreateFailed   = httpx.BadRequest("创建失败")
)

func invalid(msg string) *httpx.Error { return httpx.Validation(msg) }

// commonColumns 各类节点共有、可由管理员修改的列。
var commonColumns = []string{"group_id", "route_id", "name", "parent_id", "host", "port", "server_port", "tags", "rate", "show", "sort"}

// spec 每类节点的可写列、校验与补全规则（对应原项目各控制器 save 方法）。
type spec struct {
	columns   []string
	validate  func(p *params) error
	normalize func(s *Service, p *params, old Server) error
}

var ssCiphers = []string{"aes-128-gcm", "aes-192-gcm", "aes-256-gcm", "chacha20-ietf-poly1305", "2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm"}

var specs = map[string]spec{
	model.ServerTypeShadowsocks: {
		columns: []string{"cipher", "obfs", "obfs_settings"},
		validate: func(p *params) error {
			switch {
			case !p.required("cipher"):
				return invalid("加密方式不能为空")
			case !p.in("cipher", ssCiphers...):
				return invalid("加密方式格式不正确")
			case !p.in("obfs", "http"):
				return invalid("混淆格式不正确")
			case !p.isArray("obfs_settings"):
				return invalid("混淆设置格式不正确")
			}
			return nil
		},
	},
	model.ServerTypeVmess: {
		columns: []string{"tls", "network", "networkSettings", "ruleSettings", "tlsSettings", "dnsSettings"},
		validate: func(p *params) error {
			switch {
			case !p.required("tls"):
				return invalid("TLS不能为空")
			case !p.isInteger("tls"):
				return invalid("TLS格式不正确")
			case !p.required("network"):
				return invalid("传输协议不能为空")
			case !p.in("network", "tcp", "kcp", "ws", "http", "domainsocket", "quic", "grpc", "httpupgrade", "xhttp"):
				return invalid("传输协议格式不正确")
			case !p.isArray("networkSettings"):
				return invalid("传输协议配置有误")
			case !p.isArray("ruleSettings"):
				return invalid("规则配置有误")
			case !p.isArray("tlsSettings"):
				return invalid("tls配置有误")
			case !p.isArray("dnsSettings"):
				return invalid("dns配置有误")
			}
			if ns, ok := p.m["networkSettings"].(map[string]any); ok {
				switch sec := ns["security"].(type) {
				case nil:
				case string:
					if sec != "auto" && sec != "aes-128-gcm" && sec != "chacha20-poly1305" && sec != "none" {
						return invalid("vmess加密类型只能是: auto, aes-128-gcm, chacha20-poly1305, none")
					}
				default:
					return invalid("vmess加密类型只能是: auto, aes-128-gcm, chacha20-poly1305, none")
				}
			}
			return nil
		},
	},
	model.ServerTypeTrojan: {
		columns: []string{"network", "network_settings", "allow_insecure", "server_name"},
		validate: func(p *params) error {
			switch {
			case !p.required("network"):
				return invalid("传输协议不能为空")
			case !p.isArray("network_settings"):
				return invalid("传输协议配置有误")
			case !p.in("allow_insecure", "0", "1"):
				return invalid("允许不安全格式不正确")
			}
			return nil
		},
	},
	model.ServerTypeTuic: {
		columns: []string{"server_name", "insecure", "disable_sni", "udp_relay_mode", "zero_rtt_handshake", "congestion_control"},
		validate: func(p *params) error {
			return requiredFlags(p, "insecure", "disable_sni", "zero_rtt_handshake")
		},
	},
	model.ServerTypeHysteria: {
		columns: []string{"version", "up_mbps", "down_mbps", "obfs", "obfs_password", "server_name", "insecure"},
		validate: func(p *params) error {
			switch {
			case !p.required("version") || !p.in("version", "1", "2"):
				return invalid("版本格式不正确")
			case !p.isNumeric("up_mbps") || !p.isNumeric("down_mbps"):
				return invalid("带宽格式不正确")
			}
			return requiredFlags(p, "insecure")
		},
		normalize: func(_ *Service, p *params, _ Server) error {
			bandwidth(p)
			obfsPassword(p)
			return nil
		},
	},
	model.ServerTypeVless: {
		columns: []string{"tls", "tls_settings", "flow", "network", "network_settings", "encryption", "encryption_settings"},
		validate: func(p *params) error {
			switch {
			case !p.required("tls") || !p.in("tls", "0", "1", "2"):
				return invalid("TLS格式不正确")
			case !p.isArray("tls_settings"):
				return invalid("tls配置有误")
			case !p.in("flow", "xtls-rprx-vision"):
				return invalid("流控格式不正确")
			case !p.required("network"):
				return invalid("传输协议不能为空")
			case !p.isArray("network_settings"):
				return invalid("传输协议配置有误")
			case !p.isArray("encryption_settings"):
				return invalid("加密配置有误")
			}
			return nil
		},
		normalize: func(_ *Service, p *params, _ Server) error {
			if phpInt(p.m["tls"]) == 2 {
				if err := reality(p); err != nil {
					return err
				}
			}
			if p.str("network") != "tcp" {
				p.set("flow", nil)
			}
			if p.str("network") == "xhttp" && p.isset("network_settings") {
				xhttpExtra(p, false)
			}
			if p.str("encryption") == "mlkem768x25519plus" {
				return mlkem(p, false)
			}
			return nil
		},
	},
	model.ServerTypeAnytls: {
		columns: []string{"server_name", "insecure", "padding_scheme"},
		validate: func(p *params) error {
			return requiredFlags(p, "insecure")
		},
		normalize: func(_ *Service, p *params, _ Server) error {
			paddingScheme(p)
			return nil
		},
	},
	model.ServerTypeV2node: {
		columns: []string{"listen_ip", "protocol", "tls", "tls_settings", "flow", "network", "network_settings",
			"trusted_x_forwarded_for", "encryption", "encryption_settings", "disable_sni", "udp_relay_mode",
			"zero_rtt_handshake", "congestion_control", "cipher", "up_mbps", "down_mbps", "obfs", "obfs_password", "padding_scheme"},
		validate: func(p *params) error {
			switch {
			case !p.required("protocol") || !p.in("protocol", "shadowsocks", "vmess", "vless", "trojan", "tuic", "hysteria2", "anytls"):
				return invalid("协议格式不正确")
			case !p.required("tls") || !p.in("tls", "0", "1", "2"):
				return invalid("TLS格式不正确")
			case !p.isArray("tls_settings"):
				return invalid("tls配置有误")
			case !p.in("flow", "xtls-rprx-vision"):
				return invalid("流控格式不正确")
			case !p.required("network") || !p.in("network", "tcp", "ws", "grpc", "http", "httpupgrade", "xhttp"):
				return invalid("传输协议格式不正确")
			case !p.isArray("network_settings"):
				return invalid("传输协议配置有误")
			case !p.isArray("trusted_x_forwarded_for"):
				return invalid("信任的转发头格式不正确")
			case !p.isArray("encryption_settings"):
				return invalid("加密配置有误")
			case !p.isNumeric("up_mbps") || !p.isNumeric("down_mbps"):
				return invalid("带宽格式不正确")
			}
			return requiredFlags(p, "disable_sni", "zero_rtt_handshake")
		},
		normalize: normalizeV2node,
	},
}

func requiredFlags(p *params, keys ...string) error {
	for _, k := range keys {
		if !p.required(k) || !p.in(k, "0", "1") {
			return invalid(k + " 格式不正确")
		}
	}
	return nil
}

// validateCommon 共有字段校验（对应原项目各类节点的公共规则）。
func validateCommon(p *params) error {
	switch {
	case !p.required("name"):
		return invalid("节点名称不能为空")
	case !p.required("group_id"):
		return invalid("权限组不能为空")
	case !isIntArray(p.m["group_id"]):
		return invalid("权限组格式不正确")
	case p.m["route_id"] != nil && !isIntArray(p.m["route_id"]):
		return invalid("路由组格式不正确")
	case !p.isInteger("parent_id"):
		return invalid("父节点格式不正确")
	case !p.required("host"):
		return invalid("节点地址不能为空")
	case !p.required("port"):
		return invalid("连接端口不能为空")
	case !p.required("server_port"):
		return invalid("后端服务端口不能为空")
	case !p.isInteger("server_port"):
		return invalid("后端服务端口格式不正确")
	case !p.isArray("tags"):
		return invalid("标签格式不正确")
	case !p.required("rate"):
		return invalid("倍率不能为空")
	case !p.isNumeric("rate"):
		return invalid("倍率格式不正确")
	case !p.in("show", "0", "1"):
		return invalid("显示状态格式不正确")
	case !p.isInteger("sort"):
		return invalid("排序格式不正确")
	}
	return nil
}

func isIntArray(v any) bool {
	var items []any
	switch x := v.(type) {
	case []any:
		items = x
	case map[string]any:
		for _, e := range x {
			items = append(items, e)
		}
	default:
		return false
	}
	for _, e := range items {
		f, ok := toFloat(e)
		if !ok || f != math.Trunc(f) {
			return false
		}
	}
	return true
}

// intColumns 整数列：请求中的数字字符串、小数按 MySQL 写入整数列的方式转换。
var intColumns = map[string]bool{
	"parent_id": true, "server_port": true, "show": true, "sort": true, "tls": true, "allow_insecure": true,
	"insecure": true, "disable_sni": true, "zero_rtt_handshake": true, "version": true, "up_mbps": true, "down_mbps": true,
}

// stringColumns 字符串列：请求中的数字转为字符串。
var stringColumns = map[string]bool{"port": true, "rate": true, "name": true, "host": true, "listen_ip": true}

// decodeInto 把参数转换为模型。数组 / 对象列原样保存为 JSON。
func decodeInto(p *params, cols []string, s Server) error {
	out := make(map[string]any, len(cols))
	for _, k := range cols {
		v, ok := p.m[k]
		if !ok || v == nil {
			continue
		}
		switch {
		case intColumns[k]:
			f, _ := toFloat(v)
			if b, ok := v.(bool); ok && b {
				f = 1
			}
			v = int64(math.Round(f))
		case stringColumns[k]:
			v = phpString(v)
		case k == "group_id" || k == "route_id":
			ids := []int64{}
			walk(v, func(e any) {
				f, _ := toFloat(e)
				ids = append(ids, int64(f))
			})
			v = ids
		case k == "tags":
			tags := []string{}
			walk(v, func(e any) {
				if e != nil {
					tags = append(tags, phpString(e))
				}
			})
			v = tags
		}
		out[k] = v
	}
	b, err := json.Marshal(out)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, s)
}

func walk(v any, fn func(any)) {
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			fn(e)
		}
	case map[string]any:
		for _, e := range x {
			fn(e)
		}
	}
}

// Save 新建（id 为 0）或修改节点。body 为 JSON 对象，字段名与数据库列名一致。
// 修改时只写入请求中出现的列与服务端补全的列（与原项目 validated() 后 update 一致）。
func (s *Service) Save(ctx context.Context, typ string, id int64, body []byte) error {
	sp, ok := specs[typ]
	if !ok {
		return errTypeInvalid
	}
	p, ok := parseParams(body)
	if !ok {
		return errInvalid
	}
	if err := validateCommon(p); err != nil {
		return err
	}
	if err := sp.validate(p); err != nil {
		return err
	}
	var old Server
	if id > 0 {
		var err error
		if old, err = Find(ctx, s.DB, typ, id); err != nil {
			return err
		}
		if old == nil {
			return errServerNotExist
		}
	}
	if sp.normalize != nil {
		if err := sp.normalize(s, p, old); err != nil {
			return err
		}
	}
	cols := append(append([]string{}, commonColumns...), sp.columns...)
	srv := New(typ)
	if err := decodeInto(p, cols, srv); err != nil {
		return errInvalid
	}
	now := s.now().Unix()
	db := s.db(ctx)
	if old == nil {
		if v, ok := srv.(*model.ServerV2node); ok && v.ListenIP == "" {
			v.ListenIP = "0.0.0.0"
		}
		srv.Common().CreatedAt, srv.Common().UpdatedAt = now, now
		if err := db.Select(append(cols, "created_at", "updated_at")).Create(srv).Error; err != nil {
			s.log().Warn("创建节点失败", errField(err))
			return errCreateFailed
		}
		return nil
	}
	write := []string{"updated_at"}
	for _, k := range cols {
		if p.has(k) {
			write = append(write, k)
		}
	}
	srv.Common().ID, srv.Common().UpdatedAt = id, now
	if v, ok := srv.(*model.ServerV2node); ok && p.has("listen_ip") && v.ListenIP == "" {
		v.ListenIP = "0.0.0.0"
	}
	if err := db.Model(srv).Select(write).Updates(srv).Error; err != nil {
		s.log().Warn("保存节点失败", errField(err))
		return errSaveFailed
	}
	return nil
}

// ---------- 补全规则 ----------

// reality 开启 REALITY 时补全密钥、short_id 与目标端口。只提供私钥时由私钥推导公钥（原项目会另生成一对，导致公私钥不匹配）。
func reality(p *params) error {
	ts := p.obj("tls_settings")
	priv, pub, err := x25519Pair()
	if err != nil {
		return err
	}
	if ts["private_key"] == nil {
		ts["private_key"] = priv
	} else if derived, ok := x25519Public(phpString(ts["private_key"])); ok {
		pub = derived
	}
	if ts["public_key"] == nil {
		ts["public_key"] = pub
	}
	if ts["short_id"] == nil {
		sum := sha1.Sum([]byte(phpString(ts["private_key"])))
		ts["short_id"] = hex.EncodeToString(sum[:])[:8]
	}
	if ts["server_port"] == nil {
		ts["server_port"] = "443"
	}
	p.set("tls_settings", ts)
	return nil
}

// mlkem VLESS 加密 mlkem768x25519plus 的参数补全。v2node 额外补全 mode、rtt、ticket 默认值。
func mlkem(p *params, v2node bool) error {
	es := p.obj("encryption_settings")
	priv, pub, err := x25519Pair()
	if err != nil {
		return err
	}
	if v2node && es["mode"] == nil {
		es["mode"] = "native"
	}
	if es["rtt"] != nil {
		if phpString(es["rtt"]) == "1rtt" {
			es["ticket"] = "0s"
		}
	} else if v2node {
		es["rtt"], es["ticket"] = "0rtt", "600s"
	}
	if es["private_key"] == nil {
		es["private_key"] = priv
	} else if derived, ok := x25519Public(phpString(es["private_key"])); ok {
		pub = derived
	}
	if es["password"] == nil {
		es["password"] = pub
	}
	p.set("encryption_settings", es)
	return nil
}

// xhttpExtra 把 xhttp 的 extra 中的布尔与整数字段转换为正确类型。
func xhttpExtra(p *params, v2node bool) {
	ns := p.obj("network_settings")
	extra, ok := ns["extra"].(map[string]any)
	if !ok {
		return
	}
	bools := []string{"noGRPCHeader", "noSSEHeader"}
	if v2node {
		bools = append(bools, "xPaddingObfsMode")
	}
	for _, k := range bools {
		if extra[k] != nil {
			extra[k] = phpBool(extra[k])
		}
	}
	if extra["scMaxBufferedPosts"] != nil {
		extra["scMaxBufferedPosts"] = phpInt(extra["scMaxBufferedPosts"])
	}
	if xmux, ok := extra["xmux"].(map[string]any); ok && xmux["hKeepAlivePeriod"] != nil {
		xmux["hKeepAlivePeriod"] = phpInt(xmux["hKeepAlivePeriod"])
	}
	if ds, ok := extra["downloadSettings"].(map[string]any); ok && ds["port"] != nil {
		ds["port"] = phpInt(ds["port"])
	}
	p.set("network_settings", ns)
}

// paddingScheme AnyTLS 填充方案以 JSON 文本提交，解析后保存；不是合法 JSON 时保存为空。
func paddingScheme(p *params) {
	s, ok := p.m["padding_scheme"].(string)
	if !ok {
		return
	}
	var v any
	if json.Unmarshal([]byte(s), &v) != nil {
		v = nil
	}
	p.set("padding_scheme", v)
}

func bandwidth(p *params) {
	for _, k := range []string{"up_mbps", "down_mbps"} {
		if !p.isset(k) {
			p.set(k, int64(0))
		}
	}
}

// obfsPassword 开启混淆且未填写密码时随机生成 16 位（决策 D34），未开启混淆时清空。
func obfsPassword(p *params) {
	if !p.isset("obfs") {
		p.set("obfs_password", nil)
		return
	}
	if !p.isset("obfs_password") {
		p.set("obfs_password", util.RandomChar(16, false))
	}
}

func normalizeV2node(s *Service, p *params, old Server) error {
	protocol := p.str("protocol")
	if protocol == "anytls" && phpInt(p.m["tls"]) == 0 {
		p.set("tls", int64(1))
	}
	if protocol == "hysteria2" || protocol == "trojan" || protocol == "tuic" {
		p.set("tls", int64(1))
	}
	tls := phpInt(p.m["tls"])
	if tls == 2 {
		if err := reality(p); err != nil {
			return err
		}
	}
	if ts, ok := p.m["tls_settings"].(map[string]any); ok && !phpEmpty(ts["ech"]) && ts["ech"] == "custom" {
		if phpEmpty(ts["ech_server_name"]) {
			ts["ech"] = ""
		} else if phpEmpty(ts["ech_key"]) || phpEmpty(ts["ech_config"]) {
			key, cfg, err := echKeyPair(phpString(ts["ech_server_name"]))
			if err != nil {
				return err
			}
			if phpEmpty(ts["ech_key"]) {
				ts["ech_key"] = key
			}
			if phpEmpty(ts["ech_config"]) {
				ts["ech_config"] = cfg
			}
		}
		p.set("tls_settings", ts)
	}
	if err := remoteCert(s, p, old, tls); err != nil {
		return err
	}
	if ns, ok := p.m["network_settings"].(map[string]any); ok && ns["acceptProxyProtocol"] != nil {
		ns["acceptProxyProtocol"] = phpBool(ns["acceptProxyProtocol"])
		p.set("network_settings", ns)
	}
	network := p.str("network")
	if network != "tcp" && p.isset("encryption") && p.str("encryption") != "mlkem768x25519plus" {
		p.set("flow", nil)
	}
	if network == "xhttp" && p.isset("network_settings") {
		xhttpExtra(p, true)
	}
	if p.str("encryption") == "mlkem768x25519plus" {
		if err := mlkem(p, true); err != nil {
			return err
		}
	}
	paddingScheme(p)
	bandwidth(p)
	obfsPassword(p)
	if protocol == "shadowsocks" && !p.isset("cipher") {
		p.set("cipher", "aes-128-gcm")
	}
	return nil
}

// remoteCert TLS 证书模式为 remote 且未提供证书时：沿用节点已保存的证书，没有则生成自签名证书与指纹。
func remoteCert(s *Service, p *params, old Server, tls int64) error {
	ts, ok := p.m["tls_settings"].(map[string]any)
	if tls != 1 || !ok || phpEmpty(ts["cert_mode"]) || ts["cert_mode"] != "remote" {
		return nil
	}
	if phpEmpty(ts["tls_cert"]) || phpEmpty(ts["tls_key"]) {
		if v, ok := old.(*model.ServerV2node); ok {
			var saved map[string]any
			_ = v.TLSSettings.Decode(&saved)
			if !phpEmpty(saved["tls_cert"]) && !phpEmpty(saved["tls_key"]) {
				ts["tls_cert"], ts["tls_key"] = saved["tls_cert"], saved["tls_key"]
				if !phpEmpty(saved["pinned_peer_cert_sha256"]) {
					ts["pinned_peer_cert_sha256"] = saved["pinned_peer_cert_sha256"]
				}
			}
		}
	}
	if phpEmpty(ts["tls_cert"]) || phpEmpty(ts["tls_key"]) {
		cn := "example.com"
		if ts["server_name"] != nil {
			cn = phpString(ts["server_name"])
		}
		cert, key, pin, err := selfSignedCert(cn, s.now())
		if err != nil {
			return errCreateFailed
		}
		ts["tls_cert"], ts["tls_key"], ts["pinned_peer_cert_sha256"] = cert, key, pin
	}
	p.set("tls_settings", ts)
	return nil
}

// trimType 路由中的节点类型参数。
func trimType(t string) string { return strings.ToLower(strings.TrimSpace(t)) }
