package migrate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"gvboard/internal/config"
)

// Steps 全部一次性迁移步骤，按顺序执行。新增步骤只能追加在末尾，已发布的步骤不得修改。
func Steps() []Step {
	return []Step{
		{
			ID:          "2026111301_stat_user_drop_legacy_unique",
			Description: "删除 v2_stat_user 原唯一索引（多订阅后改为按订阅汇总）",
			Run:         stepDropStatUserLegacyUnique,
		},
		{
			ID:          "2026111302_subscription_backfill",
			Description: "将用户现有订阅复制到 v2_subscription，并回填订单与流量统计的订阅 ID",
			Run:         stepSubscriptionBackfill,
		},
		{
			ID:          "2026111303_pg_user_email_lower",
			Description: "PostgreSQL 建立小写邮箱唯一索引（邮箱不区分大小写）",
			Run:         stepPGUserEmailLower,
		},
		{
			ID:          "2026111401_mysql_content_utf8mb4",
			Description: "MySQL 将 v2_ticket、v2_notice 中仍为 utf8 的表转为 utf8mb4（支持 emoji）",
			Run:         stepContentUTF8MB4,
		},
		{
			ID:          "2026111501_subscription_drop_idx_group",
			Description: "删除 v2_subscription 的 idx_group（已由 idx_group_expired 取代）",
			Run:         stepDropSubscriptionIdxGroup,
		},
		{
			ID:          "2026111901_audit_log_day_merge",
			Description: "审计日志按天回填 day、写入细节表并合并同日同键汇总",
			Run:         stepAuditLogDayMerge,
		},
	}
}

// stepAuditLogDayMerge 为已有审计日志补 day / detail_count、写入细节，并把同日同键多行合成一条。
func stepAuditLogDayMerge(ctx context.Context, m *Migrator) error {
	db := m.db.WithContext(ctx)
	type row struct {
		ID             int64
		SubscriptionID int64
		UserID         int64
		ServerType     string
		ServerID       int64
		RuleID         int64
		Action         string
		Target         string
		Network        string
		IP             string
		Count          int64
		Banned         int
		HitAt          int64
		CreatedAt      int64
		Day            int64
	}
	var rows []row
	if err := db.Table("v2_audit_log").Order("id ASC").Find(&rows).Error; err != nil {
		return err
	}
	loc := time.Local
	dayOf := func(ts int64) int64 {
		t := time.Unix(ts, 0).In(loc)
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc).Unix()
	}
	actionRank := func(a string) int {
		switch a {
		case "ban":
			return 3
		case "block":
			return 2
		case "log":
			return 1
		default:
			return 0
		}
	}
	for i := range rows {
		if rows[i].Day == 0 {
			rows[i].Day = dayOf(rows[i].HitAt)
			if err := db.Table("v2_audit_log").Where("id = ?", rows[i].ID).Update("day", rows[i].Day).Error; err != nil {
				return err
			}
		}
		var n int64
		if err := db.Table("v2_audit_log_detail").Where("log_id = ?", rows[i].ID).Count(&n).Error; err != nil {
			return err
		}
		if n == 0 {
			d := map[string]any{
				"log_id": rows[i].ID, "target": rows[i].Target, "network": rows[i].Network,
				"ip": rows[i].IP, "count": max(rows[i].Count, 1), "hit_at": rows[i].HitAt,
			}
			if err := db.Table("v2_audit_log_detail").Create(d).Error; err != nil {
				return err
			}
		}
	}
	type key struct {
		Sub, Rule, ServerID, Day int64
		ServerType               string
	}
	groups := map[key][]row{}
	for _, r := range rows {
		k := key{r.SubscriptionID, r.RuleID, r.ServerID, r.Day, r.ServerType}
		groups[k] = append(groups[k], r)
	}
	type det struct {
		ID      int64
		Target  string
		Network string
		IP      string
		Count   int64
		HitAt   int64
	}
	upsertDetail := func(logID int64, d det) error {
		var cur det
		err := db.Table("v2_audit_log_detail").
			Where("log_id = ? AND target = ? AND network = ?", logID, d.Target, d.Network).
			Take(&cur).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return db.Table("v2_audit_log_detail").Create(map[string]any{
				"log_id": logID, "target": d.Target, "network": d.Network,
				"ip": d.IP, "count": d.Count, "hit_at": d.HitAt,
			}).Error
		}
		if err != nil {
			return err
		}
		cur.Count += d.Count
		if d.HitAt >= cur.HitAt {
			cur.HitAt, cur.IP = d.HitAt, d.IP
		}
		return db.Table("v2_audit_log_detail").Where("id = ?", cur.ID).Updates(map[string]any{
			"count": cur.Count, "ip": cur.IP, "hit_at": cur.HitAt,
		}).Error
	}
	refresh := func(keep row, group []row) error {
		var details []det
		if err := db.Table("v2_audit_log_detail").Where("log_id = ?", keep.ID).Order("id ASC").Find(&details).Error; err != nil {
			return err
		}
		var total, hitAt int64
		top := det{}
		banned, action := 0, keep.Action
		for _, r := range group {
			if r.Banned != 0 {
				banned = 1
			}
			if actionRank(r.Action) > actionRank(action) {
				action = r.Action
			}
			hitAt = max(hitAt, r.HitAt)
		}
		for _, d := range details {
			total += d.Count
			hitAt = max(hitAt, d.HitAt)
			if d.Count > top.Count || (d.Count == top.Count && d.HitAt >= top.HitAt) {
				top = d
			}
		}
		return db.Table("v2_audit_log").Where("id = ?", keep.ID).Updates(map[string]any{
			"count": total, "detail_count": int64(len(details)), "action": action, "banned": banned,
			"target": top.Target, "network": top.Network, "ip": top.IP, "hit_at": hitAt, "day": keep.Day,
		}).Error
	}
	merged := 0
	for _, g := range groups {
		keep := g[0]
		if len(g) == 1 {
			if err := refresh(keep, g); err != nil {
				return err
			}
			continue
		}
		for _, r := range g[1:] {
			var details []det
			if err := db.Table("v2_audit_log_detail").Where("log_id = ?", r.ID).Find(&details).Error; err != nil {
				return err
			}
			for _, d := range details {
				if err := upsertDetail(keep.ID, d); err != nil {
					return err
				}
			}
			if err := db.Exec("DELETE FROM v2_audit_log_detail WHERE log_id = ?", r.ID).Error; err != nil {
				return err
			}
			if err := db.Exec("DELETE FROM v2_audit_log WHERE id = ?", r.ID).Error; err != nil {
				return err
			}
			merged++
		}
		if err := refresh(keep, g); err != nil {
			return err
		}
	}
	if merged > 0 {
		m.report.Notices = append(m.report.Notices, fmt.Sprintf("已按天合并 %d 条审计日志", merged))
	}
	return nil
}

