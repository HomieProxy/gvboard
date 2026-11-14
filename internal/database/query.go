package database

import "gorm.io/gorm"

// IsPostgres 判断当前连接是否为 PostgreSQL。
func IsPostgres(db *gorm.DB) bool { return db.Dialector.Name() == "postgres" }

// WhereEmail 按邮箱查询，不区分大小写（决策 D14）。
// MySQL 的排序规则本身不区分大小写，直接比较以使用原有索引；PostgreSQL 按小写比较，对应小写邮箱索引。
func WhereEmail(db *gorm.DB, email string) *gorm.DB {
	if IsPostgres(db) {
		return db.Where("lower(email) = lower(?)", email)
	}
	return db.Where("email = ?", email)
}
