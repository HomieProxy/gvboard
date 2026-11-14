package migrate

// 数据库集成测试。需要真实数据库，通过环境变量启用：
//   GVB_TEST_MYSQL_DSN  例如 root:password@tcp(127.0.0.1:3306)/?charset=utf8mb4&multiStatements=true
//   GVB_TEST_PG_DSN     例如 postgres://postgres:password@127.0.0.1:5432/postgres?sslmode=disable
// 测试会创建并在结束后删除独立的临时数据库。

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"gvboard/internal/config"
	"gvboard/internal/model"
)

func openTestDB(t *testing.T, driver string) *gorm.DB {
	t.Helper()
	name := fmt.Sprintf("gvb_test_%d", time.Now().UnixNano())
	cfg := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}

	switch driver {
	case config.DriverMySQL:
		dsn := os.Getenv("GVB_TEST_MYSQL_DSN")
		if dsn == "" {
			t.Skip("未设置 GVB_TEST_MYSQL_DSN，跳过 MySQL 集成测试")
		}
		admin, err := gorm.Open(mysql.Open(dsn), cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := admin.Exec("CREATE DATABASE " + name + " CHARACTER SET utf8mb4").Error; err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { admin.Exec("DROP DATABASE " + name) })
		db, err := gorm.Open(mysql.Open(strings.Replace(dsn, "/?", "/"+name+"?", 1)), cfg)
		if err != nil {
			t.Fatal(err)
		}
		return db
	default:
		dsn := os.Getenv("GVB_TEST_PG_DSN")
		if dsn == "" {
			t.Skip("未设置 GVB_TEST_PG_DSN，跳过 PostgreSQL 集成测试")
		}
		admin, err := gorm.Open(postgres.Open(dsn), cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := admin.Exec("CREATE DATABASE " + name).Error; err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { admin.Exec("DROP DATABASE " + name + " WITH (FORCE)") })
		i := strings.LastIndex(dsn, "/")
		j := strings.Index(dsn[i:], "?")
		rest := ""
		if j >= 0 {
			rest = dsn[i+j:]
		}
		db, err := gorm.Open(postgres.Open(dsn[:i+1]+name+rest), cfg)
		if err != nil {
			t.Fatal(err)
		}
		return db
	}
}

// loadOriginalSchema 用原项目 install.sql 建立"原生产库"结构。
func loadOriginalSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	b, err := os.ReadFile("testdata/v2board_install.sql")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	for _, stmt := range strings.Split(src, ";\n") {
		var lines []string
		for _, l := range strings.Split(stmt, "\n") {
			if !strings.HasPrefix(strings.TrimSpace(l), "--") {
				lines = append(lines, l)
			}
		}
		s := strings.TrimSpace(strings.Join(lines, "\n"))
		if s == "" {
			continue
		}
		if err := db.Exec(s).Error; err != nil {
			t.Fatalf("执行原项目建表语句失败: %v\n%s", err, s)
		}
	}
}

