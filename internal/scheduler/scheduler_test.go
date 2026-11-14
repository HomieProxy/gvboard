package scheduler

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"gvboard/internal/cache"
	"gvboard/internal/config"
)

func TestAddValidation(t *testing.T) {
	s := New(nil, zap.NewNop(), nil)
	ok := func(context.Context) error { return nil }
	if err := s.Add(Job{Name: "a", Spec: "* * * * *", Run: ok}); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(Job{Name: "a", Spec: "* * * * *", Run: ok}); err == nil {
		t.Error("重复任务名应报错")
	}
	for _, spec := range []string{"", "61 * * * *", "* * * * * *", "@every"} {
		if err := s.Add(Job{Name: "b" + spec, Spec: spec, Run: ok}); err == nil {
			t.Errorf("非法表达式 %q 应报错", spec)
		}
	}
	// 原项目 Kernel.php 中用到的调度写法
	for name, spec := range map[string]string{
		"everyMinute": "* * * * *", "everyFifteenMinutes": "*/15 * * * *",
		"dailyAt 0:10": "10 0 * * *", "dailyAt 22:30": "30 22 * * *", "daily": "0 0 * * *",
	} {
		if err := s.Add(Job{Name: name, Spec: spec, Run: ok}); err != nil {
			t.Errorf("%s (%s): %v", name, spec, err)
		}
	}
}

func newRedis(t *testing.T) *cache.Cache {
	t.Helper()
	addr := os.Getenv("GVB_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("未设置 GVB_TEST_REDIS_ADDR，跳过")
	}
	prefix := fmt.Sprintf("gvbtest%d:", time.Now().UnixNano())
	c, err := cache.Open(config.RedisConfig{Addr: addr, Prefix: prefix})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		keys, _ := c.Keys(ctx, prefix+"*").Result()
		if len(keys) > 0 {
			c.Del(ctx, keys...)
		}
		c.Close()
	})
	return c
}

func fixedNow(t time.Time) func() time.Time { return func() time.Time { return t } }

func TestRunOncePerTickAcrossInstances(t *testing.T) {
	c := newRedis(t)
	base := time.Date(2026, 1, 1, 12, 0, 30, 0, time.Local)
	var runs atomic.Int32
	job := Job{Name: "stat", Spec: "* * * * *", Run: func(context.Context) error { runs.Add(1); return nil }}

	// 模拟 5 个实例在同一分钟内同时触发
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		s := New(c, zap.NewNop(), nil)
		s.now = fixedNow(base.Add(time.Duration(i) * time.Second))
		wg.Add(1)
		go func() { defer wg.Done(); s.trigger(job) }()
	}
	wg.Wait()
	if runs.Load() != 1 {
		t.Fatalf("同一分钟应只执行一次，实际 %d", runs.Load())
	}

	s := New(c, zap.NewNop(), nil)
	s.now = fixedNow(base.Add(time.Minute))
	s.trigger(job)
	if runs.Load() != 2 {
		t.Errorf("下一分钟应再次执行，实际 %d", runs.Load())
	}
}

func TestNoOverlap(t *testing.T) {
	c := newRedis(t)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.Local)
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	var runs atomic.Int32
	job := Job{Name: "traffic", Spec: "* * * * *", NoOverlap: true, Run: func(context.Context) error {
		runs.Add(1)
		started <- struct{}{}
		<-release
		return nil
	}}

	s1 := New(c, zap.NewNop(), nil)
	s1.now = fixedNow(base)
	done := make(chan struct{})
	go func() { s1.trigger(job); close(done) }()
	<-started

	// 上一次未结束，下一分钟（其他实例）触发时跳过
	s2 := New(c, zap.NewNop(), nil)
	s2.now = fixedNow(base.Add(time.Minute))
	s2.trigger(job)
	if runs.Load() != 1 {
		t.Fatalf("上次未结束时应跳过，实际执行 %d 次", runs.Load())
	}

	close(release)
	<-done
	s2.now = fixedNow(base.Add(2 * time.Minute))
	s2.trigger(job) // release 已关闭，任务立即返回
	if runs.Load() != 2 {
		t.Errorf("上次结束后应恢复执行，实际 %d", runs.Load())
	}

	// 未设置 NoOverlap 的任务不受影响
	var free atomic.Int32
	s3 := New(c, zap.NewNop(), nil)
	s3.now = fixedNow(base.Add(3 * time.Minute))
	s3.trigger(Job{Name: "free", Spec: "* * * * *", Run: func(context.Context) error { free.Add(1); return nil }})
	if free.Load() != 1 {
		t.Error("普通任务应执行")
	}
}

