package migrate

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gvboard/internal/config"
	"gvboard/internal/model"
)

func TestConvertMySQLToPostgres(t *testing.T) {
	src := openTestDB(t, config.DriverMySQL)
	dst := openTestDB(t, config.DriverPostgres)
	ctx := context.Background()

	loadOriginalSchema(t, src)
	seedOriginalData(t, src)

	// 源库未执行 migrate 时拒绝
	if _, err := Convert(ctx, src, dst, ConvertOptions{}); !errors.Is(err, ErrSourcePending) {
		t.Fatalf("源库未迁移时应拒绝: %v", err)
	}
	runMigrate(t, src, config.DriverMySQL)

	// 各种取值：emoji、空字符、NULL、小数、混合大小写列名、vless 的 int 端口（PostgreSQL 为 varchar）
	exec := func(sql string, args ...any) {
		t.Helper()
		if err := src.Exec(sql, args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO v2_notice (title, content, `show`, tags, created_at, updated_at) VALUES (?, ?, 1, NULL, 1, 1)", "公告 🎉", "a\x00b")
	exec("INSERT INTO v2_payment (uuid, payment, name, config, handling_fee_percent, `enable`, created_at, updated_at) VALUES ('u1', 'EPay', 'p', '{}', 2.55, 1, 1, 1)")
	exec("INSERT INTO v2_server_vless (group_id, name, host, port, server_port, rate, tls, network, created_at, updated_at) VALUES ('[\"1\"]', 'n', 'h', 443, 443, '1.5', 1, 'tcp', 1, 1)")
	exec("INSERT INTO v2_server_vmess (group_id, name, host, port, server_port, rate, network, networkSettings, created_at, updated_at) VALUES ('[\"1\"]', 'm', 'h', '443', 443, '1', 'ws', '{\"path\":\"/\"}', 1, 1)")
	exec("INSERT INTO v2_settings (name, value, created_at, updated_at) VALUES ('app_name', 'X', 1, 1)")
	for i := 0; i < 7; i++ {
		exec("INSERT INTO v2_log (title, uri, method, created_at, updated_at) VALUES ('t', '/', 'GET', 1, 1)")
	}
	// 源库多出的列与表
	exec("ALTER TABLE v2_server_vmess ADD COLUMN ips text NULL")
	exec("CREATE TABLE v2_server_v2node_bak (id int PRIMARY KEY)")

	var progress []string
	rep, err := Convert(ctx, src, dst, ConvertOptions{
		BatchSize:  2, // 小批量，覆盖分页
		SkipTables: map[string]bool{"v2_mail_log": true},
		Progress:   func(table string, _ int64) { progress = append(progress, table) },
	})
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	rows := map[string]int64{}
	for _, tb := range rep.Tables {
		rows[tb.Name] = tb.Rows
	}
	if rows["v2_user"] != 3 || rows["v2_subscription"] != 3 || rows["v2_order"] != 3 || rows["v2_stat_user"] != 3 || rows["v2_log"] != 7 {
		t.Errorf("行数有误: %v", rows)
	}
	if len(rep.Skipped) != 1 || rep.Skipped[0] != "v2_mail_log" || len(progress) != len(Schema())-1 {
		t.Errorf("跳过的表或进度回调有误: %v %d", rep.Skipped, len(progress))
	}
	if strings.Join(rep.IgnoredTables, ",") != "v2_server_v2node_bak" || !strings.Contains(strings.Join(rep.IgnoredColumns, ","), "v2_server_vmess.ips") {
		t.Errorf("应报告未复制的表与列: %v %v", rep.IgnoredTables, rep.IgnoredColumns)
	}
	if len(rep.Notices) != 1 || !strings.Contains(rep.Notices[0], "1 个值") {
		t.Errorf("应报告移除空字符: %v", rep.Notices)
	}

	assertMigrated(t, dst) // 数据、订阅关联、自增序列（新订阅 ID 大于已有最大值）
	var notice model.Notice
	dst.First(&notice)
	if notice.Title != "公告 🎉" || notice.Content != "ab" || notice.Tags != nil {
		t.Errorf("公告取值有误: %+v", notice)
	}
	var fee string
	dst.Raw(`SELECT handling_fee_percent::text FROM v2_payment`).Scan(&fee)
	if fee != "2.55" {
		t.Errorf("小数取值有误: %s", fee)
	}
	var port, ns string
	dst.Raw(`SELECT port FROM v2_server_vless`).Scan(&port)
	dst.Raw(`SELECT "networkSettings" FROM v2_server_vmess`).Scan(&ns)
	if port != "443" || ns != `{"path":"/"}` {
		t.Errorf("节点取值有误: %q %q", port, ns)
	}
	var user model.User
	if err := dst.Where("lower(email) = ?", "a@x.com").Take(&user).Error; err != nil || user.ID != 1 {
		t.Errorf("用户取值有误: %v %+v", err, user)
	}
	if err := dst.Exec(`INSERT INTO v2_user (email, password, uuid, token, created_at, updated_at) VALUES ('A@X.COM', 'h', 'u-x', 'tok-x', 1, 1)`).Error; err == nil {
		t.Error("PostgreSQL 应建立小写邮箱唯一索引")
	}
	var nl int64
	dst.Raw(`SELECT COUNT(*) FROM v2_log`).Scan(&nl)
	var log model.Log
	if err := dst.Create(&model.Log{Title: "new", URI: "/", Method: "GET"}).Error; err != nil || nl != 7 {
		t.Errorf("日志表写入失败: %v %d", err, nl)
	}
	dst.Order("id DESC").First(&log)
	if log.ID <= 7 {
		t.Errorf("日志表自增序列未同步: %d", log.ID)
	}

	// 目标库已有数据时拒绝
	if _, err := Convert(ctx, src, dst, ConvertOptions{}); !errors.Is(err, ErrTargetNotEmpty) {
		t.Fatalf("目标库非空时应拒绝: %v", err)
	}
}

func TestConvertRollback(t *testing.T) {
	src := openTestDB(t, config.DriverMySQL)
	dst := openTestDB(t, config.DriverPostgres)
	ctx := context.Background()
	runMigrate(t, src, config.DriverMySQL)
	seedOriginalData(t, src)
	if err := src.Exec("INSERT INTO v2_subscription (user_id, uuid, token, created_at, updated_at) SELECT id, uuid, token, 1, 1 FROM v2_user").Error; err != nil {
		t.Fatal(err)
	}
	// v2_notice 在 v2_order 之前复制
	if err := src.Exec("INSERT INTO v2_notice (title, content, created_at, updated_at) VALUES ('n', 'c', 1, 1)").Error; err != nil {
		t.Fatal(err)
	}
	// 目标库中预先建立冲突的唯一约束，让复制中途失败
	if _, err := New(dst, config.DriverPostgres).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if err := dst.Exec(`CREATE UNIQUE INDEX conflict_uniq ON v2_order (user_id)`).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := Convert(ctx, src, dst, ConvertOptions{}); err == nil || !strings.Contains(err.Error(), "v2_order") {
		t.Fatalf("应在复制 v2_order 时失败: %v", err)
	}
	var n int64
	dst.Raw(`SELECT COUNT(*) FROM v2_notice`).Scan(&n)
	if n != 0 {
		t.Errorf("失败后目标库不应留下数据，v2_notice 有 %d 行", n)
	}
	// 去掉冲突后可直接重试
	dst.Exec(`DROP INDEX conflict_uniq`)
	if _, err := Convert(ctx, src, dst, ConvertOptions{}); err != nil {
		t.Fatalf("重试失败: %v", err)
	}
}
