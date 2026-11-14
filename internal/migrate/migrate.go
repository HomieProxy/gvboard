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

// Step 只执行一次的迁移步骤，执行后记录到 v2_gvb_migration。
type Step struct {
	ID          string
	Description string
	Run         func(ctx context.Context, m *Migrator) error
}

// Report 迁移结果。
type Report struct {
	CreatedTables  []string
	AddedColumns   []string
	CreatedIndexes []string
	AppliedSteps   []string
	Notices        []string
}

// Migrator 迁移执行器。
type Migrator struct {
	db     *gorm.DB
	driver string
	report *Report
	steps  []Step
}

const migrationTable = "v2_gvb_migration"

func New(db *gorm.DB, driver string) *Migrator {
	return &Migrator{db: db, driver: driver, steps: Steps()}
}

func (m *Migrator) q(ident string) string { return quote(m.driver, ident) }

// Run 执行全部迁移：补齐表结构，然后依次执行未执行过的迁移步骤。
func (m *Migrator) Run(ctx context.Context) (*Report, error) {
	m.report = &Report{}
	db := m.db.WithContext(ctx)

	if err := m.checkForeignSettings(db); err != nil {
		return m.report, err
	}
	if err := m.ensureMigrationTable(db); err != nil {
		return m.report, err
	}
	for _, t := range Schema() {
		if err := m.ensureTable(db, t); err != nil {
			return m.report, err
		}
	}

	applied, err := m.appliedSteps(db)
	if err != nil {
		return m.report, err
	}
	for _, s := range m.steps {
		if applied[s.ID] {
			continue
		}
		if err := s.Run(ctx, m); err != nil {
			return m.report, fmt.Errorf("迁移步骤 %s（%s）失败: %w", s.ID, s.Description, err)
		}
		if err := db.Exec(fmt.Sprintf("INSERT INTO %s (%s, %s) VALUES (?, ?)",
			m.q(migrationTable), m.q("id"), m.q("applied_at")), s.ID, time.Now().Unix()).Error; err != nil {
			return m.report, err
		}
		m.report.AppliedSteps = append(m.report.AppliedSteps, s.ID+" "+s.Description)
	}
	return m.report, nil
}

// Pending 返回尚未执行的迁移步骤（迁移记录表不存在时视为全部未执行）。
func (m *Migrator) Pending(ctx context.Context) ([]Step, error) {
	db := m.db.WithContext(ctx)
	ok, err := m.tableExists(db, migrationTable)
	if err != nil {
		return nil, err
	}
	applied := map[string]bool{}
	if ok {
		if applied, err = m.appliedSteps(db); err != nil {
			return nil, err
		}
	}
	var out []Step
	for _, s := range m.steps {
		if !applied[s.ID] {
			out = append(out, s)
		}
	}
	return out, nil
}

func (m *Migrator) ensureMigrationTable(db *gorm.DB) error {
	var ddl string
	if m.driver == config.DriverPostgres {
		ddl = `CREATE TABLE IF NOT EXISTS "v2_gvb_migration" ("id" varchar(128) PRIMARY KEY, "applied_at" integer NOT NULL)`
	} else {
		ddl = "CREATE TABLE IF NOT EXISTS `v2_gvb_migration` (`id` varchar(128) NOT NULL, `applied_at` int NOT NULL, PRIMARY KEY (`id`)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4"
	}
	return db.Exec(ddl).Error
}

func (m *Migrator) appliedSteps(db *gorm.DB) (map[string]bool, error) {
	var ids []string
	if err := db.Raw(fmt.Sprintf("SELECT %s FROM %s", m.q("id"), m.q(migrationTable))).Scan(&ids).Error; err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

func (m *Migrator) schemaExpr() string {
	if m.driver == config.DriverPostgres {
		return "current_schema()"
	}
	return "DATABASE()"
}

func (m *Migrator) tableExists(db *gorm.DB, name string) (bool, error) {
	var n int64
	err := db.Raw("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = "+m.schemaExpr()+" AND table_name = ?", name).Scan(&n).Error
	return n > 0, err
}

func (m *Migrator) columns(db *gorm.DB, tableName string) (map[string]bool, error) {
	var names []string
	err := db.Raw("SELECT column_name FROM information_schema.columns WHERE table_schema = "+m.schemaExpr()+" AND table_name = ?", tableName).Scan(&names).Error
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[strings.ToLower(n)] = true
	}
	return out, nil
}