// stepDropSubscriptionIdxGroup 所有订阅同在一个权限组时，MySQL 仍会选 idx_group 逐行回表，百万订阅时拉取节点用户需数秒。
func stepDropSubscriptionIdxGroup(ctx context.Context, m *Migrator) error {
	dropped, err := m.dropIndexIfExists(m.db.WithContext(ctx), "v2_subscription", "idx_group")
	if dropped {
		m.report.Notices = append(m.report.Notices, "已删除 v2_subscription 索引 idx_group")
	}
	return err
}

// stepContentUTF8MB4 原安装脚本中工单与公告表为 utf8，工单标题、公告标签含 emoji 时写入失败（决策 D49）。
func stepContentUTF8MB4(ctx context.Context, m *Migrator) error {
	if m.driver != config.DriverMySQL {
		return nil
	}
	db := m.db.WithContext(ctx)
	for _, t := range []string{"v2_ticket", "v2_notice"} {
		var n int64
		err := db.Raw("SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? "+
			"AND CHARACTER_SET_NAME IS NOT NULL AND CHARACTER_SET_NAME <> 'utf8mb4'", t).Scan(&n).Error
		if err != nil {
			return err
		}
		if n == 0 {
			continue
		}
		if err := db.Exec("ALTER TABLE " + m.q(t) + " CONVERT TO CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci").Error; err != nil {
			return fmt.Errorf("转换 %s 字符集失败: %w", t, err)
		}
		m.report.Notices = append(m.report.Notices, fmt.Sprintf("已将 %s 转为 utf8mb4（%d 列）", t, n))
	}
	return nil
}

// stepPGUserEmailLower 让 PostgreSQL 下的邮箱唯一性与查询和 MySQL 一致，都不区分大小写。
func stepPGUserEmailLower(ctx context.Context, m *Migrator) error {
	if m.driver != config.DriverPostgres {
		return nil
	}
	db := m.db.WithContext(ctx)
	var dups []string
	if err := db.Raw("SELECT lower(email) FROM v2_user GROUP BY lower(email) HAVING COUNT(*) > 1 LIMIT 20").Scan(&dups).Error; err != nil {
		return err
	}
	if len(dups) > 0 {
		return fmt.Errorf("以下邮箱仅大小写不同、存在多个账号，请先合并或修改后再迁移：\n%s", strings.Join(dups, "\n"))
	}
	return db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS v2_user_email_lower ON v2_user (lower(email))").Error
}

func stepDropStatUserLegacyUnique(ctx context.Context, m *Migrator) error {
	dropped, err := m.dropIndexIfExists(m.db.WithContext(ctx), "v2_stat_user", "server_rate_user_id_record_at")
	if dropped {
		m.report.Notices = append(m.report.Notices, "已删除 v2_stat_user 原唯一索引 server_rate_user_id_record_at")
	}
	return err
}

