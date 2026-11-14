package adminuser

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"gvboard/internal/account"
	"gvboard/internal/database"
	"gvboard/internal/model"
	"gvboard/internal/util"
)

const timeLayout = "2006-01-02 15:04:05"

// csvCell 以 = + - @ 等开头的内容在表格软件中会被当作公式执行，加单引号前缀。
func csvCell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

func newCSV(header ...string) (*bytes.Buffer, *csv.Writer) {
	buf := &bytes.Buffer{}
	w := csv.NewWriter(buf)
	w.UseCRLF = true
	_ = w.Write(header)
	return buf, w
}

func writeRow(w *csv.Writer, cells ...string) {
	for i := range cells {
		cells[i] = csvCell(cells[i])
	}
	_ = w.Write(cells)
}

func expireText(exp *int64) string {
	if exp == nil {
		return "长期有效"
	}
	return time.Unix(*exp, 0).Format(timeLayout)
}

func gbText(b int64) string { return strconv.FormatFloat(float64(b)/float64(gb), 'f', -1, 64) }

func yuan(cents int64) string { return strconv.FormatFloat(float64(cents)/100, 'f', -1, 64) }

// Export 按筛选条件导出 CSV（对应原项目 admin/user/dumpCSV），每个订阅一行（决策 D53）。
func (s *Service) Export(ctx context.Context, filters []Filter, baseURL string) ([]byte, error) {
	q, err := s.filtered(ctx, filters)
	if err != nil {
		return nil, err
	}
	names, err := planNames(s.DB.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	buf, w := newCSV("邮箱", "余额", "推广佣金", "订阅 ID", "订阅名称", "主订阅", "总流量", "设备数限制", "剩余流量", "套餐到期时间", "订阅计划", "订阅地址")
	lastID := int64(0)
	for {
		var users []model.User
		err := q.Session(&gorm.Session{}).Select("id", "email", "balance", "commission_balance").
			Where("id > ?", lastID).Order("id").Limit(batchSize).Find(&users).Error
		if err != nil {
			return nil, err
		}
		ids := make([]int64, len(users))
		for i := range users {
			ids[i] = users[i].ID
		}
		var subs []model.Subscription
		if len(ids) > 0 {
			if err := s.DB.WithContext(ctx).Where("user_id IN ?", ids).Order("is_primary DESC").Order("id").Find(&subs).Error; err != nil {
				return nil, err
			}
		}
		byUser := map[int64][]model.Subscription{}
		for _, sub := range subs {
			byUser[sub.UserID] = append(byUser[sub.UserID], sub)
		}
		for _, u := range users {
			for i := range byUser[u.ID] {
				sub := &byUser[u.ID][i]
				planName, device, primary := "无订阅", "", "否"
				if sub.PlanID != nil {
					planName = names[*sub.PlanID]
				}
				if sub.DeviceLimit != nil {
					device = strconv.FormatInt(*sub.DeviceLimit, 10)
				}
				if sub.IsPrimary == 1 {
					primary = "是"
				}
				link, err := s.Subscriptions.Linker.URL(ctx, sub, baseURL)
				if err != nil {
					return nil, err
				}
				writeRow(w, u.Email, yuan(u.Balance), yuan(u.CommissionBalance), strconv.FormatInt(sub.ID, 10), sub.Name, primary,
					gbText(sub.TransferEnable), device, gbText(sub.TransferEnable-sub.U-sub.D), expireText(sub.ExpiredAt), planName, link)
			}
		}
		if len(users) < batchSize {
			break
		}
		lastID = users[len(users)-1].ID
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

// GenerateInput 生成用户参数（对应原项目 admin/user/generate）。
// EmailPrefix 不为空时生成单个用户；否则按 GenerateCount 批量生成随机前缀的用户。
// Password 为空时密码与邮箱相同；PlanID 为空或 0 时不开通套餐。
type GenerateInput struct {
	EmailPrefix   string `json:"email_prefix"`
	EmailSuffix   string `json:"email_suffix"`
	Password      string `json:"password"`
	PlanID        int64  `json:"plan_id"`
	ExpiredAt     *int64 `json:"expired_at"`
	GenerateCount int    `json:"generate_count"`
}

// Generate 生成用户。单个生成时返回 nil；批量生成时返回 CSV（账号、密码、到期时间、UUID、创建时间、订阅地址）。
// 每个用户带一个主订阅（决策 D53）。
func (s *Service) Generate(ctx context.Context, in GenerateInput, baseURL string) ([]byte, error) {
	in.EmailPrefix, in.EmailSuffix = strings.TrimSpace(in.EmailPrefix), strings.TrimSpace(in.EmailSuffix)
	if in.EmailSuffix == "" {
		return nil, errInvalid
	}
	var plan *model.Plan
	if in.PlanID > 0 {
		var p model.Plan
		err := s.DB.WithContext(ctx).Take(&p, in.PlanID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errPlanNotExist
		}
		if err != nil {
			return nil, err
		}
		plan = &p
	}
	if in.EmailPrefix != "" {
		email := in.EmailPrefix + "@" + in.EmailSuffix
		if !util.EmailValid(email, false) {
			return nil, errEmailFormat
		}
		return nil, s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var n int64
			if err := database.WhereEmail(tx.Model(&model.User{}), email).Count(&n).Error; err != nil {
				return err
			}
			if n > 0 {
				return errEmailUsed
			}
			pw := in.Password
			if pw == "" {
				pw = email
			}
			_, _, err := account.Create(tx, account.NewUser{Email: email, Password: pw, Plan: plan, ExpiredAt: in.ExpiredAt})
			return err
		})
	}
	if in.GenerateCount < 1 || in.GenerateCount > 500 {
		return nil, errGenerateCount
	}
	emails, err := s.randomEmails(ctx, in.EmailSuffix, in.GenerateCount)
	if err != nil {
		return nil, err
	}
	passwords := make([]string, len(emails))
	for i, e := range emails {
		passwords[i] = in.Password
		if passwords[i] == "" {
			passwords[i] = e
		}
	}
	hashes, err := hashAll(passwords)
	if err != nil {
		return nil, err
	}
	type created struct {
		user *model.User
		sub  *model.Subscription
	}
	rows := make([]created, 0, len(emails))
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i, e := range emails {
			u, sub, err := account.Create(tx, account.NewUser{Email: e, PasswordHash: hashes[i], Plan: plan, ExpiredAt: in.ExpiredAt})
			if err != nil {
				return err
			}
			rows = append(rows, created{u, sub})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	buf, w := newCSV("账号", "密码", "过期时间", "UUID", "创建时间", "订阅地址")
	for i, r := range rows {
		link, err := s.Subscriptions.Linker.URL(ctx, r.sub, baseURL)
		if err != nil {
			return nil, err
		}
		writeRow(w, r.user.Email, passwords[i], expireText(r.sub.ExpiredAt), r.sub.UUID, time.Unix(r.user.CreatedAt, 0).Format(timeLayout), link)
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

// randomEmails 生成 n 个不重复且未注册的随机邮箱（前缀 6 位，与原项目一致）。
func (s *Service) randomEmails(ctx context.Context, suffix string, n int) ([]string, error) {
	if !util.EmailValid("a@"+suffix, false) {
		return nil, errEmailFormat
	}
	seen := map[string]bool{}
	out := make([]string, 0, n)
	for attempt := 0; len(out) < n && attempt < 5; attempt++ {
		var batch []string
		for len(out)+len(batch) < n {
			e := strings.ToLower(util.RandomChar(6, false)) + "@" + suffix
			if !seen[e] {
				seen[e] = true
				batch = append(batch, e)
			}
		}
		var taken []string
		if err := s.DB.WithContext(ctx).Model(&model.User{}).Where("LOWER(email) IN ?", batch).Pluck("LOWER(email)", &taken).Error; err != nil {
			return nil, err
		}
		used := map[string]bool{}
		for _, t := range taken {
			used[t] = true
		}
		for _, e := range batch {
			if !used[e] {
				out = append(out, e)
			}
		}
	}
	if len(out) < n {
		return nil, errEmailUsed
	}
	return out, nil
}

// hashAll 并行计算 bcrypt（批量生成 500 个用户时串行需要数十秒）。
func hashAll(passwords []string) ([]string, error) {
	out := make([]string, len(passwords))
	errs := make([]error, len(passwords))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range max(runtime.NumCPU(), 1) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				out[i], errs[i] = util.HashPassword(passwords[i])
			}
		}()
	}
	for i := range passwords {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return out, errors.Join(errs...)
}
