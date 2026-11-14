// Package auth 登录凭证与会话管理，对应原项目 AuthService 及 User / Admin 中间件。
//
// 登录凭证为 HS256 JWT，载荷 {id, session}，密钥为 app.key（与原项目相同）；
// 会话记录在 Redis 中，凭证必须对应一条存在的会话才有效，删除会话即可让凭证立即失效。
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"gvboard/internal/cache"
	"gvboard/internal/model"
	"gvboard/internal/util"
)

var (
	// ErrUnauthenticated 凭证缺失、无效、会话已删除或用户不存在。
	ErrUnauthenticated = errors.New("未登录或登录已过期")
	// ErrBanned 用户已被封禁（决策 D16：已登录的会话同样失效）。
	ErrBanned = errors.New("账户已被封禁")
)

// Identity 当前登录用户。
type Identity struct {
	ID      int64  `json:"id"`
	Email   string `json:"email"`
	IsAdmin bool   `json:"is_admin"`
	IsStaff bool   `json:"is_staff"`
	Session string `json:"-"`
}

// SessionMeta 会话信息。
type SessionMeta struct {
	IP      string `json:"ip"`
	LoginAt int64  `json:"login_at"`
	UA      string `json:"ua"`
}

type claims struct {
	ID      int64  `json:"id"`
	Session string `json:"session"`
	jwt.RegisteredClaims
}

// Service 登录凭证服务。
type Service struct {
	db    *gorm.DB
	cache *cache.Cache
	key   []byte
	now   func() time.Time

	// AdminHooks 管理端路由组在认证通过后执行的中间件，必须在挂载路由前设置。
	AdminHooks []gin.HandlerFunc
}

// New 创建服务。appKey 原样作为签名密钥（与 Laravel config('app.key') 一致，不做 base64 解码）。
func New(db *gorm.DB, c *cache.Cache, appKey string) *Service {
	return &Service{db: db, cache: c, key: []byte(appKey), now: time.Now}
}

func (s *Service) sessionsKey(userID int64) string {
	return s.cache.KeyOf(cache.KeyUserSessions, userID)
}

// Issue 为用户签发登录凭证并记录会话。
func (s *Service) Issue(ctx context.Context, userID int64, ip, ua string) (string, error) {
	sid := util.GUID()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims{ID: userID, Session: sid}).SignedString(s.key)
	if err != nil {
		return "", err
	}
	meta, err := json.Marshal(SessionMeta{IP: ip, LoginAt: s.now().Unix(), UA: ua})
	if err != nil {
		return "", err
	}
	if err := s.cache.HSet(ctx, s.sessionsKey(userID), sid, meta).Err(); err != nil {
		return "", err
	}
	return token, nil
}

// Parse 校验凭证并返回当前用户。每次都从数据库读取用户，管理员权限与封禁状态即时生效。
func (s *Service) Parse(ctx context.Context, raw string) (*Identity, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) > 7 && strings.EqualFold(raw[:7], "Bearer ") {
		raw = strings.TrimSpace(raw[7:])
	}
	if raw == "" {
		return nil, ErrUnauthenticated
	}
	var cl claims
	_, err := jwt.ParseWithClaims(raw, &cl, func(*jwt.Token) (any, error) { return s.key, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil || cl.ID <= 0 || cl.Session == "" {
		return nil, ErrUnauthenticated
	}
	ok, err := s.cache.HExists(ctx, s.sessionsKey(cl.ID), cl.Session).Result()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrUnauthenticated
	}
	var u model.User
	err = s.db.WithContext(ctx).Select("id", "email", "is_admin", "is_staff", "banned").Take(&u, cl.ID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrUnauthenticated
	}
	if err != nil {
		return nil, err
	}
	if u.Banned != 0 {
		return nil, ErrBanned
	}
	return &Identity{ID: u.ID, Email: u.Email, IsAdmin: u.IsAdmin != 0, IsStaff: u.IsStaff != 0, Session: cl.Session}, nil
}

// Sessions 返回用户的全部会话，键为会话 ID。
func (s *Service) Sessions(ctx context.Context, userID int64) (map[string]SessionMeta, error) {
	raw, err := s.cache.HGetAll(ctx, s.sessionsKey(userID)).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	out := make(map[string]SessionMeta, len(raw))
	for sid, v := range raw {
		var m SessionMeta
		if json.Unmarshal([]byte(v), &m) == nil {
			out[sid] = m
		}
	}
	return out, nil
}

// RemoveSession 删除指定会话，对应的凭证立即失效。
func (s *Service) RemoveSession(ctx context.Context, userID int64, sid string) error {
	return s.cache.HDel(ctx, s.sessionsKey(userID), sid).Err()
}

// RemoveAll 删除用户的全部会话（修改 / 重置密码、封禁等场景）。
func (s *Service) RemoveAll(ctx context.Context, userID int64) error {
	return s.cache.Del(ctx, s.sessionsKey(userID)).Err()
}

// tempTokenKey 快捷登录临时令牌。
func (s *Service) tempTokenKey(code string) string {
	return s.cache.KeyOf(cache.KeyTempToken, code)
}

// CreateTempToken 生成一次性快捷登录码，有效期 60 秒（与原项目一致）。
func (s *Service) CreateTempToken(ctx context.Context, userID int64) (string, error) {
	code := util.GUID()
	if err := s.cache.Set(ctx, s.tempTokenKey(code), userID, 60*time.Second).Err(); err != nil {
		return "", err
	}
	return code, nil
}

// getDelScript 原子地读取并删除（GETDEL 需要 Redis 6.2，脚本兼容更早的版本）。
var getDelScript = redis.NewScript(`local v = redis.call("get", KEYS[1]) if v then redis.call("del", KEYS[1]) end return v`)

// ConsumeTempToken 使用快捷登录码，成功后立即作废。码不存在或已被使用时返回 0。
func (s *Service) ConsumeTempToken(ctx context.Context, code string) (int64, error) {
	if code == "" || len(code) > 64 {
		return 0, nil
	}
	v, err := getDelScript.Run(ctx, s.cache.Client, []string{s.tempTokenKey(code)}).Text()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	id, _ := strconv.ParseInt(v, 10, 64)
	return id, nil
}
