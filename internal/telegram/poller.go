package telegram

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"gvboard/internal/cache"
)

const (
	pollTimeout   = 25 // getUpdates 长轮询秒数
	pollLockName  = "telegram_poller"
	pollLockTTL   = 90 * time.Second
	pollIdle      = 10 * time.Second // 未启用长轮询或其他实例在轮询时的检查间隔
	pollBackoff   = 5 * time.Second
	updateTimeout = 60 * time.Second
)

// polling 当前是否应使用长轮询。
func (s *Service) polling() bool {
	return s.Enabled() && s.Settings.String("telegram_mode") == "polling"
}

// RunPoller 长轮询模式下接收更新，阻塞直到 ctx 结束。多实例时通过 Redis 锁保证只有一个实例在轮询；
// 设置切换为 Webhook 或关闭机器人后自动停止轮询。
func (s *Service) RunPoller(ctx context.Context) {
	for ctx.Err() == nil {
		if !s.polling() {
			sleep(ctx, pollIdle)
			continue
		}
		l, err := s.Cache.TryLock(ctx, pollLockName, pollLockTTL)
		if err != nil {
			if !errors.Is(err, cache.ErrLockHeld) {
				s.log().Warn("获取长轮询锁失败", zap.Error(err))
			}
			sleep(ctx, pollIdle)
			continue
		}
		s.pollLoop(ctx, l)
		if err := l.Unlock(context.WithoutCancel(ctx)); err != nil {
			s.log().Warn("释放长轮询锁失败", zap.Error(err))
		}
	}
}

func (s *Service) pollLoop(ctx context.Context, l *cache.Lock) {
	webhookCleared := ""
	for ctx.Err() == nil && s.polling() {
		if ok, err := l.Extend(ctx, pollLockTTL); err != nil || !ok {
			return
		}
		cl, err := s.Client()
		if err != nil {
			return
		}
		// 存在 Webhook 时 getUpdates 会返回 409，切换到长轮询时先删除（每个 token 只做一次）。
		if webhookCleared != cl.Token {
			if err := cl.DeleteWebhook(ctx); err != nil {
				s.log().Warn("删除 Webhook 失败", zap.Error(err))
				sleep(ctx, pollBackoff)
				continue
			}
			webhookCleared = cl.Token
		}
		offsetKey := s.Cache.KeyOf(cache.KeyTelegramOffset, tokenDigest(cl.Token))
		offset, err := s.Cache.Get(ctx, offsetKey).Int64()
		if err != nil && !errors.Is(err, redis.Nil) {
			s.log().Warn("读取长轮询位置失败", zap.Error(err))
			sleep(ctx, pollBackoff)
			continue
		}
		updates, err := cl.GetUpdates(ctx, offset, pollTimeout)
		if err != nil {
			if ctx.Err() == nil {
				s.log().Warn("Telegram 长轮询失败", zap.Error(err))
				if IsAPIError(err, 409) {
					webhookCleared = ""
				}
				sleep(ctx, pollBackoff)
			}
			continue
		}
		for _, u := range updates {
			uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), updateTimeout)
			s.HandleUpdate(uctx, u)
			cancel()
			if err := s.Cache.Set(ctx, offsetKey, strconv.FormatInt(u.UpdateID+1, 10), 0).Err(); err != nil {
				s.log().Warn("保存长轮询位置失败", zap.Error(err))
			}
		}
	}
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
