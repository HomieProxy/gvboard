package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"gvboard/internal/config"
)

// ConvertOptions MySQL → PostgreSQL 数据转换选项。
type ConvertOptions struct {
	// SkipTables 不复制数据的表（表结构仍会创建）。
	SkipTables map[string]bool
	// BatchSize 每批读取 / 写入的行数，默认 1000。
	BatchSize int
	// Progress 每张表复制完成后调用。
	Progress func(table string, rows int64)
}

// ConvertReport 转换结果。
type ConvertReport struct {
	Tables         []ConvertedTable
	Skipped        []string // 按选项跳过数据的表
	IgnoredTables  []string // 源库中不属于 Gvboard 的 v2_ 表（不复制）
	IgnoredColumns []string // 源库中多出的列（不复制）
	Notices        []string
}

// ConvertedTable 单张表的复制结果。
type ConvertedTable struct {
	Name string
	Rows int64
}

var (
	ErrSourcePending  = errors.New("源数据库还有未执行的迁移步骤")
	ErrTargetNotEmpty = errors.New("目标数据库不是空库")
)

// Convert 把已执行过 gvboard migrate 的 MySQL 库全量复制到 PostgreSQL 空库。
//
// 源库在一个只读的一致性快照事务中读取，目标库的全部写入在一个事务中完成，失败时目标库不会留下数据，可直接重试。
func Convert(ctx context.Context, src, dst *gorm.DB, opt ConvertOptions) (*ConvertReport, error) {
	if opt.BatchSize <= 0 {
		opt.BatchSize = 1000
	}
	rep := &ConvertReport{}
	srcM, dstM := New(src, config.DriverMySQL), New(dst, config.DriverPostgres)

	pending, err := srcM.Pending(ctx)
	if err != nil {
		return rep, err
	}
	if len(pending) > 0 {
		return rep, fmt.Errorf("%w（%d 个），请先对源库执行 gvboard migrate", ErrSourcePending, len(pending))
	}
	if err := checkEmpty(ctx, dstM); err != nil {
		return rep, err
	}
	if _, err := dstM.Run(ctx); err != nil {
		return rep, fmt.Errorf("创建目标库表结构失败: %w", err)
	}

	tables := Schema()
	known := map[string]bool{migrationTable: true}
	for _, t := range tables {
		known[t.Name] = true
	}
	var srcTables []string
	if err := src.WithContext(ctx).Raw("SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name LIKE 'v2\\_%'").
		Scan(&srcTables).Error; err != nil {
		return rep, err
	}
	for _, n := range srcTables {
		if !known[n] {
			rep.IgnoredTables = append(rep.IgnoredTables, n)
		}
	}
	sort.Strings(rep.IgnoredTables)

	readOnly := &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	err = src.WithContext(ctx).Transaction(func(stx *gorm.DB) error {
		return dst.WithContext(ctx).Transaction(func(dtx *gorm.DB) error {
			c := &converter{ctx: ctx, src: stx, dst: dtx, srcM: srcM, batch: opt.BatchSize, rep: rep}
			for _, t := range tables {
				if opt.SkipTables[t.Name] {
					rep.Skipped = append(rep.Skipped, t.Name)
					continue
				}
				n, err := c.copyTable(t)
				if err != nil {
					return fmt.Errorf("复制表 %s 失败: %w", t.Name, err)
				}
				rep.Tables = append(rep.Tables, ConvertedTable{Name: t.Name, Rows: n})
				if opt.Progress != nil {
					opt.Progress(t.Name, n)
				}
			}
			for _, t := range tables {
				if err := dtx.Exec(fmt.Sprintf(`SELECT setval(pg_get_serial_sequence('%s', 'id'), COALESCE((SELECT MAX("id") FROM "%s"), 0) + 1, false)`,
					t.Name, t.Name)).Error; err != nil {
					return fmt.Errorf("同步表 %s 的自增序列失败: %w", t.Name, err)
				}
			}
			if c.nulStripped > 0 {
				rep.Notices = append(rep.Notices, fmt.Sprintf("PostgreSQL 文本不能包含空字符（\\0），已从 %d 个值中移除", c.nulStripped))
			}
			if c.invalidUTF8 > 0 {
				rep.Notices = append(rep.Notices, fmt.Sprintf("已替换 %d 个值中的无效 UTF-8 字节", c.invalidUTF8))
			}
			return nil
		})
	}, readOnly)
	return rep, err
}

// checkEmpty 目标库中 Gvboard 的表必须都不存在或没有数据（上次转换失败时表结构已创建但没有数据，可以重试）。
func checkEmpty(ctx context.Context, m *Migrator) error {
	db := m.db.WithContext(ctx)
	for _, t := range Schema() {
		ok, err := m.tableExists(db, t.Name)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		var n int64
		if err := db.Raw(fmt.Sprintf(`SELECT COUNT(*) FROM "%s"`, t.Name)).Scan(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return fmt.Errorf("%w：表 %s 已有 %d 行数据，请使用新建的空数据库", ErrTargetNotEmpty, t.Name, n)
		}
	}
	return nil
}

