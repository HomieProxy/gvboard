// Package scheduler 定时任务框架，替代原项目的 Laravel Schedule（app/Console/Kernel.php）。
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"go.uber.org/zap"

	"gvboard/internal/cache"
)

// Job 定时任务。
type Job struct {
	Name string
	// Spec 标准 5 段 cron 表达式（分 时 日 月 周），按 app.timezone 解释。
	Spec string
	// NoOverlap 上一次未结束时跳过本次（对应原项目 withoutOverlapping）。
	NoOverlap bool
	// MaxRuntime 防重叠锁的最长持有时间，超过后视为异常退出自动释放。默认 1 小时。
	MaxRuntime time.Duration
	Run        func(ctx context.Context) error
}

// Locker 分布式锁，多实例部署时保证任务只执行一次。
type Locker interface {
	TryLock(ctx context.Context, name string, ttl time.Duration) (*cache.Lock, error)
}

// StateStore 保存任务最近一次执行状态（多实例共享）。
type StateStore interface {
	Key(k string) string
	GetJSON(ctx context.Context, key string, v any) (bool, error)
	SetJSON(ctx context.Context, key string, v any, ttl time.Duration) error
}

// JobState 任务最近一次执行状态。FinishedAt 为 0 表示正在执行。
type JobState struct {
	StartedAt  int64  `json:"started_at"`
	FinishedAt int64  `json:"finished_at"`
	ElapsedMS  int64  `json:"elapsed_ms"`
	Error      string `json:"error"`
	Manual     bool   `json:"manual"`
}

// JobInfo 任务信息（管理端展示）。
type JobInfo struct {
	Name      string    `json:"name"`
	Spec      string    `json:"spec"`
	NoOverlap bool      `json:"no_overlap"`
	Running   bool      `json:"running"`
	Last      *JobState `json:"last"`
}

var (
	ErrJobNotFound = errors.New("任务不存在")
	ErrJobRunning  = errors.New("任务正在执行")
	ErrStopped     = errors.New("调度器已停止")
)

// Scheduler 定时任务调度器。
type Scheduler struct {
	cron    *cron.Cron
	locker  Locker
	state   StateStore
	log     *zap.Logger
	beat    func(ctx context.Context, now time.Time)
	wg      sync.WaitGroup
	mu      sync.Mutex
	ctx     context.Context
	stopped bool
	names   map[string]bool
	jobs    []Job
	now     func() time.Time
}

// New 创建调度器。beat 每分钟调用一次，用于记录"定时任务最后运行时间"，可为 nil。
// locker 同时实现 StateStore 时记录每个任务最近一次的执行状态。
func New(locker Locker, log *zap.Logger, beat func(ctx context.Context, now time.Time)) *Scheduler {
	s := &Scheduler{
		cron:   cron.New(cron.WithLocation(time.Local)),
		locker: locker,
		log:    log.Named("scheduler"),
		beat:   beat,
		ctx:    context.Background(),
		names:  map[string]bool{},
		now:    time.Now,
	}
	if st, ok := locker.(StateStore); ok && st != nil {
		s.state = st
	}
	return s
}

func (s *Scheduler) context() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ctx
}

// Add 注册任务。
func (s *Scheduler) Add(j Job) error {
	if j.Name == "" || j.Run == nil {
		return errors.New("任务名称与执行函数不能为空")
	}
	if s.names[j.Name] {
		return fmt.Errorf("任务重复注册: %s", j.Name)
	}
	if j.MaxRuntime <= 0 {
		j.MaxRuntime = time.Hour
	}
	if _, err := s.cron.AddFunc(j.Spec, func() { s.trigger(j) }); err != nil {
		return fmt.Errorf("任务 %s 的 cron 表达式无效: %w", j.Name, err)
	}
	s.names[j.Name] = true
	s.jobs = append(s.jobs, j)
	return nil
}

// trigger 每个触发时刻在所有实例中只执行一次。
func (s *Scheduler) trigger(j Job) {
	s.wg.Add(1)
	defer s.wg.Done()
	ctx := s.context()
	if ctx.Err() != nil {
		return
	}
	tick := s.now().Truncate(time.Minute).Unix()
	if _, err := s.locker.TryLock(ctx, fmt.Sprintf("cron:%s:%d", j.Name, tick), 2*time.Minute); err != nil {
		if !errors.Is(err, cache.ErrLockHeld) {
			s.log.Warn("获取任务锁失败", zap.String("job", j.Name), zap.Error(err))
		}
		return
	}
	if j.NoOverlap {
		l, err := s.locker.TryLock(ctx, "cron-running:"+j.Name, j.MaxRuntime)
		if err != nil {
			if errors.Is(err, cache.ErrLockHeld) {
				s.log.Debug("上次执行尚未结束，跳过", zap.String("job", j.Name))
			} else {
				s.log.Warn("获取任务锁失败", zap.String("job", j.Name), zap.Error(err))
			}
			return
		}
		defer func() { _ = l.Unlock(context.Background()) }()
	}
	s.run(ctx, j, false)
}

