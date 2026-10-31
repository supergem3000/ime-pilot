package core

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type ServiceOptions struct {
	Store    ConfigStore
	Signals  SignalSource
	Validate func(Config) error
	Create   func(Config, func() bool, uintptr) (Switcher, error)
}

type PluginEndpoint interface {
	Snapshot() Status
	Close()
}

type Dashboard struct {
	Config       Config  `json:"config"`
	ConfigPath   string  `json:"configPath"`
	Error        string  `json:"error"`
	MonitorError string  `json:"monitorError"`
	Recent       Outcome `json:"recent"`
	RPC          Status  `json:"rpc"`
	RPCError     string  `json:"rpcError"`
}

type Service struct {
	opts                   ServiceOptions
	root                   context.Context
	Agent                  *Agent
	operation              sync.Mutex
	saveMu                 sync.Mutex
	mu                     sync.Mutex
	manualCancel           context.CancelFunc
	endpoint               PluginEndpoint
	monitorError, rpcError string
	monitorDone            chan struct{}
	changed                func()
}

func NewService(ctx context.Context, opts ServiceOptions) *Service {
	s := &Service{root: ctx, opts: opts, monitorDone: make(chan struct{})}
	c, _ := opts.Store.Snapshot()
	s.Agent = NewAgent(ctx, c, &s.operation, func(valid func() bool) (Switcher, error) {
		c, err := opts.Store.Snapshot()
		if err != nil {
			return nil, err
		}
		return opts.Create(c, valid, 0)
	}, s.Notify)
	return s
}
func (s *Service) SetChanged(f func()) { s.mu.Lock(); s.changed = f; s.mu.Unlock() }
func (s *Service) Notify() {
	s.mu.Lock()
	f := s.changed
	s.mu.Unlock()
	if f != nil {
		f()
	}
}
func (s *Service) Config() Config { c, _ := s.opts.Store.Snapshot(); return c }
func (s *Service) GetStatus() Dashboard {
	c, err := s.opts.Store.Snapshot()
	d := Dashboard{Config: c, ConfigPath: s.opts.Store.Path(), Recent: s.Agent.Recent()}
	if err != nil {
		d.Error = err.Error()
	}
	s.mu.Lock()
	d.MonitorError = s.monitorError
	d.RPCError = s.rpcError
	endpoint := s.endpoint
	s.mu.Unlock()
	if endpoint != nil {
		d.RPC = endpoint.Snapshot()
	}
	return d
}
func (s *Service) ListApps() ([]App, error) { return s.opts.Signals.Apps() }
func (s *Service) SaveConfig(c Config) (Dashboard, error) {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	if err := s.opts.Validate(c); err != nil {
		return s.GetStatus(), err
	}
	// Serialize publication with actual native operations. Queued work is
	// invalidated before the lock is released, so it cannot use a new scheme.
	s.operation.Lock()
	next, err := s.opts.Store.Save(c)
	if err == nil {
		s.Agent.Update(next)
	}
	s.operation.Unlock()
	if err != nil {
		return s.GetStatus(), err
	}
	s.Notify()
	return s.GetStatus(), nil
}
func (s *Service) SetPaused(paused bool) (Dashboard, error) {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	c, err := s.opts.Store.Snapshot()
	if err != nil {
		return s.GetStatus(), err
	}
	c.Paused = paused
	next, err := s.opts.Store.Save(c)
	if err != nil {
		return s.GetStatus(), err
	}
	s.Agent.Update(next)
	s.Notify()
	return s.GetStatus(), nil
}
func (s *Service) StartSignals() {
	go func() {
		defer close(s.monitorDone)
		err := s.opts.Signals.Run(s.root, s.Agent.Invalidate, func(e Activation) { s.Agent.Observe(e.Target, e.Token, e.Baseline) })
		if s.root.Err() == nil {
			s.Agent.Invalidate()
			s.mu.Lock()
			s.monitorError = "前台监听已停止，请重启 IME Pilot"
			if err != nil {
				s.monitorError = err.Error()
			}
			s.mu.Unlock()
			s.Notify()
		}
	}()
}
func (s *Service) SessionOptions() SessionOptions {
	return SessionOptions{Config: s.Config, Capture: s.opts.Signals.Capture, Execute: s.Agent.SubmitPlugin, Publish: s.Agent.SetTakeovers, Changed: s.Notify}
}
func (s *Service) SetEndpoint(endpoint PluginEndpoint, err error) {
	s.mu.Lock()
	s.endpoint = endpoint
	if err != nil {
		s.endpoint = nil
		s.rpcError = err.Error()
	}
	s.mu.Unlock()
	s.Notify()
}
func (s *Service) Close() {
	s.CancelManual()
	s.mu.Lock()
	endpoint := s.endpoint
	s.mu.Unlock()
	if endpoint != nil {
		endpoint.Close()
	}
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-s.Agent.Done():
	case <-timer.C:
		return
	}
	select {
	case <-s.monitorDone:
	case <-timer.C:
	}
}
func (s *Service) CancelManual() {
	s.mu.Lock()
	if s.manualCancel != nil {
		s.manualCancel()
	}
	s.mu.Unlock()
}

type Reply struct {
	State   State  `json:"state"`
	Message string `json:"message"`
	OK      bool   `json:"ok"`
}

func (s *Service) Perform(call context.Context, mode string, window uintptr) Reply {
	if window == 0 {
		return Reply{Message: "设置窗口尚未就绪"}
	}
	if mode != "inspect" && mode != string(Chinese) && mode != string(English) {
		return Reply{Message: "无效操作"}
	}
	if !s.operation.TryLock() {
		return Reply{Message: "另一次切换正在执行"}
	}
	defer s.operation.Unlock()
	ctx, cancel := context.WithTimeout(s.root, 500*time.Millisecond)
	defer cancel()
	stop := context.AfterFunc(call, cancel)
	defer stop()
	s.mu.Lock()
	s.manualCancel = cancel
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.manualCancel = nil; s.mu.Unlock() }()
	valid := func() bool { return ctx.Err() == nil }
	c, err := s.opts.Store.Snapshot()
	if err != nil {
		return Reply{Message: err.Error()}
	}
	instance, err := s.opts.Create(c, valid, window)
	if err != nil {
		return Reply{Message: err.Error()}
	}
	if mode == "inspect" {
		inspector, ok := instance.(Inspector)
		if !ok {
			return Reply{Message: "当前方案不提供状态查询"}
		}
		state, err := inspector.Inspect()
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			return Reply{Message: err.Error()}
		}
		return Reply{State: state, OK: true, Message: "已查询方案状态；请核对实际输入效果。"}
	}
	result, verified, err := Switch(ctx, instance, Mode(mode))
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return Reply{Message: fmt.Sprintf("未确认切换成功：%v", err)}
	}
	if !verified {
		return Reply{Message: "切换调用已完成，方案不提供状态回读。"}
	}
	return Reply{State: result.After, OK: true, Message: "方案已确认目标状态；请核对实际输入效果。"}
}
