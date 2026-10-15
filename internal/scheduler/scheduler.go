// Package scheduler 是进程内唯一的定时任务调度器。
//
// 需求要求「定时任务集中在调度器里，不在入口裸起 goroutine 定时循环」：需要周期执行的任务
// 统一通过本调度器登记，入口只负责启动与停止。实现用单次 time.AfterFunc 自续期（不是裸
// `for { time.Sleep }` 循环），每次 Schedule 用代数（gen）作废旧回调，天然防重入与 ABA。
package scheduler

import (
	"sync"
	"time"
)

// Scheduler 是周期任务调度器。
type Scheduler struct {
	mu      sync.Mutex
	timer   *time.Timer
	fn      func()
	every   time.Duration
	gen     int
	stopped bool
}

// New 构造调度器。
func New() *Scheduler { return &Scheduler{} }

// Schedule 设置周期任务：every<=0 表示停止（等价于旧实现的「0 秒关闭采集」）。
// 重复调用会作废旧计划并以新周期重排（等价于旧实现的 reschedule）。
func (s *Scheduler) Schedule(every time.Duration, fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gen++
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.fn = fn
	s.every = every
	if every <= 0 {
		return
	}
	gen := s.gen
	s.timer = time.AfterFunc(every, func() { s.fire(gen) })
}

// Stop 永久停止调度器（进程退出时调用）。
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
	s.gen++
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
}

// fire 执行一轮任务并自续期；被作废的代直接返回。
func (s *Scheduler) fire(gen int) {
	s.mu.Lock()
	if s.stopped || gen != s.gen || s.fn == nil {
		s.mu.Unlock()
		return
	}
	fn := s.fn
	every := s.every
	s.mu.Unlock()

	fn()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped || gen != s.gen || s.fn == nil {
		return
	}
	s.timer = time.AfterFunc(every, func() { s.fire(gen) })
}