func (m *Migrator) indexExists(db *gorm.DB, tableName, name string) (bool, error) {
	var n int64
	var err error
	if m.driver == config.DriverPostgres {
		err = db.Raw("SELECT COUNT(*) FROM pg_indexes WHERE schemaname = current_schema() AND tablename = ? AND indexname = ?",
			tableName, indexName(m.driver, tableName, name)).Scan(&n).Error
	} else {
		err = db.Raw("SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = ? AND index_name = ?",
			tableName, name).Scan(&n).Error
	}
	return n > 0, err
}

func (m *Migrator) ensureTable(db *gorm.DB, t Table) error {
	exists, err := m.tableExists(db, t.Name)
	if err != nil {
		return err
	}
	if !exists {
		stmts, err := CreateTableSQL(m.driver, t)
		if err != nil {
			return err
		}
		for _, s := range stmts {
			if err := db.Exec(s).Error; err != nil {
				return fmt.Errorf("创建表 %s 失败: %w", t.Name, err)
			}
		}
		m.report.CreatedTables = append(m.report.CreatedTables, t.Name)
		return nil
	}

	existing, err := m.columns(db, t.Name)
	if err != nil {
		return err
	}
	for _, c := range t.Columns {
		if existing[strings.ToLower(c.Name)] {
			continue
		}
		s, err := AddColumnSQL(m.driver, t.Name, c)
		if err != nil {
			return err
		}
		if err := db.Exec(s).Error; err != nil {
			return fmt.Errorf("为表 %s 添加列 %s 失败: %w", t.Name, c.Name, err)
		}
		m.report.AddedColumns = append(m.report.AddedColumns, t.Name+"."+c.Name)
	}
	for _, i := range t.Indexes {
		if !i.Ensure {
			continue
		}
		ok, err := m.indexExists(db, t.Name, i.Name)
		if err != nil {
			return err
		}
		if ok {
			continue
		}
		if err := db.Exec(CreateIndexSQL(m.driver, t.Name, i)).Error; err != nil {
			return fmt.Errorf("为表 %s 创建索引 %s 失败: %w", t.Name, i.Name, err)
		}
		m.report.CreatedIndexes = append(m.report.CreatedIndexes, t.Name+"."+i.Name)
	}
	return nil
}

func (m *Migrator) dropIndexIfExists(db *gorm.DB, tableName, name string) (bool, error) {
	ok, err := m.indexExists(db, tableName, name)
	if err != nil || !ok {
		return false, err
	}
	var s string
	if m.driver == config.DriverPostgres {
		s = fmt.Sprintf("DROP INDEX IF EXISTS %s", m.q(indexName(m.driver, tableName, name)))
	} else {
		s = fmt.Sprintf("ALTER TABLE %s DROP INDEX %s", m.q(tableName), m.q(name))
	}
	return true, db.Exec(s).Error
}

// ErrForeignSettings 库中已有一张不是 Gvboard 创建的 v2_settings 表（Xboard 的同名表）。
var ErrForeignSettings = errors.New("数据库中已有其它程序（Xboard）创建的 v2_settings 表")

// checkForeignSettings Xboard 的 v2_settings 带 group / type 列，键名与取值格式都与 Gvboard 不同，
// 直接复用会把其中的设置当作 Gvboard 设置读取。
func (m *Migrator) checkForeignSettings(db *gorm.DB) error {
	ok, err := m.tableExists(db, "v2_settings")
	if err != nil || !ok {
		return err
	}
	cols, err := m.columns(db, "v2_settings")
	if err != nil {
		return err
	}
	if !cols["group"] && !cols["type"] {
		return nil
	}
	rename := "RENAME TABLE `v2_settings` TO `v2_settings_xboard_bak`"
	if m.driver == config.DriverPostgres {
		rename = `ALTER TABLE "v2_settings" RENAME TO "v2_settings_xboard_bak"`
	}
	return fmt.Errorf("%w（含 group / type 列）。Gvboard 不使用这张表，请先改名保留：\n  %s\n然后重新执行 gvboard migrate；原面板的设置请用 gvboard import-config 导入", ErrForeignSettings, rename)
}

// ErrDuplicateUUID 原用户表中存在重复 uuid，无法迁移为全局唯一的订阅 uuid。
var ErrDuplicateUUID = errors.New("原用户表中存在重复的 uuid")