func (s *Scheduler) run(ctx context.Context, j Job, manual bool) {
	start := time.Now()
	s.saveState(ctx, j.Name, JobState{StartedAt: start.Unix(), Manual: manual})
	var err error
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
			s.log.Error("任务 panic", zap.String("job", j.Name), zap.Any("panic", r))
		} else if err != nil {
			s.log.Error("任务执行失败", zap.String("job", j.Name), zap.Error(err), zap.Duration("elapsed", time.Since(start)))
		} else {
			s.log.Debug("任务完成", zap.String("job", j.Name), zap.Duration("elapsed", time.Since(start)))
		}
		st := JobState{StartedAt: start.Unix(), FinishedAt: time.Now().Unix(), ElapsedMS: time.Since(start).Milliseconds(), Manual: manual}
		if err != nil {
			st.Error = truncate(err.Error(), 500)
		}
		s.saveState(context.WithoutCancel(ctx), j.Name, st)
	}()
	err = j.Run(ctx)
}

func (s *Scheduler) stateKey(name string) string { return s.state.Key("cron-state:" + name) }

func (s *Scheduler) saveState(ctx context.Context, name string, st JobState) {
	if s.state == nil {
		return
	}
	if err := s.state.SetJSON(ctx, s.stateKey(name), st, 0); err != nil {
		s.log.Warn("记录任务状态失败", zap.String("job", name), zap.Error(err))
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// RunNow 立即执行指定任务（命令行手动执行使用），不检查锁。
func (s *Scheduler) RunNow(ctx context.Context, j Job) {
	s.run(ctx, j, true)
}

// List 已注册的任务及最近一次执行状态（按注册顺序）。
func (s *Scheduler) List(ctx context.Context) ([]JobInfo, error) {
	now := s.now()
	list := make([]JobInfo, 0, len(s.jobs))
	for _, j := range s.jobs {
		info := JobInfo{Name: j.Name, Spec: j.Spec, NoOverlap: j.NoOverlap}
		if s.state != nil {
			var st JobState
			ok, err := s.state.GetJSON(ctx, s.stateKey(j.Name), &st)
			if err != nil {
				return nil, err
			}
			if ok {
				info.Last = &st
				// 进程异常退出时状态停留在“执行中”，超过最长运行时间后不再视为执行中
				info.Running = st.FinishedAt == 0 && now.Sub(time.Unix(st.StartedAt, 0)) < j.MaxRuntime
			}
		}
		list = append(list, info)
	}
	return list, nil
}

// Start 在后台立即执行一次指定任务（管理端手动执行），返回时任务已开始。
// 同一任务的手动执行在所有实例中互斥；设置了 NoOverlap 的任务还与定时触发的执行互斥。
func (s *Scheduler) Start(ctx context.Context, name string) error {
	i := slices.IndexFunc(s.jobs, func(j Job) bool { return j.Name == name })
	if i < 0 {
		return ErrJobNotFound
	}
	j := s.jobs[i]
	lockName := "cron-manual:" + name
	if j.NoOverlap {
		lockName = "cron-running:" + name
	}
	l, err := s.locker.TryLock(ctx, lockName, j.MaxRuntime)
	if err != nil {
		if errors.Is(err, cache.ErrLockHeld) {
			return ErrJobRunning
		}
		return err
	}
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		_ = l.Unlock(context.Background())
		return ErrStopped
	}
	s.wg.Add(1)
	runCtx := s.ctx
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		defer func() { _ = l.Unlock(context.Background()) }()
		s.run(runCtx, j, true)
	}()
	return nil
}

// Run 启动调度，阻塞直到 ctx 结束，退出前等待正在执行的任务完成。
func (s *Scheduler) Run(ctx context.Context) {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
	if s.beat != nil {
		_, _ = s.cron.AddFunc("* * * * *", func() { s.beat(ctx, time.Now()) })
		s.beat(ctx, time.Now())
	}
	s.cron.Start()
	s.log.Info("定时任务已启动", zap.Int("jobs", len(s.names)))
	<-ctx.Done()
	s.mu.Lock()
	s.stopped = true
	s.mu.Unlock()
	<-s.cron.Stop().Done()
	s.wg.Wait()
}