func stepSubscriptionBackfill(ctx context.Context, m *Migrator) error {
	db := m.db.WithContext(ctx)
	q := m.q

	// 1. 检查重复 uuid：订阅 uuid 需全局唯一，用于节点认证。
	var dups []struct {
		UUID string
		Ids  string
	}
	agg := "GROUP_CONCAT(id)"
	if m.driver == config.DriverPostgres {
		agg = "string_agg(CAST(id AS varchar), ',')"
	}
	if err := db.Raw(fmt.Sprintf("SELECT %s AS uuid, %s AS ids FROM %s GROUP BY %s HAVING COUNT(*) > 1 LIMIT 20",
		q("uuid"), agg, q("v2_user"), q("uuid"))).Scan(&dups).Error; err != nil {
		return err
	}
	if len(dups) > 0 {
		var lines []string
		for _, d := range dups {
			lines = append(lines, fmt.Sprintf("uuid=%s 用户ID=%s", d.UUID, d.Ids))
		}
		return fmt.Errorf("%w，请先在原面板为这些用户重置订阅后再迁移：\n%s", ErrDuplicateUUID, strings.Join(lines, "\n"))
	}

	if err := db.Transaction(func(tx *gorm.DB) error {
		now := time.Now().Unix()
		subCols := "user_id, name, is_primary, plan_id, group_id, uuid, token, transfer_enable, u, d, t, device_limit, speed_limit, expired_at, auto_renewal, remind_expire, remind_traffic, banned, created_at, updated_at"
		selCols := "u.id, '', 1, u.plan_id, u.group_id, u.uuid, u.token, u.transfer_enable, u.u, u.d, u.t, u.device_limit, u.speed_limit, u.expired_at, COALESCE(u.auto_renewal, 0), COALESCE(u.remind_expire, 1), COALESCE(u.remind_traffic, 1), 0, u.created_at, ?"
		noSub := "NOT EXISTS (SELECT 1 FROM v2_subscription s WHERE s.user_id = u.id)"

		// 2. 主订阅 ID 与用户 ID 相同，保证节点侧用户 ID、在线 IP、历史流量统计无感知。
		res := tx.Exec(fmt.Sprintf("INSERT INTO v2_subscription (id, %s) SELECT u.id, %s FROM v2_user u WHERE %s AND NOT EXISTS (SELECT 1 FROM v2_subscription s2 WHERE s2.id = u.id)",
			subCols, selCols, noSub), now)
		if res.Error != nil {
			return fmt.Errorf("复制订阅失败: %w", res.Error)
		}
		copied := res.RowsAffected

		// 3. 极端情况下 ID 已被占用的用户，改用自增 ID。
		res = tx.Exec(fmt.Sprintf("INSERT INTO v2_subscription (%s) SELECT %s FROM v2_user u WHERE %s", subCols, selCols, noSub), now)
		if res.Error != nil {
			return fmt.Errorf("复制订阅失败: %w", res.Error)
		}
		copied += res.RowsAffected
		m.report.Notices = append(m.report.Notices, fmt.Sprintf("已为 %d 个用户创建主订阅", copied))

		// 4. PostgreSQL 显式插入 ID 后需要同步自增序列。
		if m.driver == config.DriverPostgres {
			if err := tx.Exec(`SELECT setval(pg_get_serial_sequence('v2_subscription', 'id'), COALESCE((SELECT MAX(id) FROM v2_subscription), 0) + 1, false)`).Error; err != nil {
				return fmt.Errorf("同步订阅序列失败: %w", err)
			}
		}

		// 5. 历史订单关联到主订阅（充值订单不关联订阅）。
		res = tx.Exec(`UPDATE v2_order SET subscription_id = user_id WHERE subscription_id IS NULL AND period <> 'deposit'
			AND EXISTS (SELECT 1 FROM v2_subscription s WHERE s.id = v2_order.user_id AND s.user_id = v2_order.user_id)`)
		if res.Error != nil {
			return fmt.Errorf("回填订单订阅 ID 失败: %w", res.Error)
		}
		m.report.Notices = append(m.report.Notices, fmt.Sprintf("已为 %d 条历史订单关联订阅", res.RowsAffected))
		return nil
	}); err != nil {
		return err
	}

	// 6. 历史流量统计：原 user_id 即节点上报的用户 ID，等于主订阅 ID。
	// 表可能很大，在事务外分批更新以避免长时间锁表；中断后重新执行会从未回填的记录继续。
	batch := "UPDATE v2_stat_user SET subscription_id = user_id WHERE subscription_id IS NULL LIMIT 20000"
	if m.driver == config.DriverPostgres {
		batch = "UPDATE v2_stat_user SET subscription_id = user_id WHERE id IN (SELECT id FROM v2_stat_user WHERE subscription_id IS NULL LIMIT 20000)"
	}
	var total int64
	for {
		res := db.Exec(batch)
		if res.Error != nil {
			return fmt.Errorf("回填流量统计订阅 ID 失败: %w", res.Error)
		}
		total += res.RowsAffected
		if res.RowsAffected == 0 {
			break
		}
	}
	m.report.Notices = append(m.report.Notices, fmt.Sprintf("已为 %d 条流量统计记录关联订阅", total))
	return nil
}
