package balance

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"gorm.io/gorm"

	"gvboard/internal/model"
	"gvboard/internal/testutil"
)

func TestApply(t *testing.T) {
	drivers := testutil.Drivers()
	if len(drivers) == 0 {
		t.Skip("未配置测试数据库，跳过")
	}
	for _, driver := range drivers {
		t.Run(driver, func(t *testing.T) {
			db := testutil.DB(t, driver)
			u := model.User{Email: "b@x.com", Password: "x", Token: "t", UUID: "u", Balance: 100}
			if err := db.Create(&u).Error; err != nil {
				t.Fatal(err)
			}
			logs := func() []model.BalanceLog {
				var rows []model.BalanceLog
				db.Where("user_id = ?", u.ID).Order("id").Find(&rows)
				return rows
			}
			apply := func(c Change) (int64, error) {
				var after int64
				err := db.Transaction(func(tx *gorm.DB) error {
					var err error
					after, err = Apply(tx, c)
					return err
				})
				return after, err
			}

			after, err := apply(Change{UserID: u.ID, Amount: 50, Type: TypeAdmin, OperatorID: 7, Remark: strings.Repeat("备", 300)})
			if err != nil || after != 150 {
				t.Fatalf("增加余额: after=%d err=%v", after, err)
			}
			l := logs()
			if len(l) != 1 || l[0].Amount != 50 || l[0].Balance != 150 || l[0].Type != TypeAdmin || l[0].OperatorID == nil ||
				*l[0].OperatorID != 7 || len([]rune(l[0].Remark)) != RemarkMax || l[0].CreatedAt == 0 {
				t.Fatalf("增加余额的日志不正确: %+v", l)
			}

			if _, err := apply(Change{UserID: u.ID, Amount: -151, Type: TypeOrderPay}); !errors.Is(err, ErrInsufficient) {
				t.Fatalf("余额不足应返回 ErrInsufficient，实际 %v", err)
			}
			if after, err := apply(Change{UserID: u.ID, Amount: 0, Type: TypeOrderPay}); err != nil || after != 150 {
				t.Fatalf("金额为 0 应返回当前余额: after=%d err=%v", after, err)
			}
			if _, err := apply(Change{UserID: u.ID + 1000, Amount: 1, Type: TypeGiftcard}); !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Fatalf("用户不存在应返回 ErrRecordNotFound，实际 %v", err)
			}
			if n := len(logs()); n != 1 {
				t.Fatalf("失败与金额为 0 的变动不应记录，实际 %d 条", n)
			}

			// 并发扣减：150 分每次扣 10 分，20 个请求只有 15 个成功，余额不会为负，日志与成功次数一致
			var wg sync.WaitGroup
			var mu sync.Mutex
			ok := 0
			for range 20 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if _, err := apply(Change{UserID: u.ID, Amount: -10, Type: TypePlugin, Plugin: "checkin"}); err == nil {
						mu.Lock()
						ok++
						mu.Unlock()
					} else if !errors.Is(err, ErrInsufficient) {
						t.Error(err)
					}
				}()
			}
			wg.Wait()
			var cur model.User
			db.Select("balance").Take(&cur, u.ID)
			l = logs()
			if ok != 15 || cur.Balance != 0 || len(l) != 16 {
				t.Fatalf("并发扣减结果不正确: 成功 %d 次，余额 %d，日志 %d 条", ok, cur.Balance, len(l))
			}
			seen := map[int64]bool{}
			for _, r := range l[1:] {
				if r.Amount != -10 || r.Plugin != "checkin" || seen[r.Balance] {
					t.Fatalf("并发扣减的日志不正确: %+v", r)
				}
				seen[r.Balance] = true
			}
		})
	}
}
