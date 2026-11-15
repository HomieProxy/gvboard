package util

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPasswordLimit(t *testing.T) {
	hash, err := HashPasswordLimited(context.Background(), "password123")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := CheckPassword(context.Background(), "", "", "password123", hash); err != nil || !ok {
		t.Fatalf("校验失败: %v %v", ok, err)
	}

	// 占满名额后排队超时返回繁忙；旧算法（md5 等）不占名额。
	old := passwordWait
	passwordWait = 50 * time.Millisecond
	defer func() { passwordWait = old }()
	for i := 0; i < cap(passwordSlots); i++ {
		passwordSlots <- struct{}{}
	}
	start := time.Now()
	if _, err := CheckPassword(context.Background(), "", "", "password123", hash); !errors.Is(err, ErrPasswordBusy) {
		t.Fatalf("应返回繁忙，实际 %v", err)
	}
	if time.Since(start) < 50*time.Millisecond {
		t.Error("应先排队等待")
	}
	if _, err := HashPasswordLimited(context.Background(), "x"); !errors.Is(err, ErrPasswordBusy) {
		t.Fatalf("应返回繁忙，实际 %v", err)
	}
	if ok, err := CheckPassword(context.Background(), PasswordAlgoMD5, "", "password123", "482c811da5d5b4bc6d497ffa98491e38"); err != nil || !ok {
		t.Fatalf("md5 不应受名额限制: %v %v", ok, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CheckPassword(ctx, "", "", "password123", hash); !errors.Is(err, context.Canceled) {
		t.Fatalf("请求取消时应立即返回，实际 %v", err)
	}

	// 释放一个名额后排队的请求可以继续。
	go func() {
		time.Sleep(10 * time.Millisecond)
		<-passwordSlots
	}()
	if ok, err := CheckPassword(context.Background(), "", "", "password123", hash); err != nil || !ok {
		t.Fatalf("释放名额后应校验成功: %v %v", ok, err)
	}
	for len(passwordSlots) > 0 {
		<-passwordSlots
	}
}
