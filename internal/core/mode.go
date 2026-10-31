package core

import (
	"context"
	"path/filepath"
	"time"
)

// SetTakeovers is called synchronously at registration changes. It only takes
// the agent lock: it must not call back into the RPC server or perform IO.
func (a *Agent) SetTakeovers(owners []Lease) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.owners = owners
	if a.current != nil && !a.eligibleLocked(*a.current) {
		a.current.cancel()
	}
	select {
	case pending := <-a.pending:
		if !a.eligibleLocked(pending) {
			drop(pending)
		} else {
			a.pending <- pending
		}
	default:
	}
}

func (a *Agent) coveredLocked(target Target) bool {
	now := time.Now()
	for _, owner := range a.owners {
		if now.Before(owner.Expires) && CoversIdentity(owner.Programs, target.Identity()) {
			return true
		}
	}
	return false
}

func (a *Agent) eligibleLocked(r request) bool {
	if r.session == "" {
		return !a.coveredLocked(r.target)
	}
	for _, owner := range a.owners {
		if owner.SessionID == r.session && time.Now().Before(owner.Expires) {
			return CoversIdentity(owner.Programs, r.target.Identity())
		}
	}
	return false
}

// SubmitPlugin bypasses ordinary rules. Session ownership and global pause
// still apply, and both plugin and activation requests use the same worker.
func (a *Agent) SubmitPlugin(ctx context.Context, t Target, session string, mode Mode) ModeResult {
	skipped := func(code, message string) ModeResult {
		return ModeResult{Code: code, Outcome: Outcome{At: time.Now(), Status: "skipped", Message: message}}
	}
	if t.Valid == nil || !t.Valid() {
		return skipped("focus_changed", "目标已失焦")
	}
	a.mu.Lock()
	if a.root.Err() != nil || ctx.Err() != nil {
		a.mu.Unlock()
		return skipped("timeout", "请求已结束")
	}
	if a.cfg.Paused {
		a.mu.Unlock()
		return skipped("skipped_paused", "自动切换已暂停")
	}
	req := request{target: t, mode: mode, session: session, revision: a.cfg.Revision, name: filepath.Base(t.Path)}
	if t.BundleID != "" {
		req.name = t.BundleID
	}
	if mode != English && mode != Chinese {
		a.mu.Unlock()
		return skipped("invalid_request", "无效目标模式")
	}
	if !a.eligibleLocked(req) {
		a.mu.Unlock()
		return skipped("scope_not_owned", "会话未接管目标程序")
	}

	// 新请求替换旧任务；原生操作始终由单个工作线程串行执行。
	a.generation++
	if a.cancel != nil {
		a.cancel()
	}
	select {
	case old := <-a.pending:
		drop(old)
	default:
	}
	op, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	req.ctx, req.cancel = op, cancel
	req.token = a.generation
	req.reply = make(chan ModeResult, 1)
	a.current = &req
	stop := context.AfterFunc(a.root, cancel)
	a.pending <- req
	a.mu.Unlock()
	defer stop()

	select {
	case result := <-req.reply:
		return result
	case <-ctx.Done():
		cancel()
		return skipped("timeout", "请求已取消或超时")
	}
}
