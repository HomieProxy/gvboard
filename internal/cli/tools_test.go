package cli

import (
	"testing"

	"gvboard/internal/model"
	"gvboard/internal/testutil"
	"gvboard/internal/util"
)

func TestCreateAdmin(t *testing.T) {
	for _, driver := range testutil.Drivers() {
		t.Run(driver, func(t *testing.T) {
			db := testutil.DB(t, driver)

			password, err := createAdmin(db, "Admin@Example.com")
			if err != nil {
				t.Fatal(err)
			}
			if password == "" {
				t.Fatal("新管理员应返回随机密码")
			}
			var u model.User
			if err := db.Where("email = ?", "Admin@Example.com").Take(&u).Error; err != nil {
				t.Fatal(err)
			}
			if u.IsAdmin != 1 {
				t.Errorf("is_admin = %d", u.IsAdmin)
			}
			algo, salt := "", ""
			if u.PasswordAlgo != nil {
				algo = *u.PasswordAlgo
			}
			if u.PasswordSalt != nil {
				salt = *u.PasswordSalt
			}
			if !util.VerifyPassword(algo, salt, password, u.Password) {
				t.Error("返回的密码无法登录")
			}
			var subs int64
			db.Model(&model.Subscription{}).Where("user_id = ? AND is_primary = 1", u.ID).Count(&subs)
			if subs != 1 {
				t.Errorf("主订阅数量 = %d", subs)
			}

			// 已注册用户（邮箱大小写不同）：只设为管理员，不改密码、不重复创建
			db.Model(&model.User{}).Where("id = ?", u.ID).Update("is_admin", 0)
			again, err := createAdmin(db, "admin@example.COM")
			if err != nil {
				t.Fatal(err)
			}
			if again != "" {
				t.Error("已注册用户不应重置密码")
			}
			var after model.User
			db.Where("id = ?", u.ID).Take(&after)
			if after.IsAdmin != 1 || after.Password != u.Password {
				t.Errorf("is_admin = %d，密码是否变化 = %v", after.IsAdmin, after.Password != u.Password)
			}
			var users int64
			db.Model(&model.User{}).Count(&users)
			if users != 1 {
				t.Errorf("用户数量 = %d", users)
			}
		})
	}
}
