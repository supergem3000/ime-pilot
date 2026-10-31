// Package automation schedules event-driven application activation requests.
package automation

import (
	"context"
	"errors"
	"sync"
	"time"

	"ime-pilot/internal/config"
	"ime-pilot/internal/core"
	"ime-pilot/internal/takeover"
)

type Target struct {
	BundleID   string
	Window     uintptr
	PID        uint32
	Path       string
	At         time.Time
	HistoryGap bool
	// Valid verifies the system's foreground generation, including ABA focus changes.
	Valid func() bool
}

func (t Target) Identity() takeover.Identity {
	return takeover.Identity{Path: t.Path, BundleID: t.BundleID}
}

type Outcome struct {
	At      time.Time `json:"at"`
	App     string    `json:"app"`
	Status  string    `json:"status"`
	Message string    `json:"message"`
}
type request struct {
	mode     core.Mode
	session  string
	target   Target
	token    uint64
	revision uint64
	name     string
	ctx      context.Context
	cancel   context.CancelFunc
	reply    chan ModeResult
	result   core.Result
	code     string
}
type ModeResult struct {
	Outcome Outcome
	Code    string
	Before  core.State
	After   core.State
}

func drop(r request) {
	r.cancel()
	if r.reply != nil {
		r.reply <- ModeResult{Outcome: Outcome{At: time.Now(), App: r.name, Status: "skipped", Message: "请求已失效"}, Code: "focus_changed"}
	}
}

type Agent struct {
	owners       []takeover.Lease
	current      *request
	mu           sync.Mutex
	cfg          config.Config
	generation   uint64
	lastPath     string
	lastObserved uint64
	last         Outcome
	lastToken    uint64
	cancel       context.CancelFunc
	pending      chan request
	root         context.Context
	operation    *sync.Mutex
	factory      func(Target) core.Backend
	changed      func()
	done         chan struct{}
}

func New(ctx context.Context, c config.Config, operation *sync.Mutex, factory func(Target) core.Backend, changed func()) *Agent {
	c.Rules = append([]config.Rule{}, c.Rules...)
	a := &Agent{cfg: c, pending: make(chan request, 1), root: ctx, operation: operation, factory: factory, changed: changed, done: make(chan struct{})}
	go a.run()
	return a
}

// Invalidate is fast enough for a native event callback; it never does IO.
func (a *Agent) Invalidate() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.generation++
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	select {
	case r := <-a.pending:
		drop(r)
	default:
	}
	return a.generation
}
func (a *Agent) Update(c config.Config) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c.Rules = append([]config.Rule{}, c.Rules...)
	a.cfg = c
	a.generation++
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	select {
	case r := <-a.pending:
		drop(r)
	default:
	}
}
func (a *Agent) Recent() Outcome       { a.mu.Lock(); defer a.mu.Unlock(); return a.last }
func (a *Agent) Done() <-chan struct{} { return a.done }

// Observe consumes server-captured identities, never paths supplied by the page.
func (a *Agent) Observe(t Target, token uint64, baseline bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if token <= a.lastObserved || a.root.Err() != nil {
		return
	}
	a.lastObserved = token
	path := t.Identity().Key()
	previous := a.lastPath
	a.lastPath = path
	if t.HistoryGap {
		previous = ""
	}
	if token != a.generation || !t.Valid() {
		return
	}
	if baseline || path == "" || path == previous || a.cfg.Paused || a.coveredLocked(t) {
		return
	}
	for _, r := range a.cfg.Rules {
		if !r.Enabled || !r.OnActivation() || r.Identity().Key() != path {
			continue
		}
		ctx, cancel := context.WithCancel(a.root)
		a.cancel = cancel
		req := request{target: t, mode: r.ActivationMode(), token: token, revision: a.cfg.Revision, name: r.Name, ctx: ctx, cancel: cancel}
		a.current = &req
		select {
		case old := <-a.pending:
			drop(old)
		default:
		}
		a.pending <- req
		return
	}
}
func (a *Agent) valid(r request) bool {
	a.mu.Lock()
	ok := a.generation == r.token && a.cfg.Revision == r.revision && !a.cfg.Paused && a.eligibleLocked(r)
	a.mu.Unlock()
	return ok && r.ctx.Err() == nil && r.target.Valid()
}
func (a *Agent) record(r request, status, message string) {
	code := r.code
	if code == "" {
		switch status {
		case "skipped":
			code = "focus_changed"
		case "failed":
			code = "native_error"
		default:
			code = status
		}
	}
	out := Outcome{time.Now(), r.name, status, message}
	if r.reply != nil {
		r.reply <- ModeResult{Outcome: out, Code: code, Before: r.result.Before, After: r.result.After}
	}
	a.mu.Lock()
	if r.token < a.lastToken {
		a.mu.Unlock()
		return
	}
	a.lastToken = r.token
	a.last = out
	a.mu.Unlock()
	if a.changed != nil {
		a.changed()
	}
}
func (a *Agent) run() {
	defer close(a.done)
	for {
		select {
		case <-a.root.Done():
			a.Invalidate()
			return
		case r := <-a.pending:
			a.execute(r)
		}
	}
}
func (a *Agent) execute(r request) {
	defer r.cancel()
	// Allow activation to settle before capturing an editor's native focus.
	timer := time.NewTimer(60 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-r.ctx.Done():
		a.record(r, "skipped", "请求已取消或目标已失焦")
		return
	case <-timer.C:
	}
	if !a.valid(r) {
		a.record(r, "skipped", "目标或配置已变化，旧请求已跳过")
		return
	}
	if time.Since(r.target.At) > 300*time.Millisecond {
		r.code = "skipped_stale"
		a.record(r, "skipped", "事件已过期，未执行切换")
		return
	}
	if !a.operation.TryLock() {
		r.code = "skipped_busy"
		a.record(r, "skipped", "另一次切换正在执行，本次已跳过")
		return
	}
	defer a.operation.Unlock()
	ctx, cancel := context.WithTimeout(r.ctx, 500*time.Millisecond)
	defer cancel()
	// 原生后端每次核实目标时也检查所有者，防止排队期间接管关系改变。
	target := r.target
	target.Valid = func() bool { return a.valid(r) }
	backend := a.factory(target)
	result, err := core.NewController(backend).Switch(ctx, r.mode)
	r.result = result
	if !a.valid(r) {
		a.record(r, "skipped", "目标或配置已变化，结果不再确认")
		return
	}
	if err != nil {
		switch {
		case errors.Is(err, core.ErrUnsupported):
			r.code = "unsupported_mode"
		case errors.Is(err, context.DeadlineExceeded):
			r.code = "timeout"
		case errors.Is(err, core.ErrVerification):
			r.code = "verification_failed"
		}
		status := "failed"
		if errors.Is(err, core.ErrFocusChanged) || errors.Is(err, context.Canceled) {
			status = "skipped"
		}
		a.record(r, status, err.Error())
		return
	}
	if result.Changed {
		message := "已查询确认输入法模式：" + string(r.mode)
		if _, ok := backend.(core.SourceSelector); ok {
			message = "已确认系统输入源：" + result.After.Description
		}
		a.record(r, "changed", message)
	} else {
		a.record(r, "already_selected", "已是目标模式，没有重复切换")
	}
}