type converter struct {
	ctx         context.Context
	src, dst    *gorm.DB
	srcM        *Migrator
	batch       int
	rep         *ConvertReport
	nulStripped int
	invalidUTF8 int
}

func (c *converter) copyTable(t Table) (int64, error) {
	srcCols, err := c.srcM.columns(c.src, t.Name)
	if err != nil {
		return 0, err
	}
	if len(srcCols) == 0 {
		return 0, errors.New("源库中没有这张表")
	}
	var names []string
	want := map[string]bool{}
	for _, col := range t.Columns {
		want[strings.ToLower(col.Name)] = true
		if srcCols[strings.ToLower(col.Name)] {
			names = append(names, col.Name)
		}
	}
	var extra []string
	for n := range srcCols {
		if !want[n] {
			extra = append(extra, t.Name+"."+n)
		}
	}
	sort.Strings(extra)
	c.rep.IgnoredColumns = append(c.rep.IgnoredColumns, extra...)

	var total int64
	if err := c.src.Raw(fmt.Sprintf("SELECT COUNT(*) FROM `%s`", t.Name)).Scan(&total).Error; err != nil {
		return 0, err
	}

	// PostgreSQL 单条语句最多 65535 个参数
	batch := min(c.batch, 60000/len(names))
	srcQ := make([]string, len(names))
	dstQ := make([]string, len(names))
	idPos := -1
	for i, n := range names {
		srcQ[i] = "`" + n + "`"
		dstQ[i] = `"` + n + `"`
		if n == "id" {
			idPos = i
		}
	}
	if idPos < 0 {
		return 0, errors.New("缺少 id 列")
	}
	selectSQL := fmt.Sprintf("SELECT %s FROM `%s` WHERE `id` > ? ORDER BY `id` LIMIT %d", strings.Join(srcQ, ", "), t.Name, batch)
	insertHead := fmt.Sprintf(`INSERT INTO "%s" (%s) VALUES `, t.Name, strings.Join(dstQ, ", "))

	// 直接使用连接执行，避免 ORM 日志记录行数据
	srcPool, dstPool := c.src.Statement.ConnPool, c.dst.Statement.ConnPool
	var copied, last int64
	for {
		rows, err := srcPool.QueryContext(c.ctx, selectSQL, last)
		if err != nil {
			return copied, err
		}
		var args []any
		n := 0
		for rows.Next() {
			vals := make([]any, len(names))
			ptrs := make([]any, len(names))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				rows.Close()
				return copied, err
			}
			for i, v := range vals {
				s, err := c.value(v)
				if err != nil {
					rows.Close()
					return copied, fmt.Errorf("列 %s: %w", names[i], err)
				}
				vals[i] = s
			}
			id, err := strconv.ParseInt(fmt.Sprint(vals[idPos]), 10, 64)
			if err != nil {
				rows.Close()
				return copied, fmt.Errorf("无效的 id: %w", err)
			}
			last = id
			args = append(args, vals...)
			n++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return copied, err
		}
		rows.Close()
		if n == 0 {
			break
		}
		var b strings.Builder
		b.WriteString(insertHead)
		p := 1
		for r := 0; r < n; r++ {
			if r > 0 {
				b.WriteString(", ")
			}
			b.WriteByte('(')
			for i := range names {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString("$" + strconv.Itoa(p))
				p++
			}
			b.WriteByte(')')
		}
		if _, err := dstPool.ExecContext(c.ctx, b.String(), args...); err != nil {
			return copied, err
		}
		copied += int64(n)
		if n < batch {
			break
		}
	}

	var got int64
	if err := c.dst.Raw(fmt.Sprintf(`SELECT COUNT(*) FROM "%s"`, t.Name)).Scan(&got).Error; err != nil {
		return copied, err
	}
	if got != total || copied != total {
		return copied, fmt.Errorf("行数不一致：源库 %d，已复制 %d，目标库 %d", total, copied, got)
	}
	return copied, nil
}

// value 统一转为字符串（NULL 保持 nil），由 PostgreSQL 按列类型解析，避免各类型的驱动编码差异。
func (c *converter) value(v any) (any, error) {
	var s string
	switch x := v.(type) {
	case nil:
		return nil, nil
	case []byte:
		s = string(x)
	case string:
		s = x
	case int64:
		return strconv.FormatInt(x, 10), nil
	case uint64:
		return strconv.FormatUint(x, 10), nil
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	case float32:
		return strconv.FormatFloat(float64(x), 'f', -1, 32), nil
	case bool:
		if x {
			return "1", nil
		}
		return "0", nil
	case time.Time:
		return nil, fmt.Errorf("不支持的日期时间值")
	default:
		return nil, fmt.Errorf("不支持的值类型 %T", v)
	}
	if strings.IndexByte(s, 0) >= 0 {
		s = strings.ReplaceAll(s, "\x00", "")
		c.nulStripped++
	}
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "\uFFFD")
		c.invalidUTF8++
	}
	return s, nil
}