// seedOriginalData 写入原项目格式的样例数据：3 个用户、订单、流量统计。
func seedOriginalData(t *testing.T, db *gorm.DB) {
	t.Helper()
	now := time.Now().Unix()
	exp := now + 30*86400
	users := []map[string]any{
		{"id": 1, "email": "a@x.com", "password": "h", "uuid": "uuid-1", "token": "token00000000000000000000000001", "plan_id": 1, "group_id": 1, "transfer_enable": 100 << 30, "u": 10, "d": 20, "expired_at": exp, "created_at": now, "updated_at": now},
		{"id": 5, "email": "b@x.com", "password": "h", "uuid": "uuid-5", "token": "token00000000000000000000000005", "plan_id": nil, "group_id": nil, "expired_at": 0, "remind_expire": nil, "created_at": now, "updated_at": now},
		{"id": 9, "email": "c@x.com", "password": "h", "uuid": "uuid-9", "token": "token00000000000000000000000009", "plan_id": 2, "group_id": 2, "transfer_enable": 50 << 30, "expired_at": nil, "created_at": now, "updated_at": now},
	}
	for _, u := range users {
		if err := db.Table("v2_user").Create(u).Error; err != nil {
			t.Fatal(err)
		}
	}
	orders := []map[string]any{
		{"user_id": 1, "plan_id": 1, "type": 1, "period": "month_price", "trade_no": "T1", "total_amount": 100, "status": 3, "created_at": now, "updated_at": now},
		{"user_id": 1, "plan_id": 0, "type": 9, "period": "deposit", "trade_no": "T2", "total_amount": 100, "status": 3, "created_at": now, "updated_at": now},
		{"user_id": 404, "plan_id": 1, "type": 1, "period": "month_price", "trade_no": "T3", "total_amount": 100, "status": 3, "created_at": now, "updated_at": now},
	}
	for _, o := range orders {
		if err := db.Table("v2_order").Create(o).Error; err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		row := map[string]any{"user_id": 1, "server_rate": 1.0, "u": 1, "d": 2, "record_type": "d", "record_at": now - int64(i)*86400, "created_at": now, "updated_at": now}
		if err := db.Table("v2_stat_user").Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func assertMigrated(t *testing.T, db *gorm.DB) {
	t.Helper()
	var subs []model.Subscription
	if err := db.Order("id").Find(&subs).Error; err != nil {
		t.Fatal(err)
	}
	if len(subs) != 3 {
		t.Fatalf("应为 3 个用户各创建 1 个主订阅，实际 %d", len(subs))
	}
	for _, s := range subs {
		if s.ID != s.UserID || s.IsPrimary != 1 {
			t.Errorf("主订阅 ID 应等于用户 ID 且为主订阅: %+v", s)
		}
	}
	s1 := subs[0]
	if s1.UUID != "uuid-1" || s1.Token != "token00000000000000000000000001" || s1.U != 10 || s1.D != 20 ||
		s1.TransferEnable != 100<<30 || s1.PlanID == nil || *s1.PlanID != 1 || s1.ExpiredAt == nil {
		t.Errorf("订阅数据未原样复制: %+v", s1)
	}
	if subs[1].ExpiredAt == nil || *subs[1].ExpiredAt != 0 || subs[1].RemindExpire != 1 {
		t.Errorf("expired_at=0 应保持为 0（已到期），remind_expire 为 NULL 时应取默认 1: %+v", subs[1])
	}
	if subs[2].ExpiredAt != nil {
		t.Errorf("expired_at 为 NULL（一次性长期有效）应保持 NULL: %+v", subs[2])
	}

	var orders []model.Order
	db.Order("trade_no").Find(&orders)
	if orders[0].SubscriptionID == nil || *orders[0].SubscriptionID != 1 {
		t.Errorf("套餐订单应关联主订阅: %+v", orders[0].SubscriptionID)
	}
	if orders[1].SubscriptionID != nil {
		t.Error("充值订单不应关联订阅")
	}
	if orders[2].SubscriptionID != nil {
		t.Error("已删除用户的订单不应关联订阅")
	}

	var unfilled int64
	db.Model(&model.StatUser{}).Where("subscription_id IS NULL").Count(&unfilled)
	if unfilled != 0 {
		t.Errorf("仍有 %d 条流量统计未回填订阅 ID", unfilled)
	}

	// 新订阅的自增 ID 不能与迁移的订阅冲突
	ns := model.Subscription{UserID: 1, UUID: "uuid-new", Token: "token000000000000000000000000new"}
	if err := db.Create(&ns).Error; err != nil {
		t.Fatalf("新建订阅失败: %v", err)
	}
	if ns.ID <= 9 {
		t.Errorf("新订阅 ID 应大于已迁移的最大 ID 9，实际 %d", ns.ID)
	}

	// 同一用户不同订阅在同一天同倍率的统计不应冲突（原唯一索引已删除）
	now := time.Now().Unix()
	for _, sid := range []int64{1, ns.ID} {
		row := model.StatUser{UserID: 1, SubscriptionID: &sid, ServerRate: 2, RecordType: "d", RecordAt: now}
		if err := db.Create(&row).Error; err != nil {
			t.Errorf("多订阅流量统计写入失败: %v", err)
		}
	}
}

func runMigrate(t *testing.T, db *gorm.DB, driver string) *Report {
	t.Helper()
	rep, err := New(db, driver).Run(context.Background())
	if err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	return rep
}

func TestMigrateOriginalMySQL(t *testing.T) {
	db := openTestDB(t, config.DriverMySQL)
	loadOriginalSchema(t, db)
	seedOriginalData(t, db)

	rep := runMigrate(t, db, config.DriverMySQL)
	for _, want := range []string{"v2_user.subscription_limit", "v2_order.subscription_id", "v2_stat_user.subscription_id"} {
		if !strings.Contains(strings.Join(rep.AddedColumns, ","), want) {
			t.Errorf("应为原表新增列 %s，实际 %v", want, rep.AddedColumns)
		}
	}
	if len(rep.AppliedSteps) != len(Steps()) {
		t.Errorf("应执行全部迁移步骤: %v", rep.AppliedSteps)
	}
	assertMigrated(t, db)
	if !strings.Contains(strings.Join(rep.AddedColumns, ","), "v2_ticket.subscription_id") {
		t.Errorf("应为 v2_ticket 新增 subscription_id: %v", rep.AddedColumns)
	}
	for _, tc := range [][2]string{{"v2_ticket", "user_id"}, {"v2_ticket_message", "ticket_id"}} {
		var n int64
		db.Raw("SELECT COUNT(*) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?", tc[0], tc[1]).Scan(&n)
		if n == 0 {
			t.Errorf("%s.%s 应建立索引", tc[0], tc[1])
		}
	}
	var legacy int64
	db.Raw("SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME IN ('v2_ticket', 'v2_notice') " +
		"AND CHARACTER_SET_NAME IS NOT NULL AND CHARACTER_SET_NAME <> 'utf8mb4'").Scan(&legacy)
	if legacy != 0 {
		t.Errorf("v2_ticket、v2_notice 应全部转为 utf8mb4，仍有 %d 列", legacy)
	}
	if err := db.Create(&model.Ticket{UserID: 1, Subject: "无法连接 🔥", Level: 1}).Error; err != nil {
		t.Errorf("工单标题应支持 emoji: %v", err)
	}
	if err := db.Create(&model.Notice{Title: "公告", Content: "x", Tags: model.StrList{"🎉 活动"}}).Error; err != nil {
		t.Errorf("公告标签应支持 emoji: %v", err)
	}

	// 重复执行不应有任何变化
	rep = runMigrate(t, db, config.DriverMySQL)
	if len(rep.CreatedTables)+len(rep.AddedColumns)+len(rep.CreatedIndexes)+len(rep.AppliedSteps) != 0 {
		t.Errorf("重复执行迁移应无变化: %+v", rep)
	}
}

func TestMigrateOldVersionMySQL(t *testing.T) {
	// 模拟较老版本的原生产库：缺少 v2node 表与 vless 的 encryption 列
	db := openTestDB(t, config.DriverMySQL)
	loadOriginalSchema(t, db)
	db.Exec("DROP TABLE v2_server_v2node")
	db.Exec("ALTER TABLE v2_server_vless DROP COLUMN encryption, DROP COLUMN encryption_settings")
	seedOriginalData(t, db)

	rep := runMigrate(t, db, config.DriverMySQL)
	if !strings.Contains(strings.Join(rep.CreatedTables, ","), "v2_server_v2node") {
		t.Errorf("应补建缺失的表: %v", rep.CreatedTables)
	}
	if !strings.Contains(strings.Join(rep.AddedColumns, ","), "v2_server_vless.encryption") {
		t.Errorf("应补充缺失的列: %v", rep.AddedColumns)
	}
	assertMigrated(t, db)
}

func TestMigrateDuplicateUUID(t *testing.T) {
	db := openTestDB(t, config.DriverMySQL)
	loadOriginalSchema(t, db)
	seedOriginalData(t, db)
	db.Exec("UPDATE v2_user SET uuid = 'uuid-1' WHERE id = 5")
	if _, err := New(db, config.DriverMySQL).Run(context.Background()); err == nil || !strings.Contains(err.Error(), "uuid-1") {
		t.Errorf("存在重复 uuid 时应中止并列出用户: %v", err)
	}
}

func TestMigrateForeignSettings(t *testing.T) {
	db := openTestDB(t, config.DriverMySQL)
	loadOriginalSchema(t, db)
	if err := db.Exec("CREATE TABLE v2_settings (id int NOT NULL AUTO_INCREMENT PRIMARY KEY, `group` varchar(64), `type` varchar(32), name varchar(64), value text)").Error; err != nil {
		t.Fatal(err)
	}
	_, err := New(db, config.DriverMySQL).Run(context.Background())
	if !errors.Is(err, ErrForeignSettings) || !strings.Contains(err.Error(), "v2_settings_xboard_bak") {
		t.Fatalf("Xboard 的 v2_settings 应中止迁移并给出改名语句: %v", err)
	}
	var n int64
	db.Raw("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'v2_subscription'").Scan(&n)
	if n != 0 {
		t.Error("中止时不应修改数据库")
	}
	db.Exec("RENAME TABLE v2_settings TO v2_settings_xboard_bak")
	runMigrate(t, db, config.DriverMySQL)
}

// testFreshInstall 全新安装建全部表，再模拟导入原库数据后执行订阅迁移。
func testFreshInstall(t *testing.T, driver string) {
	db := openTestDB(t, driver)
	rep := runMigrate(t, db, driver)
	if len(rep.CreatedTables) != len(Schema()) {
		t.Errorf("全新安装应创建全部 %d 张表，实际 %d", len(Schema()), len(rep.CreatedTables))
	}
	seedOriginalData(t, db)
	db.Exec(`DELETE FROM v2_gvb_migration WHERE id = '2026111302_subscription_backfill'`)
	runMigrate(t, db, driver)
	assertMigrated(t, db)
}

func TestMigrateFreshMySQL(t *testing.T) { testFreshInstall(t, config.DriverMySQL) }

func TestMigratePostgres(t *testing.T) { testFreshInstall(t, config.DriverPostgres) }
