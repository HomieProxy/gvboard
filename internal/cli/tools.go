package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"gorm.io/gorm"

	"gvboard/internal/account"
	"gvboard/internal/adminuser"
	"gvboard/internal/app"
	"gvboard/internal/database"
	"gvboard/internal/model"
	"gvboard/internal/scheduler"
	"gvboard/internal/util"
)

// withServices 启动完整的应用与业务服务后执行 f。
func withServices(f func(ctx context.Context, a *app.App, sv *services) error) error {
	a, err := app.New(cfgPath)
	if err != nil {
		return err
	}
	defer a.Close()
	sv, err := newServices(a)
	if err != nil {
		return err
	}
	defer sv.close()
	return f(context.Background(), a, sv)
}

// confirm 要求输入 yes 确认，yes 为 true 时跳过。
func confirm(cmd *cobra.Command, msg string, yes bool) bool {
	if yes {
		return true
	}
	cmd.Printf("%s 输入 yes 确认：", msg)
	line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	return strings.TrimSpace(line) == "yes"
}

func newJobCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "job", Short: "定时任务"}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "列出全部定时任务",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withServices(func(_ context.Context, _ *app.App, sv *services) error {
				for _, j := range sv.jobs() {
					cmd.Printf("%-18s %s\n", j.Name, j.Spec)
				}
				return nil
			})
		},
	}, &cobra.Command{
		Use:   "run <任务名>",
		Short: "立即执行一个定时任务（不检查其他实例是否正在执行）",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withServices(func(ctx context.Context, a *app.App, sv *services) error {
				jobs := sv.jobs()
				i := slices.IndexFunc(jobs, func(j scheduler.Job) bool { return j.Name == args[0] })
				if i < 0 {
					return fmt.Errorf("任务不存在: %s（gvboard job list 查看全部任务）", args[0])
				}
				if err := jobs[i].Run(ctx); err != nil {
					return err
				}
				cmd.Println("执行完成")
				return nil
			})
		},
	})
	return cmd
}

func newCheckServerCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check-server",
		Short: "立即检查掉线节点并通知管理员（不受管理端开关影响；开启时定时任务每 5 分钟自动执行）",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withServices(func(ctx context.Context, _ *app.App, sv *services) error {
				list, err := sv.tasks.CheckServers(ctx)
				if err != nil {
					return err
				}
				for _, o := range list {
					cmd.Printf("掉线：%s #%d %s（%s）\n", o.Type, o.ID, o.Name, o.Host)
				}
				cmd.Printf("共 %d 个节点掉线\n", len(list))
				return nil
			})
		},
	}
}

func newResetPasswordCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reset-password <邮箱>",
		Short: "重置用户密码为随机密码，并注销该用户的全部登录会话",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withServices(func(ctx context.Context, a *app.App, _ *services) error {
				var u model.User
				err := a.DB.WithContext(ctx).Where("LOWER(email) = ?", strings.ToLower(strings.TrimSpace(args[0]))).Take(&u).Error
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return errors.New("邮箱不存在")
				}
				if err != nil {
					return err
				}
				password := util.GUID()
				hash, err := util.HashPassword(password)
				if err != nil {
					return err
				}
				err = a.DB.WithContext(ctx).Model(&u).Updates(map[string]any{"password": hash, "password_algo": nil, "password_salt": nil}).Error
				if err != nil {
					return err
				}
				if err := a.Auth.RemoveAll(ctx, u.ID); err != nil {
					return err
				}
				cmd.Println("重置成功")
				cmd.Printf("新密码为：%s，请尽快修改密码。\n", password)
				return nil
			})
		},
	}
}

func newCreateAdminCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "create-admin <邮箱>",
		Short: "创建管理员账号（随机密码）；邮箱已注册时将该用户设为管理员",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			email := strings.TrimSpace(args[0])
			if !util.EmailValid(email, false) {
				return errors.New("邮箱格式不正确")
			}
			return withServices(func(ctx context.Context, a *app.App, _ *services) error {
				password, err := createAdmin(a.DB.WithContext(ctx), email)
				if err != nil {
					return err
				}
				if password != "" {
					cmd.Printf("管理员已创建：%s\n密码：%s（只显示这一次，请登录后尽快修改）\n", email, password)
				} else {
					cmd.Printf("已将 %s 设为管理员，密码不变\n", email)
				}
				cmd.Printf("管理端地址：/%s\n", strings.Trim(a.Settings.String("secure_path"), "/"))
				return nil
			})
		},
	}
}

// createAdmin 邮箱未注册时创建用户（随机密码）并设为管理员，已注册时只设为管理员。
// 返回新用户的密码，已注册时返回空字符串。
func createAdmin(db *gorm.DB, email string) (string, error) {
	password := ""
	err := db.Transaction(func(tx *gorm.DB) error {
		var u model.User
		err := database.WhereEmail(tx, email).Take(&u).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			password = util.GUID()
			created, _, err := account.Create(tx, account.NewUser{Email: email, Password: password})
			if err != nil {
				return err
			}
			u = *created
		} else if err != nil {
			return err
		}
		return tx.Model(&model.User{}).Where("id = ?", u.ID).Update("is_admin", 1).Error
	})
	if err != nil {
		return "", err
	}
	return password, nil
}

func newClearUserCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "clear-user",
		Short: "删除没有任何数据的用户（从未登录、订阅没有套餐 / 流量 / 到期时间，且没有订单、余额、佣金）",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withServices(func(ctx context.Context, a *app.App, sv *services) error {
				users := &adminuser.Service{DB: a.DB, Settings: a.Settings, Auth: a.Auth, Subscriptions: sv.subs, Mail: a.Mail}
				ids, err := users.EmptyUserIDs(ctx)
				if err != nil {
					return err
				}
				if len(ids) == 0 {
					cmd.Println("没有需要删除的用户")
					return nil
				}
				if !confirm(cmd, fmt.Sprintf("将删除 %d 位没有任何数据的用户。", len(ids)), yes) {
					cmd.Println("已取消")
					return nil
				}
				n, err := users.DeleteByIDs(ctx, ids)
				if err != nil {
					return err
				}
				cmd.Printf("已删除 %d 位没有任何数据的用户\n", n)
				return nil
			})
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "不询问直接执行")
	return cmd
}

func newResetUserCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "reset-user",
		Short: "重置所有订阅的 uuid 与 token（所有订阅链接失效，需要重新获取）",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !confirm(cmd, "确定要重置所有订阅的安全信息吗？所有订阅链接将失效。", yes) {
				cmd.Println("已取消")
				return nil
			}
			return withServices(func(ctx context.Context, a *app.App, _ *services) error {
				n, err := resetAllSubscriptions(ctx, a.DB)
				if err != nil {
					return err
				}
				cmd.Printf("已重置 %d 个订阅的安全信息\n", n)
				return nil
			})
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "不询问直接执行")
	return cmd
}

// resetAllSubscriptions 为每个订阅生成新的 uuid 与 token（对应原项目 reset:user）。
func resetAllSubscriptions(ctx context.Context, db *gorm.DB) (int, error) {
	n, lastID := 0, int64(0)
	for {
		var ids []int64
		if err := db.WithContext(ctx).Model(&model.Subscription{}).Where("id > ?", lastID).Order("id").Limit(500).Pluck("id", &ids).Error; err != nil {
			return n, err
		}
		if len(ids) == 0 {
			return n, nil
		}
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			for _, id := range ids {
				if err := tx.Model(&model.Subscription{}).Where("id = ?", id).
					Updates(map[string]any{"uuid": util.UUIDv4(), "token": util.GUID()}).Error; err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return n, err
		}
		n += len(ids)
		lastID = ids[len(ids)-1]
	}
}
