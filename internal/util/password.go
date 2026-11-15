package util

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"runtime"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// 密码算法（v2_user.password_algo），兼容从其他面板导入的用户。空值表示 bcrypt。
const (
	PasswordAlgoMD5     = "md5"
	PasswordAlgoSHA256  = "sha256"
	PasswordAlgoMD5Salt = "md5salt"
)

// VerifyPassword 与原项目 Helper::multiPasswordVerify 一致，比较使用常量时间。
func VerifyPassword(algo, salt, password, hash string) bool {
	switch algo {
	case PasswordAlgoMD5:
		sum := md5.Sum([]byte(password))
		return constEq(hex.EncodeToString(sum[:]), hash)
	case PasswordAlgoSHA256:
		sum := sha256.Sum256([]byte(password))
		return constEq(hex.EncodeToString(sum[:]), hash)
	case PasswordAlgoMD5Salt:
		sum := md5.Sum([]byte(password + salt))
		return constEq(hex.EncodeToString(sum[:]), hash)
	default:
		return bcrypt.CompareHashAndPassword([]byte(hash), bcryptInput(password)) == nil
	}
}

// HashPassword 生成 bcrypt 哈希（与 PHP password_hash 默认参数相同，cost 10）。
func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword(bcryptInput(password), bcrypt.DefaultCost)
	return string(b), err
}

// ErrPasswordBusy 等待密码计算名额超时。
var ErrPasswordBusy = errors.New("password hashing busy")

// 未登录即可触发的 bcrypt 计算（登录、注册）最多同时占用一半 CPU 核心，
// 大量请求时排队等待，超时返回 ErrPasswordBusy，保证其它接口仍有 CPU 可用。
var (
	passwordSlots = make(chan struct{}, max(1, runtime.GOMAXPROCS(0)/2))
	passwordWait  = 3 * time.Second
)

func acquirePasswordSlot(ctx context.Context) (func(), error) {
	select {
	case passwordSlots <- struct{}{}:
		return func() { <-passwordSlots }, nil
	default:
	}
	t := time.NewTimer(passwordWait)
	defer t.Stop()
	select {
	case passwordSlots <- struct{}{}:
		return func() { <-passwordSlots }, nil
	case <-t.C:
		return nil, ErrPasswordBusy
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// CheckPassword 带并发上限的 VerifyPassword，供未登录即可调用的接口使用。
func CheckPassword(ctx context.Context, algo, salt, password, hash string) (bool, error) {
	if algo != "" {
		return VerifyPassword(algo, salt, password, hash), nil
	}
	release, err := acquirePasswordSlot(ctx)
	if err != nil {
		return false, err
	}
	defer release()
	return VerifyPassword(algo, salt, password, hash), nil
}

// HashPasswordLimited 带并发上限的 HashPassword，供未登录即可调用的接口使用。
func HashPasswordLimited(ctx context.Context, password string) (string, error) {
	release, err := acquirePasswordSlot(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	return HashPassword(password)
}

// bcrypt 只使用前 72 字节。PHP 静默截断，Go 对超长密码直接报错，这里按 PHP 的方式截断，
// 保证原项目用超长密码生成的哈希仍能校验通过。
func bcryptInput(password string) []byte {
	b := []byte(password)
	if len(b) > 72 {
		b = b[:72]
	}
	return b
}

func constEq(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
