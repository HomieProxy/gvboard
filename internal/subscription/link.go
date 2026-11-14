package subscription

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"gvboard/internal/cache"
	"gvboard/internal/model"
	"gvboard/internal/setting"
	"gvboard/internal/util"
)

// 订阅链接显示方式（设置 show_subscribe_method）。
const (
	LinkPermanent = 0 // 永久 token
	LinkOneTime   = 1 // 一次性 token，24 小时内有效
	LinkTOTP      = 2 // 时效 token，show_subscribe_expire 分钟内有效
)

const (
	defaultSubscribePath = "/api/v1/client/subscribe"
	otpTTL               = 24 * time.Hour
)

// Linker 生成订阅链接，对应原项目 Helper::getSubscribeUrl。多订阅后时效 token 中的 ID 为订阅 ID。
type Linker struct {
	Settings *setting.Service
	Cache    *cache.Cache
	Now      func() time.Time
}

func (l *Linker) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

// URL 生成订阅链接。未设置订阅域名时使用 baseURL（当前请求的地址）。
func (l *Linker) URL(ctx context.Context, sub *model.Subscription, baseURL string) (string, error) {
	path := l.Settings.String("subscribe_path")
	if path == "" {
		path = defaultSubscribePath
	}
	token := sub.Token
	switch l.Settings.Int("show_subscribe_method") {
	case LinkOneTime:
		t, err := l.oneTimeToken(ctx, sub.Token)
		if err != nil {
			return "", err
		}
		token = t
	case LinkTOTP:
		token = TOTPToken(sub.ID, sub.Token, l.Settings.Int("show_subscribe_expire"), l.now())
	}
	host := baseURL
	if urls := strings.Split(l.Settings.String("subscribe_url"), ","); len(urls) > 0 {
		if u := urls[util.RandomInt(0, int64(len(urls)-1))]; u != "" {
			host = u
		}
	}
	return host + path + "?token=" + token, nil
}

// oneTimeToken 同一订阅 24 小时内复用同一个一次性 token；并发生成时以先写入的为准。
func (l *Linker) oneTimeToken(ctx context.Context, token string) (string, error) {
	key := l.Cache.KeyOf(cache.KeySubscribeOTP, token)
	v, err := l.Cache.Get(ctx, key).Result()
	if err == nil && v != "" {
		return v, nil
	}
	if err != nil && !errors.Is(err, redis.Nil) {
		return "", err
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	v = util.Base64EncodeURLSafe(b)
	ok, err := l.Cache.SetNX(ctx, key, v, otpTTL).Result()
	if err != nil {
		return "", err
	}
	if !ok {
		return l.Cache.Get(ctx, key).Result()
	}
	if err := l.Cache.Set(ctx, l.Cache.KeyOf(cache.KeySubscribeOTPToken, v), token, otpTTL).Err(); err != nil {
		return "", err
	}
	return v, nil
}

// TOTPToken 时效 token：base64url("订阅ID:HMAC-SHA1(时间片, 订阅token)")，与原项目算法一致。
func TOTPToken(subID int64, token string, expireMinutes int64, now time.Time) string {
	return util.Base64EncodeURLSafe([]byte(fmt.Sprintf("%d:%s", subID, TOTPHash(token, expireMinutes, now))))
}

// TOTPStep 时效 token 的有效秒数（show_subscribe_expire 分钟，未设置时 5 分钟）。
func TOTPStep(expireMinutes int64) int64 {
	if expireMinutes <= 0 {
		expireMinutes = 5
	}
	return expireMinutes * 60
}

// TOTPHash 时效 token 中的签名：HMAC-SHA1(8 字节大端时间片, 订阅 token) 的十六进制。
func TOTPHash(token string, expireMinutes int64, now time.Time) string {
	counter := now.Unix() / TOTPStep(expireMinutes)
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(counter))
	mac := hmac.New(sha1.New, []byte(token))
	mac.Write(msg[:])
	return hex.EncodeToString(mac.Sum(nil))
}
