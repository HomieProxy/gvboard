// Package migrate 负责数据库结构的创建、增量升级以及从原项目数据的迁移。
package migrate

import (
	"fmt"
	"regexp"
	"strings"

	"gvboard/internal/config"
)

// Column 列定义。Type 使用 MySQL 风格的抽象类型，生成 DDL 时按方言转换。
type Column struct {
	Name    string
	Type    string // int / bigint / tinyint / varchar(n) / char(n) / text / longtext / decimal(p,s)
	Null    bool
	Default string // SQL 字面量，如 '0'；为空表示无默认值
	AutoPK  bool   // 自增主键
	PGType  string // PostgreSQL 下覆盖的类型（可选）
}

// Index 索引定义。
type Index struct {
	Name   string
	Cols   []string
	Unique bool
	// Ensure 为 true 时，即使表已存在也会补建该索引；原项目已有表的原始索引不设置此项，避免与生产库中同义索引重复。
	Ensure bool
}

// Table 表定义。
type Table struct {
	Name    string
	Columns []Column
	Indexes []Index
}

func col(name, typ string) Column       { return Column{Name: name, Type: typ} }
func null(name, typ string) Column      { return Column{Name: name, Type: typ, Null: true} }
func def(name, typ, dflt string) Column { return Column{Name: name, Type: typ, Default: dflt} }
func nullDef(name, typ, dflt string) Column {
	return Column{Name: name, Type: typ, Null: true, Default: dflt}
}
func pk() Column                               { return Column{Name: "id", Type: "int", AutoPK: true} }
func timestamps() []Column                     { return []Column{col("created_at", "int"), col("updated_at", "int")} }
func idx(name string, cols ...string) Index    { return Index{Name: name, Cols: cols} }
func unique(name string, cols ...string) Index { return Index{Name: name, Cols: cols, Unique: true} }

func table(name string, cols []Column, indexes ...Index) Table {
	all := append([]Column{pk()}, cols...)
	all = append(all, timestamps()...)
	return Table{Name: name, Columns: all, Indexes: indexes}
}

// serverCommon 各类节点表的公共列（顺序与原表一致的部分）。
func serverCommon(portType string) []Column {
	return []Column{
		col("group_id", "varchar(255)"),
		null("route_id", "varchar(255)"),
		col("name", "varchar(255)"),
		null("parent_id", "int"),
		col("host", "varchar(255)"),
		col("port", portType),
		col("server_port", "int"),
		null("tags", "varchar(255)"),
		col("rate", "varchar(11)"),
		def("show", "tinyint", "0"),
		null("sort", "int"),
	}
}

