// Package core schedules event-driven application activation requests.
package core

import (
	"context"
	"errors"
	"sync"
	"time"
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

func (t Target) Identity() Identity {
	return Identity{Path: t.Path, BundleID: t.BundleID}
}

type Outcome struct {
	At      time.Time `json:"at"`
	App     string    `json:"app"`
	Status  string    `json:"status"`
	Message string    `json:"message"`
}
type request struct {
	mode     Mode
	session  string
	target   Target
	token    uint64
	revision uint64
	name     string
	ctx      context.Context
	cancel   context.CancelFunc
	reply    chan ModeResult
	result   Result
	code     string
}
type ModeResult struct {
	Outcome Outcome
	Code    string
	Before  State
	After   State
}

func drop(r request) {
	r.cancel()
	if r.reply != nil {
		r.reply <- ModeResult{Outcome: Outcome{At: time.Now(), App: r.name, Status: "skipped", Message: "请求已失效"}, Code: "focus_changed"}
	}
}

type Agent struct {
	owners       []Lease
	current      *request
	mu           sync.Mutex
	cfg          Config
	generation   uint64
	lastPath     string
	lastObserved uint64
	last         Outcome
	lastToken    uint64
	cancel       context.CancelFunc
	pending      chan request
	root         context.Context
	operation    *sync.Mutex
	factory      SwitcherFactory
	changed      func()
	done         chan struct{}
}

func NewAgent(ctx context.Context, c Config, operation *sync.Mutex, factory SwitcherFactory, changed func()) *Agent {
	c.Rules = append([]Rule{}, c.Rules...)
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
func (a *Agent) Update(c Config) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c.Rules = append([]Rule{}, c.Rules...)
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
	// A platform check may marshal to the GUI thread. Do not hold the agent
	// lock while doing so: native activation callbacks also invalidate it.
	valid := t.Valid != nil && t.Valid()
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
	if token != a.generation || !valid {
		return
	}
	if baseline || path == "" || path == previous || a.cfg.Paused || a.coveredLocked(t) {
		return
	}
	// An explicit rule takes precedence even when disabled. Only applications
	// absent from the rule list inherit the default; plugin ownership was checked above.
	mode := a.cfg.DefaultMode
	for _, r := range a.cfg.Rules {
		if r.Identity().Key() != path {
			continue
		}
		if !r.Enabled {
			return
		}
		mode = r.TargetMode
		break
	}
	if mode != English && mode != Chinese {
		return
	}
	ctx, cancel := context.WithCancel(a.root)
	a.cancel = cancel
	// Record the stable identity; the desktop resolves its display name.
	app := t.Path
	if t.BundleID != "" {
		app = t.BundleID
	}
	req := request{target: t, mode: mode, token: token, revision: a.cfg.Revision, name: app, ctx: ctx, cancel: cancel}
	a.current = &req
	select {
	case old := <-a.pending:
		drop(old)
	default:
	}
	a.pending <- req
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
	// Core retains the business target; the scheme receives only a validity guard.
	switcher, err := a.factory(func() bool { return a.valid(r) })
	var result Result
	var verified bool
	if err == nil {
		result, verified, err = Switch(ctx, switcher, r.mode)
	}
	r.result = result
	if !a.valid(r) {
		a.record(r, "skipped", "目标或配置已变化，结果不再确认")
		return
	}
	if err != nil {
		switch {
		case errors.Is(err, ErrUnsupported):
			r.code = "unsupported_mode"
		case errors.Is(err, context.DeadlineExceeded):
			r.code = "timeout"
		case errors.Is(err, ErrVerification):
			r.code = "verification_failed"
		}
		status := "failed"
		if errors.Is(err, ErrFocusChanged) || errors.Is(err, context.Canceled) {
			status = "skipped"
		}
		a.record(r, status, err.Error())
		return
	}
	if !verified {
		a.record(r, "applied", "切换调用已完成，方案不提供状态回读")
		return
	}
	if result.Changed {
		message := "方案已确认目标状态：" + result.After.Description
		a.record(r, "changed", message)
	} else {
		a.record(r, "already_selected", "已是目标模式，没有重复切换")
	}
}
