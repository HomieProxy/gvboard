package adminuser

import (
	"context"
	"slices"
	"testing"

	"gvboard/internal/model"
)

func TestEmptyUsers(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		p := e.plan(t, "P")
		empty, emptySub := e.user(t, "empty@x.com")
		empty2, _ := e.user(t, "empty2@x.com")
		logged, _ := e.user(t, "logged@x.com")
		e.DB.Model(logged).Update("last_login_at", 1700000000)
		_, planSub := e.user(t, "plan@x.com")
		e.DB.Model(planSub).Update("plan_id", p.ID)
		extra, _ := e.user(t, "extra@x.com")
		e.DB.Create(&model.Subscription{UserID: extra.ID, Name: "s", UUID: "uuid-empty-x", Token: "token-empty-x", TransferEnable: 1})
		_, forever := e.user(t, "forever@x.com")
		e.DB.Model(forever).Update("expired_at", nil)
		rich, _ := e.user(t, "rich@x.com")
		e.DB.Model(rich).Update("commission_balance", 1)
		admin, _ := e.user(t, "admin@x.com")
		e.DB.Model(admin).Update("is_admin", 1)
		buyer, _ := e.user(t, "buyer@x.com")
		e.DB.Create(&model.Order{UserID: buyer.ID, PlanID: p.ID, Period: "month_price", TradeNo: "EMPTY1", Status: model.OrderStatusCancelled})

		ids, err := e.EmptyUserIDs(ctx)
		if err != nil || !slices.Equal(ids, []int64{empty.ID, empty2.ID}) {
			t.Fatalf("空用户错误：%v %v", ids, err)
		}
		n, err := e.DeleteByIDs(ctx, ids)
		if err != nil || n != 2 {
			t.Fatalf("删除错误：%d %v", n, err)
		}
		var users, subs int64
		e.DB.Model(&model.User{}).Where("id IN ?", ids).Count(&users)
		e.DB.Model(&model.Subscription{}).Where("id = ?", emptySub.ID).Count(&subs)
		if users != 0 || subs != 0 {
			t.Fatalf("用户与订阅应已删除：%d %d", users, subs)
		}
		if ids, _ = e.EmptyUserIDs(ctx); len(ids) != 0 {
			t.Fatalf("删除后不应再有空用户：%v", ids)
		}
	})
}