func cols(groups ...[]Column) []Column {
	var out []Column
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

// Schema 全部表定义：原项目表（与 install.sql 一致）+ Gvboard 新增表与字段。
func Schema() []Table {
	return []Table{
		table("v2_commission_log", []Column{
			col("invite_user_id", "int"),
			col("user_id", "int"),
			col("trade_no", "char(36)"),
			col("order_amount", "int"),
			col("get_amount", "int"),
		}),
		table("v2_coupon", []Column{
			col("code", "varchar(255)"),
			col("name", "varchar(255)"),
			col("type", "tinyint"),
			col("value", "int"),
			def("show", "tinyint", "0"),
			null("limit_use", "int"),
			null("limit_use_with_user", "int"),
			null("limit_plan_ids", "varchar(255)"),
			null("limit_period", "varchar(255)"),
			col("started_at", "int"),
			col("ended_at", "int"),
		}),
		table("v2_giftcard", []Column{
			col("code", "varchar(255)"),
			col("name", "varchar(255)"),
			col("type", "tinyint"),
			null("value", "int"),
			null("plan_id", "int"),
			null("limit_use", "int"),
			null("used_user_ids", "text"), // 原表为 utf8 下的 varchar(16384)，utf8mb4 超出行长限制
			col("started_at", "int"),
			col("ended_at", "int"),
		}),
		table("v2_invite_code", []Column{
			col("user_id", "int"),
			col("code", "char(32)"),
			def("status", "tinyint", "0"),
			def("pv", "int", "0"),
		}),
		table("v2_knowledge", []Column{
			col("language", "char(5)"),
			col("category", "varchar(255)"),
			col("title", "varchar(255)"),
			col("body", "text"),
			null("sort", "int"),
			def("show", "tinyint", "0"),
		}),
		table("v2_log", []Column{
			col("title", "text"),
			null("level", "varchar(11)"),
			null("host", "varchar(255)"),
			col("uri", "varchar(255)"),
			col("method", "varchar(11)"),
			null("data", "text"),
			null("ip", "varchar(128)"),
			null("context", "text"),
		}),
		table("v2_mail_log", []Column{
			col("email", "varchar(64)"),
			col("subject", "varchar(255)"),
			col("template_name", "varchar(255)"),
			null("error", "text"),
		}),
		table("v2_notice", []Column{
			col("title", "varchar(255)"),
			col("content", "text"),
			def("show", "tinyint", "0"),
			null("img_url", "varchar(255)"),
			null("tags", "varchar(255)"),
		}),
		table("v2_order", []Column{
			null("invite_user_id", "int"),
			col("user_id", "int"),
			col("plan_id", "int"),
			null("coupon_id", "int"),
			null("payment_id", "int"),
			col("type", "int"),
			col("period", "varchar(255)"),
			col("trade_no", "varchar(36)"),
			null("callback_no", "varchar(255)"),
			col("total_amount", "int"),
			null("handling_amount", "int"),
			null("discount_amount", "int"),
			null("surplus_amount", "int"),
			null("refund_amount", "int"),
			null("balance_amount", "int"),
			null("surplus_order_ids", "text"),
			def("status", "tinyint", "0"),
			def("commission_status", "tinyint", "0"),
			def("commission_balance", "int", "0"),
			null("actual_commission_balance", "int"),
			null("paid_at", "int"),
			null("subscription_id", "int"),   // Gvboard 新增
			def("immediate", "tinyint", "0"), // Gvboard 新增（决策 D68）
		},
			unique("trade_no", "trade_no"),
			idx("idx_user", "user_id"),
			idx("idx_user_status", "user_id", "status"),
			Index{Name: "idx_subscription", Cols: []string{"subscription_id"}, Ensure: true},
		),
		table("v2_payment", []Column{
			col("uuid", "char(32)"),
			col("payment", "varchar(16)"),
			col("name", "varchar(255)"),
			null("icon", "varchar(255)"),
			col("config", "text"),
			null("notify_domain", "varchar(128)"),
			null("handling_fee_fixed", "int"),
			null("handling_fee_percent", "decimal(5,2)"),
			def("enable", "tinyint", "0"),
			null("sort", "int"),
		}),
		table("v2_plan", []Column{
			col("group_id", "int"),
			col("transfer_enable", "int"),
			null("device_limit", "int"),
			col("name", "varchar(255)"),
			null("speed_limit", "int"),
			def("show", "tinyint", "0"),
			null("sort", "int"),
			def("renew", "tinyint", "1"),
			null("content", "text"),
			null("month_price", "int"),
			null("quarter_price", "int"),
			null("half_year_price", "int"),
			null("year_price", "int"),
			null("two_year_price", "int"),
			null("three_year_price", "int"),
			null("onetime_price", "int"),
			null("reset_price", "int"),
			null("reset_traffic_method", "tinyint"),
			null("capacity_limit", "int"),
		}),
		table("v2_server_group", []Column{
			col("name", "varchar(255)"),
		}),
		table("v2_server_tuic", cols(serverCommon("varchar(11)"), []Column{
			null("server_name", "varchar(64)"),
			def("insecure", "tinyint", "0"),
			def("disable_sni", "tinyint", "0"),
			null("udp_relay_mode", "varchar(64)"),
			def("zero_rtt_handshake", "tinyint", "0"),
			null("congestion_control", "varchar(64)"),
		})),
		table("v2_server_hysteria", cols([]Column{col("version", "int")}, serverCommon("varchar(255)"), []Column{
			col("up_mbps", "int"),
			col("down_mbps", "int"),
			null("obfs", "varchar(64)"),
			null("obfs_password", "varchar(255)"),
			null("server_name", "varchar(64)"),
			def("insecure", "tinyint", "0"),
		})),
		table("v2_server_route", []Column{
			col("remarks", "varchar(255)"),
			col("match", "text"),
			col("action", "varchar(11)"),
			null("action_value", "text"),
		}),
		table("v2_server_shadowsocks", cols(serverCommon("varchar(11)"), []Column{
			col("cipher", "varchar(255)"),
			null("obfs", "char(11)"),
			null("obfs_settings", "varchar(255)"),
		})),
		table("v2_server_trojan", cols(serverCommon("varchar(11)"), []Column{
			null("network", "varchar(11)"),
			null("network_settings", "text"),
			def("allow_insecure", "tinyint", "0"),
			null("server_name", "varchar(255)"),
		})),
		table("v2_server_vless", cols(vlessCommon(), []Column{
			col("tls", "tinyint"),
			null("tls_settings", "text"),
			null("flow", "varchar(64)"),
			col("network", "varchar(11)"),
			null("network_settings", "text"),
			null("encryption", "varchar(64)"),
			null("encryption_settings", "text"),
		})),
		table("v2_server_vmess", cols(serverCommon("varchar(11)"), []Column{
			def("tls", "tinyint", "0"),
			col("network", "varchar(11)"),
			null("rules", "text"),
			null("networkSettings", "text"),
			null("tlsSettings", "text"),
			null("ruleSettings", "text"),
			null("dnsSettings", "text"),
		})),
		table("v2_server_anytls", cols(serverCommon("varchar(11)"), []Column{
			null("server_name", "varchar(64)"),
			def("insecure", "tinyint", "0"),
			null("padding_scheme", "text"),
		})),
		table("v2_server_v2node", cols(serverCommon("varchar(11)"), []Column{
			def("listen_ip", "varchar(255)", "'0.0.0.0'"),
			col("protocol", "varchar(24)"),
			col("tls", "tinyint"),
			null("tls_settings", "text"),
			null("flow", "varchar(64)"),
			col("network", "varchar(11)"),
			null("network_settings", "text"),
			null("trusted_x_forwarded_for", "varchar(255)"),
			null("encryption", "varchar(64)"),
			null("encryption_settings", "text"),
			def("disable_sni", "tinyint", "0"),
			null("udp_relay_mode", "varchar(64)"),
			def("zero_rtt_handshake", "tinyint", "0"),
			null("congestion_control", "varchar(64)"),
			null("cipher", "varchar(64)"),
			col("up_mbps", "int"),
			col("down_mbps", "int"),
			null("obfs", "varchar(64)"),
			null("obfs_password", "varchar(255)"),
			null("padding_scheme", "text"),
		})),
		table("v2_stat", []Column{
			col("record_at", "int"),
			col("record_type", "char(1)"),
			col("order_count", "int"),
			col("order_total", "int"),
			col("commission_count", "int"),
			col("commission_total", "int"),
			col("paid_count", "int"),
			col("paid_total", "int"),
			col("register_count", "int"),
			col("invite_count", "int"),
			col("transfer_used_total", "varchar(32)"),
		}, unique("record_at", "record_at")),
		table("v2_stat_server", []Column{
			col("server_id", "int"),
			col("server_type", "char(11)"),
			col("u", "bigint"),
			col("d", "bigint"),
			col("record_type", "char(1)"),
			col("record_at", "int"),
		},
			unique("server_id_server_type_record_at", "server_id", "server_type", "record_at"),
			idx("record_at", "record_at"),
			idx("server_id", "server_id"),
		),
		table("v2_stat_user", []Column{
			col("user_id", "int"),
			col("server_rate", "decimal(10,2)"),
			col("u", "bigint"),
			col("d", "bigint"),
			col("record_type", "char(2)"),
			col("record_at", "int"),
			null("subscription_id", "int"), // Gvboard 新增
		},
			idx("user_id", "user_id"),
			idx("record_at", "record_at"),
			idx("server_rate", "server_rate"),
			// 多订阅后按订阅汇总，原唯一索引 server_rate_user_id_record_at 由迁移步骤删除。
			Index{Name: "uniq_rate_sub_record", Cols: []string{"server_rate", "subscription_id", "record_at"}, Unique: true, Ensure: true},
			Index{Name: "idx_subscription", Cols: []string{"subscription_id"}, Ensure: true},
		),
		table("v2_ticket", []Column{
			col("user_id", "int"),
			col("subject", "varchar(255)"),
			col("level", "tinyint"),
			def("status", "tinyint", "0"),
			def("reply_status", "tinyint", "0"),
			null("subscription_id", "int"), // Gvboard 新增（决策 D48）
		}, Index{Name: "idx_user", Cols: []string{"user_id"}, Ensure: true}),
		table("v2_ticket_message", []Column{
			col("user_id", "int"),
			col("ticket_id", "int"),
			col("message", "text"),
		}, Index{Name: "idx_ticket", Cols: []string{"ticket_id"}, Ensure: true}),
		table("v2_user", []Column{
			null("invite_user_id", "int"),
			null("telegram_id", "bigint"),
			col("email", "varchar(64)"),
			col("password", "varchar(64)"),
			null("password_algo", "char(10)"),
			null("password_salt", "char(10)"),
			def("balance", "int", "0"),
			null("discount", "int"),
			def("commission_type", "tinyint", "0"),
			null("commission_rate", "int"),
			def("commission_balance", "int", "0"),
			def("t", "int", "0"),
			def("u", "bigint", "0"),
			def("d", "bigint", "0"),
			def("transfer_enable", "bigint", "0"),
			null("device_limit", "int"),
			def("banned", "tinyint", "0"),
			def("is_admin", "tinyint", "0"),
			null("last_login_at", "int"),
			def("is_staff", "tinyint", "0"),
			null("last_login_ip", "int"),
			col("uuid", "varchar(36)"),
			null("group_id", "int"),
			null("plan_id", "int"),
			null("speed_limit", "int"),
			nullDef("auto_renewal", "tinyint", "0"),
			nullDef("remind_expire", "tinyint", "1"),
			nullDef("remind_traffic", "tinyint", "1"),
			col("token", "char(32)"),
			nullDef("expired_at", "bigint", "0"),
			null("remarks", "text"),
			null("subscription_limit", "int"), // Gvboard 新增
		},
			unique("email", "email"),
			unique("token", "token"),
			Index{Name: "idx_invite_user", Cols: []string{"invite_user_id"}, Ensure: true},
			Index{Name: "idx_telegram", Cols: []string{"telegram_id"}, Ensure: true},
		),

		// ===== Gvboard 新增表 =====
		table("v2_subscription", []Column{
			col("user_id", "int"),
			def("name", "varchar(64)", "''"),
			def("is_primary", "tinyint", "0"),
			null("plan_id", "int"),
			null("group_id", "int"),
			col("uuid", "varchar(36)"),
			col("token", "char(32)"),
			def("transfer_enable", "bigint", "0"),
			def("u", "bigint", "0"),
			def("d", "bigint", "0"),
			def("t", "int", "0"),
			null("device_limit", "int"),
			null("speed_limit", "int"),
			nullDef("expired_at", "bigint", "0"),
			def("auto_renewal", "tinyint", "0"),
			def("remind_expire", "tinyint", "1"),
			def("remind_traffic", "tinyint", "1"),
			def("banned", "tinyint", "0"),
		},
			Index{Name: "uniq_token", Cols: []string{"token"}, Unique: true, Ensure: true},
			Index{Name: "uniq_uuid", Cols: []string{"uuid"}, Unique: true, Ensure: true},
			Index{Name: "idx_user", Cols: []string{"user_id"}, Ensure: true},
			Index{Name: "idx_group", Cols: []string{"group_id"}, Ensure: true},
		),
		table("v2_settings", []Column{
			col("name", "varchar(64)"),
			null("value", "longtext"),
		}, Index{Name: "uniq_name", Cols: []string{"name"}, Unique: true, Ensure: true}),
		table("v2_plugin", []Column{
			col("code", "varchar(64)"),
			col("type", "varchar(16)"),
			def("version", "varchar(32)", "''"),
			def("enabled", "tinyint", "0"),
			null("config", "longtext"),
		}, Index{Name: "uniq_code", Cols: []string{"code"}, Unique: true, Ensure: true}),
		table("v2_plugin_kv", []Column{
			col("plugin_code", "varchar(64)"),
			col("k", "varchar(191)"),
			null("v", "longtext"),
			null("expire_at", "int"),
		}, Index{Name: "uniq_plugin_k", Cols: []string{"plugin_code", "k"}, Unique: true, Ensure: true}),
		// JS 插件安装包（决策 D56），archive 为 zip 的 base64
		table("v2_plugin_package", []Column{
			col("plugin_code", "varchar(64)"),
			col("sha256", "varchar(64)"),
			def("size", "int", "0"),
			null("manifest", "text"),
			null("archive", "longtext"),
		}, Index{Name: "uniq_code", Cols: []string{"plugin_code"}, Unique: true, Ensure: true}),
		// Telegram 机器人的用户偏好（决策 D51）
		table("v2_user_telegram", []Column{
			col("user_id", "int"),
			null("language", "varchar(16)"),
			def("notify_order", "tinyint", "1"),
			def("notify_ticket", "tinyint", "1"),
			def("notify_remind", "tinyint", "1"),
		}, Index{Name: "uniq_user", Cols: []string{"user_id"}, Unique: true, Ensure: true}),
		// 订阅下发风控（决策 D46）
		table("v2_subscribe_rule", []Column{
			def("name", "varchar(64)", "''"),
			col("target", "varchar(16)"),
			def("header", "varchar(64)", "''"),
			col("match_type", "varchar(16)"),
			null("pattern", "text"),
			def("negate", "tinyint", "0"),
			col("action", "varchar(16)"),
			def("priority", "int", "0"),
			def("enabled", "tinyint", "1"),
			def("hit_count", "bigint", "0"),
			null("last_hit_at", "int"),
		}),
		{
			Name: "v2_subscribe_log",
			Columns: []Column{
				pk(),
				col("subscription_id", "int"),
				col("user_id", "int"),
				col("ip", "varchar(64)"),
				def("user_agent", "varchar(255)", "''"),
				def("client", "varchar(32)", "''"),
				null("headers", "text"),
				col("result", "tinyint"),
				null("rule_id", "int"),
				col("created_at", "int"),
			},
			Indexes: []Index{
				{Name: "idx_created", Cols: []string{"created_at"}, Ensure: true},
				{Name: "idx_sub_created", Cols: []string{"subscription_id", "created_at"}, Ensure: true},
				{Name: "idx_ip_created", Cols: []string{"ip", "created_at"}, Ensure: true},
			},
		},
	}
}

// vlessCommon v2_server_vless 的端口为 int、group_id/route_id/tags 为 text，与其它节点表不同。
func vlessCommon() []Column {
	return []Column{
		col("group_id", "text"),
		null("route_id", "text"),
		col("name", "varchar(255)"),
		null("parent_id", "int"),
		col("host", "varchar(255)"),
		{Name: "port", Type: "int", PGType: "varchar(11)"},
		col("server_port", "int"),
		null("tags", "text"),
		col("rate", "varchar(11)"),
		def("show", "tinyint", "0"),
		null("sort", "int"),
	}
}

var typePattern = regexp.MustCompile(`^([a-z]+)(\((\d+)(,(\d+))?\))?$`)

// columnType 把抽象类型转换为方言类型。
func columnType(driver string, c Column) (string, error) {
	if driver == config.DriverPostgres && c.PGType != "" {
		return c.PGType, nil
	}
	m := typePattern.FindStringSubmatch(c.Type)
	if m == nil {
		return "", fmt.Errorf("列 %s 类型格式错误: %s", c.Name, c.Type)
	}
	base, args := m[1], m[2]
	if driver == config.DriverMySQL {
		return c.Type, nil
	}
	switch base {
	case "int":
		return "integer", nil
	case "bigint":
		return "bigint", nil
	case "tinyint":
		return "smallint", nil
	case "varchar", "char":
		// PostgreSQL 的 char(n) 会在末尾补空格，统一用 varchar 避免取值不一致。
		return "varchar" + args, nil
	case "text", "longtext":
		return "text", nil
	case "decimal":
		return "numeric" + args, nil
	}
	return "", fmt.Errorf("列 %s 不支持的类型 %s", c.Name, c.Type)
}

func quote(driver, ident string) string {
	if driver == config.DriverPostgres {
		return `"` + ident + `"`
	}
	return "`" + ident + "`"
}

// columnDDL 生成列定义。forAdd 为 true 表示向已有数据的表追加列：
// 非空且无默认值的列会自动补默认值（数值为 0）或改为可空，避免对存量数据报错。
func columnDDL(driver string, c Column, forAdd bool) (string, error) {
	q := quote(driver, c.Name)
	if c.AutoPK {
		if driver == config.DriverPostgres {
			return q + " integer GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY", nil
		}
		return q + " int NOT NULL AUTO_INCREMENT", nil
	}
	typ, err := columnType(driver, c)
	if err != nil {
		return "", err
	}
	null, dflt := c.Null, c.Default
	if forAdd && !null && dflt == "" {
		if isNumeric(c.Type) {
			dflt = "0"
		} else {
			null = true
		}
	}
	var b strings.Builder
	b.WriteString(q + " " + typ)
	if null {
		b.WriteString(" NULL")
	} else {
		b.WriteString(" NOT NULL")
	}
	if dflt != "" && !isTextType(c.Type) {
		if driver == config.DriverMySQL && !strings.HasPrefix(dflt, "'") {
			dflt = "'" + dflt + "'"
		}
		b.WriteString(" DEFAULT " + dflt)
	}
	return b.String(), nil
}

func isNumeric(t string) bool {
	return strings.HasPrefix(t, "int") || strings.HasPrefix(t, "bigint") ||
		strings.HasPrefix(t, "tinyint") || strings.HasPrefix(t, "decimal")
}

func isTextType(t string) bool { return t == "text" || t == "longtext" }

// indexName PostgreSQL 的索引名在模式内全局唯一，需加表名前缀；MySQL 保持原名以兼容原表。
func indexName(driver, tableName, name string) string {
	if driver == config.DriverPostgres {
		return tableName + "_" + name
	}
	return name
}

// CreateTableSQL 生成建表语句（MySQL 含索引；PostgreSQL 索引需另行创建）。
func CreateTableSQL(driver string, t Table) ([]string, error) {
	var defs []string
	for _, c := range t.Columns {
		d, err := columnDDL(driver, c, false)
		if err != nil {
			return nil, fmt.Errorf("表 %s: %w", t.Name, err)
		}
		defs = append(defs, "  "+d)
	}
	tq := quote(driver, t.Name)
	if driver == config.DriverMySQL {
		defs = append(defs, "  PRIMARY KEY (`id`)")
		for _, i := range t.Indexes {
			kind := "KEY"
			if i.Unique {
				kind = "UNIQUE KEY"
			}
			defs = append(defs, fmt.Sprintf("  %s %s (%s)", kind, quote(driver, i.Name), quoteCols(driver, i.Cols)))
		}
		return []string{fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (\n%s\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci",
			tq, strings.Join(defs, ",\n"))}, nil
	}
	stmts := []string{fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (\n%s\n)", tq, strings.Join(defs, ",\n"))}
	for _, i := range t.Indexes {
		stmts = append(stmts, CreateIndexSQL(driver, t.Name, i))
	}
	return stmts, nil
}

// CreateIndexSQL 生成建索引语句。
func CreateIndexSQL(driver, tableName string, i Index) string {
	kind := "INDEX"
	if i.Unique {
		kind = "UNIQUE INDEX"
	}
	name := quote(driver, indexName(driver, tableName, i.Name))
	if driver == config.DriverPostgres {
		return fmt.Sprintf("CREATE %s IF NOT EXISTS %s ON %s (%s)", kind, name, quote(driver, tableName), quoteCols(driver, i.Cols))
	}
	return fmt.Sprintf("CREATE %s %s ON %s (%s)", kind, name, quote(driver, tableName), quoteCols(driver, i.Cols))
}

// AddColumnSQL 生成追加列语句。
func AddColumnSQL(driver, tableName string, c Column) (string, error) {
	d, err := columnDDL(driver, c, true)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", quote(driver, tableName), d), nil
}

func quoteCols(driver string, cs []string) string {
	q := make([]string, len(cs))
	for i, c := range cs {
		q[i] = quote(driver, c)
	}
	return strings.Join(q, ", ")
}
