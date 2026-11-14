package usercenter

import (
	"context"
	"encoding/csv"
	"strings"
	"testing"

	"gvboard/internal/model"
	"gvboard/internal/testutil"
)

func TestGiftcardAdmin(t *testing.T) {
	drivers := testutil.Drivers()
	if len(drivers) == 0 {
		t.Skip("未配置测试数据库，跳过")
	}
	for _, driver := range drivers {
		t.Run(driver, func(t *testing.T) { testGiftcardAdmin(t, newSvc(t, driver)) })
	}
}

func testGiftcardAdmin(t *testing.T, s *Service) {
	ctx := context.Background()
	p := &model.Plan{Name: "P", GroupID: 1, TransferEnable: 10, Show: 1, Renew: 1}
	if err := s.DB.Create(p).Error; err != nil {
		t.Fatal(err)
	}

	for _, in := range []GiftcardInput{
		{Name: "", Type: GiftcardBalance, Value: ptr(int64(1))},
		{Name: "x", Type: 0, Value: ptr(int64(1))},
		{Name: "x", Type: 6, Value: ptr(int64(1))},
		{Name: "x", Type: GiftcardBalance},
		{Name: "x", Type: GiftcardBalance, Value: ptr(int64(-1))},
		{Name: "x", Type: GiftcardBalance, Value: ptr(int64(1)), StartedAt: 10, EndedAt: 5},
		{Name: "x", Type: GiftcardBalance, Value: ptr(int64(1)), LimitUse: ptr(int64(-1))},
		{Name: "x", Type: GiftcardPlan, Value: ptr(int64(30))},
	} {
		wantErr(t, s.SaveGiftcard(ctx, in), errGiftcardInvalid)
	}
	wantErr(t, s.SaveGiftcard(ctx, GiftcardInput{Name: "x", Type: GiftcardPlan, Value: ptr(int64(30)), PlanID: ptr(int64(9999))}), errGiftcardPlanNone)

	// 重置流量类不需要数值；非套餐类忽略 plan_id；未填卡密时随机生成
	if err := s.SaveGiftcard(ctx, GiftcardInput{Name: "重置", Type: GiftcardResetTraffic, PlanID: &p.ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveGiftcard(ctx, GiftcardInput{Name: "余额", Code: "FIXEDCODE", Type: GiftcardBalance, Value: ptr(int64(500))}); err != nil {
		t.Fatal(err)
	}
	var reset, fixed model.Giftcard
	s.DB.Where("name = ?", "重置").Take(&reset)
	s.DB.Where("code = ?", "FIXEDCODE").Take(&fixed)
	if len(reset.Code) != 16 || reset.PlanID != nil || reset.Value != nil || fixed.Value == nil || *fixed.Value != 500 {
		t.Fatalf("新建礼品卡错误：%+v %+v", reset, fixed)
	}
	wantErr(t, s.SaveGiftcard(ctx, GiftcardInput{Name: "y", Code: "FIXEDCODE", Type: GiftcardBalance, Value: ptr(int64(1))}), errGiftcardDup)
	wantErr(t, s.SaveGiftcard(ctx, GiftcardInput{ID: reset.ID, Name: "y", Code: "FIXEDCODE", Type: GiftcardResetTraffic}), errGiftcardDup)
	wantErr(t, s.SaveGiftcard(ctx, GiftcardInput{ID: 99999, Name: "y", Type: GiftcardResetTraffic}), errGiftcardNone)

	// 修改：未填卡密时保留原卡密，可清空可用次数
	s.DB.Model(&fixed).Update("limit_use", 3)
	if err := s.SaveGiftcard(ctx, GiftcardInput{ID: fixed.ID, Name: "套餐卡", Type: GiftcardPlan, Value: ptr(int64(30)), PlanID: &p.ID}); err != nil {
		t.Fatal(err)
	}
	var got model.Giftcard
	s.DB.Take(&got, fixed.ID)
	if got.Code != "FIXEDCODE" || got.Type != GiftcardPlan || got.PlanID == nil || *got.PlanID != p.ID || got.LimitUse != nil || got.Name != "套餐卡" {
		t.Fatalf("修改礼品卡错误：%+v", got)
	}

	// 批量生成
	for _, n := range []int{0, 501} {
		if _, err := s.GenerateGiftcards(ctx, GiftcardInput{Name: "b", Type: GiftcardBalance, Value: ptr(int64(1))}, n); err != errGiftcardCount {
			t.Fatalf("数量 %d 应报错", n)
		}
	}
	b, err := s.GenerateGiftcards(ctx, GiftcardInput{Name: "=批量", Type: GiftcardBalance, Value: ptr(int64(1050)), LimitUse: ptr(int64(2))}, 30)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(b))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, r := range rows[1:] {
		codes[r[6]] = true
	}
	if len(rows) != 31 || rows[1][0] != "'=批量" || rows[1][2] != "10.5" || rows[1][3] != "不限制" || rows[1][5] != "2" || len(codes) != 30 {
		t.Fatalf("批量生成 CSV 错误：%v", rows[:2])
	}

	// 列表：排序白名单，分页最少 10 条
	list, err := s.AdminGiftcards(ctx, GiftcardPage{Sort: "id; DROP TABLE v2_giftcard", SortType: "ASC", PageSize: 5})
	if err != nil || list.Total != 32 || len(list.Data) != 10 || list.Data[0].ID != reset.ID {
		t.Fatalf("列表错误：%v %v", list, err)
	}
	list, _ = s.AdminGiftcards(ctx, GiftcardPage{Sort: "value", Current: 4})
	if len(list.Data) != 2 {
		t.Fatalf("第 4 页应有 2 条，实际 %d", len(list.Data))
	}

	if err := s.DeleteGiftcard(ctx, fixed.ID); err != nil {
		t.Fatal(err)
	}
	wantErr(t, s.DeleteGiftcard(ctx, fixed.ID), errGiftcardNone)
}
