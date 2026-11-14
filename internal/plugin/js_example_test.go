package plugin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"gvboard/internal/model"
)

// examples/js-plugins 中的示例插件必须能安装并正常运行。
func TestJSExampleDailyBonus(t *testing.T) {
	dir := filepath.Join("..", "..", "examples", "js-plugins", "daily-bonus")
	files := map[string]string{}
	for _, name := range []string{"plugin.json", "main.js"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		files[name] = string(b)
	}
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		m := e.manager(t)
		je := newJSEnv(t, e, m)
		if v := install(t, m, makeZip(t, files)); v.Status != StatusOK || len(v.Features.Routes) != 2 {
			t.Fatalf("示例插件安装失败：%+v", v)
		}
		if err := m.SaveConfig(ctx, "daily_bonus", map[string]any{"reward": 10, "streak_days": 1, "streak_reward": 5}); err != nil {
			t.Fatal(err)
		}
		mustEnable(t, m, "daily_bonus")

		var res struct {
			Data map[string]any `json:"data"`
		}
		w := je.do("POST", "plugin/daily_bonus/checkin", "", nil, je.uTok)
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &res) != nil || res.Data["reward"] != float64(10) ||
			res.Data["bonus"] != float64(5) || res.Data["streak"] != float64(1) {
			t.Fatalf("签到失败：%d %s", w.Code, w.Body.String())
		}
		if w := je.do("POST", "plugin/daily_bonus/checkin", "", nil, je.uTok); w.Code != 400 {
			t.Fatalf("同一天重复签到应 400：%d %s", w.Code, w.Body.String())
		}
		var u model.User
		e.db.Take(&u, je.normal.ID)
		if u.Balance != je.normal.Balance+15 {
			t.Fatalf("余额应增加 15 分：%d → %d", je.normal.Balance, u.Balance)
		}
		w = je.do("GET", "plugin/daily_bonus/status", "", nil, je.uTok)
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &res) != nil || res.Data["checked_in"] != true || res.Data["streak"] != float64(1) {
			t.Fatalf("签到状态错误：%d %s", w.Code, w.Body.String())
		}
		if w := je.do("POST", "plugin/daily_bonus/checkin", "", nil, ""); w.Code == 200 {
			t.Fatal("未登录不能签到")
		}
	})
}