func TestPanicRecovered(t *testing.T) {
	c := newRedis(t)
	s := New(c, zap.NewNop(), nil)
	s.trigger(Job{Name: "boom", Spec: "* * * * *", NoOverlap: true, Run: func(context.Context) error { panic("boom") }})
	// 走到这里说明 panic 已被捕获；防重叠锁也应已释放
	s.now = fixedNow(time.Now().Add(time.Minute))
	ran := false
	s.trigger(Job{Name: "boom", Spec: "* * * * *", NoOverlap: true, Run: func(context.Context) error { ran = true; return nil }})
	if !ran {
		t.Error("panic 后任务应能继续执行")
	}
}

func TestStateAndManualRun(t *testing.T) {
	c := newRedis(t)
	s := New(c, zap.NewNop(), nil)
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	var runs atomic.Int32
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.Add(Job{Name: "slow", Spec: "* * * * *", NoOverlap: true, Run: func(context.Context) error {
		runs.Add(1)
		started <- struct{}{}
		<-release
		return nil
	}}))
	must(s.Add(Job{Name: "fail", Spec: "0 0 * * *", Run: func(context.Context) error { return errors.New(strings.Repeat("x", 600)) }}))
	must(s.Add(Job{Name: "never", Spec: "0 0 * * *", Run: func(context.Context) error { return nil }}))
	ctx := context.Background()

	if err := s.Start(ctx, "missing"); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("不存在的任务应返回 ErrJobNotFound，实际 %v", err)
	}
	must(s.Start(ctx, "slow"))
	<-started
	if err := s.Start(ctx, "slow"); !errors.Is(err, ErrJobRunning) {
		t.Fatalf("执行中再次手动执行应返回 ErrJobRunning，实际 %v", err)
	}
	// NoOverlap 任务的手动执行与定时触发互斥
	s.trigger(s.jobs[0])
	if runs.Load() != 1 {
		t.Fatalf("手动执行中定时触发应跳过，实际执行 %d 次", runs.Load())
	}
	list, err := s.List(ctx)
	must(err)
	if len(list) != 3 || list[0].Name != "slow" || list[1].Name != "fail" || list[2].Name != "never" {
		t.Fatalf("任务列表顺序有误: %+v", list)
	}
	if !list[0].Running || list[0].Last == nil || !list[0].Last.Manual || list[0].Last.FinishedAt != 0 {
		t.Errorf("执行中的状态有误: %+v", list[0])
	}
	if list[2].Last != nil || list[2].Running {
		t.Errorf("从未执行的任务不应有状态: %+v", list[2])
	}
	close(release)
	s.wg.Wait()

	s.trigger(s.jobs[1])
	list, err = s.List(ctx)
	must(err)
	if list[0].Running || list[0].Last.FinishedAt == 0 || list[0].Last.Error != "" {
		t.Errorf("完成后的状态有误: %+v", list[0].Last)
	}
	if l := list[1].Last; l == nil || l.Manual || len([]rune(l.Error)) != 501 {
		t.Errorf("失败任务应记录截断后的错误: %+v", l)
	}
	// 结束后可再次手动执行
	must(s.Start(ctx, "slow"))
	<-started
	s.wg.Wait()

	// 异常退出遗留的“执行中”状态超过最长运行时间后不再视为执行中
	must(c.SetJSON(ctx, c.Key("cron-state:never"), JobState{StartedAt: time.Now().Add(-2 * time.Hour).Unix()}, 0))
	list, err = s.List(ctx)
	must(err)
	if list[2].Running {
		t.Error("超时的执行中状态不应视为执行中")
	}
}

func TestStartAfterStop(t *testing.T) {
	c := newRedis(t)
	s := New(c, zap.NewNop(), nil)
	if err := s.Add(Job{Name: "a", Spec: "0 0 * * *", Run: func(context.Context) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.Run(ctx)
	if err := s.Start(context.Background(), "a"); !errors.Is(err, ErrStopped) {
		t.Fatalf("调度器停止后应拒绝手动执行，实际 %v", err)
	}
	// 被拒绝时应释放锁
	if l, err := c.TryLock(context.Background(), "cron-manual:a", time.Minute); err != nil {
		t.Errorf("锁未释放: %v", err)
	} else {
		_ = l.Unlock(context.Background())
	}
}

func TestRunHeartbeatAndShutdown(t *testing.T) {
	c := newRedis(t)
	var beats atomic.Int32
	s := New(c, zap.NewNop(), func(context.Context, time.Time) { beats.Add(1) })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("取消后调度器应退出")
	}
	if beats.Load() < 1 {
		t.Error("启动时应立即记录一次心跳")
	}
}
