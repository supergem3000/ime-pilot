package rpc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"ime-pilot/internal/automation"
	"ime-pilot/internal/config"
	"ime-pilot/internal/core"
	"ime-pilot/internal/takeover"
)

const maxBody = 65536
const sessionLease = 15 * time.Second

var requestID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var pluginID = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

type Evidence struct {
	PID      uint32 `json:"pid"`
	WindowID string `json:"windowId"`
}

type Options struct {
	Config  func() config.Config
	Capture func(Evidence) (automation.Target, error)
	Execute func(context.Context, automation.Target, string, core.Mode) automation.ModeResult
	// Publish only updates the scheduler's in-memory ownership snapshot.
	// It runs under the registry lock and must never call back into this server.
	Publish func([]takeover.Lease)
	Changed func()
}

type Request struct {
	RequestID string          `json:"requestId"`
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params"`
}

type Response struct {
	Code    string `json:"code"`
	Data    any    `json:"data"`
	Message string `json:"message"`
}

type registration struct {
	PluginID   string             `json:"pluginId"`
	Name       string             `json:"name"`
	Version    string             `json:"version"`
	InstanceID string             `json:"instanceId"`
	Programs   []takeover.Program `json:"programs"`
}

type session struct {
	ID string
	registration
	Expires time.Time
	Context context.Context
	Cancel  context.CancelFunc
}

type cached struct {
	Hash     [32]byte
	Done     chan struct{}
	Response Response
	At       time.Time
}

type PluginStatus struct {
	PluginID string             `json:"pluginId"`
	Name     string             `json:"name"`
	Programs []takeover.Program `json:"programs"`
}

type Status struct {
	Listening bool           `json:"listening"`
	Clients   int            `json:"clients"`
	LastEvent string         `json:"lastEvent"`
	Plugins   []PluginStatus `json:"plugins"`
}

type Server struct {
	mu         sync.Mutex
	http       *http.Server
	listener   net.Listener
	root       context.Context
	cancel     context.CancelFunc
	stopped    chan struct{}
	host, path string
	discovery  discovery
	opts       Options
	sessions   map[string]*session
	cache      map[string]*cached
	slots      chan struct{}
	rateAt     time.Time
	rateTokens float64
	lastEvent  string
}

func Start(ctx context.Context, dir string, opts Options) (*Server, error) {
	root, cancel := context.WithCancel(ctx)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		cancel()
		return nil, err
	}
	s := &Server{
		listener: listener, root: root, cancel: cancel, stopped: make(chan struct{}),
		host: listener.Addr().String(), path: filepath.Join(dir, "runtime.json"), opts: opts,
		sessions: map[string]*session{}, cache: map[string]*cached{},
		slots: make(chan struct{}, 16), rateAt: time.Now(), rateTokens: 40,
	}
	s.discovery = discovery{Port: listener.Addr().(*net.TCPAddr).Port, Token: randomID() + randomID()}
	s.http = &http.Server{
		Handler: http.HandlerFunc(s.handle), ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second, IdleTimeout: 30 * time.Second,
		MaxHeaderBytes: 8192,
	}
	if err = publish(s.path, s.discovery); err != nil {
		listener.Close()
		cancel()
		return nil, err
	}
	go s.http.Serve(listener)
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		defer close(s.stopped)
		for {
			select {
			case <-root.Done():
				s.close()
				return
			case <-ticker.C:
				s.expire()
			}
		}
	}()
	return s, nil
}

func (s *Server) Close() {
	s.cancel()
	<-s.stopped
}

func (s *Server) close() {
	s.mu.Lock()
	for _, client := range s.sessions {
		client.Cancel()
	}
	s.sessions = map[string]*session{}
	s.publishLocked()
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = s.http.Shutdown(ctx)
	_ = s.http.Close()
	// 只清理属于本次启动的发现文件，避免删除其他主体发布的信息。
	data, err := os.ReadFile(s.path)
	var d discovery
	if err == nil && json.Unmarshal(data, &d) == nil && d.Token == s.discovery.Token {
		_ = os.Remove(s.path)
	}
	s.mu.Lock()
	s.cache = map[string]*cached{}
	s.mu.Unlock()
}

func (s *Server) Snapshot() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := Status{Listening: s.root.Err() == nil, LastEvent: s.lastEvent, Plugins: []PluginStatus{}}
	for _, client := range s.sessions {
		if time.Now().Before(client.Expires) {
			out.Clients++
			out.Plugins = append(out.Plugins, PluginStatus{client.PluginID, client.Name, append([]takeover.Program{}, client.Programs...)})
		}
	}
	return out
}

func (s *Server) changed() {
	if s.opts.Changed != nil && s.root.Err() == nil {
		s.opts.Changed()
	}
}

// 接管与规则资格在返回注册响应前同步发布；后台清理不负责授权判断。
func (s *Server) publishLocked() {
	if s.opts.Publish == nil {
		return
	}
	owners := make([]takeover.Lease, 0, len(s.sessions))
	for _, client := range s.sessions {
		owners = append(owners, takeover.Lease{
			SessionID: client.ID, PluginID: client.PluginID, Name: client.Name,
			Programs: append([]takeover.Program{}, client.Programs...), Expires: client.Expires,
		})
	}
	s.opts.Publish(owners)
}

func (s *Server) pruneLocked() bool {
	changed := false
	now := time.Now()
	for id, client := range s.sessions {
		if !now.Before(client.Expires) {
			client.Cancel()
			delete(s.sessions, id)
			changed = true
		}
	}
	for key, entry := range s.cache {
		select {
		case <-entry.Done:
			if now.Sub(entry.At) > time.Minute {
				delete(s.cache, key)
			}
		default:
		}
	}
	if changed {
		s.publishLocked()
	}
	return changed
}

func (s *Server) expire() {
	s.mu.Lock()
	changed := s.pruneLocked()
	s.mu.Unlock()
	if changed {
		s.changed()
	}
}

func strict(data []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errors.New("extra JSON")
	}
	return nil
}

func result(code, message string, data any) Response {
	return Response{Code: code, Data: data, Message: message}
}

func (s *Server) validLocked(client *session) bool {
	return client != nil && s.sessions[client.ID] == client && time.Now().Before(client.Expires) && s.root.Err() == nil
}

// dispatch serializes registration/lease changes, but releases the registry
// lock while a switch runs. Duplicate in-flight requests wait for its result.
func (s *Server) dispatch(r Request, received time.Time) Response {
	var reg registration
	var basic struct {
		SessionID string `json:"sessionId"`
	}
	switch r.Method {
	case "registerPlugin":
		if strict(r.Params, &reg) != nil || !pluginID.MatchString(reg.PluginID) || !requestID.MatchString(reg.InstanceID) || reg.Name == "" || len(reg.Name) > 160 || reg.Version == "" || len(reg.Version) > 64 {
			return result("invalid_request", "插件注册信息无效", nil)
		}
		programs, err := takeover.Normalize(reg.Programs)
		if err != nil {
			return result("scope_unverifiable", err.Error(), nil)
		}
		reg.Programs = programs
	case "renewSession", "unregisterPlugin":
		if strict(r.Params, &basic) != nil || basic.SessionID == "" {
			return result("invalid_request", "缺少有效会话标识", nil)
		}
	case "setInputMode":
		if json.Unmarshal(r.Params, &basic) != nil || basic.SessionID == "" {
			return result("invalid_request", "缺少有效会话标识", nil)
		}
	default:
		return result("invalid_request", "未知接口", nil)
	}
	// Canonical JSON ignores object key order when comparing duplicate content.
	var params any
	if json.Unmarshal(r.Params, &params) != nil {
		return result("invalid_request", "参数格式无效", nil)
	}
	canonical, _ := json.Marshal([]any{r.Method, params})
	hash := sha256.Sum256(canonical)

	s.mu.Lock()
	pruned := s.pruneLocked()
	defer func() {
		if pruned {
			s.changed()
		}
	}()
	client := s.sessions[basic.SessionID]
	if r.Method != "registerPlugin" && r.Method != "unregisterPlugin" && !s.validLocked(client) {
		s.mu.Unlock()
		return result("session_expired", "会话已失效，请重新注册", nil)
	}
	if entry := s.cache[r.RequestID]; entry != nil {
		if entry.Hash != hash {
			s.mu.Unlock()
			return result("invalid_request", "同一请求标识不能改变内容", nil)
		}
		// 失效的注册缓存不能复活旧会话；重新注册建立全新身份。
		if r.Method == "registerPlugin" {
			select {
			case <-entry.Done:
				if data, ok := entry.Response.Data.(map[string]any); ok {
					id, _ := data["sessionId"].(string)
					if s.sessions[id] == nil {
						delete(s.cache, r.RequestID)
						entry = nil
					}
				}
			default:
			}
		}
		if entry != nil {
			s.mu.Unlock()
			select {
			case <-entry.Done:
				return entry.Response
			case <-s.root.Done():
				return result("timeout", "主体正在退出", nil)
			}
		}
	}
	if len(s.cache) >= 1024 {
		s.mu.Unlock()
		return result("rate_limited", "请求记录已满，请稍后重试", nil)
	}
	entry := &cached{Hash: hash, Done: make(chan struct{})}
	s.cache[r.RequestID] = entry
	var response Response
	switch r.Method {
	case "registerPlugin":
		response = s.registerLocked(reg)
	case "renewSession":
		client.Expires = time.Now().Add(sessionLease)
		s.publishLocked()
		response = s.leaseResult(client, false)
	case "unregisterPlugin":
		if client != nil {
			client.Cancel()
			delete(s.sessions, client.ID)
			s.publishLocked()
		}
		response = result("ok", "已注销", nil)
	case "setInputMode":
		s.mu.Unlock()
		response = s.switchMode(r.Params, client, received)
		s.mu.Lock()
	}
	entry.At, entry.Response = time.Now(), response
	close(entry.Done)
	s.lastEvent = r.Method + " / " + response.Code
	s.mu.Unlock()
	s.changed()
	return response
}

func (s *Server) leaseResult(client *session, includeID bool) Response {
	data := map[string]any{"leaseMs": 15000, "renewAfterMs": 5000, "paused": s.opts.Config().Paused}
	if includeID {
		data["sessionId"] = client.ID
	}
	return result("ok", "会话有效", data)
}

func (s *Server) registerLocked(reg registration) Response {
	for _, client := range s.sessions {
		if client.PluginID == reg.PluginID && client.InstanceID == reg.InstanceID {
			left, _ := json.Marshal(client.registration)
			right, _ := json.Marshal(reg)
			if !bytes.Equal(left, right) {
				return result("registration_conflict", "同一插件实例的注册内容不同", nil)
			}
			return s.leaseResult(client, true)
		}
		if client.PluginID != reg.PluginID {
			for _, existing := range client.Programs {
				for _, candidate := range reg.Programs {
					if takeover.Overlaps(existing, candidate) {
						return result("scope_conflict", "程序已被其他插件接管："+client.Name, nil)
					}
				}
			}
		}
	}
	if len(s.sessions) >= 32 {
		return result("rate_limited", "插件会话数量已达上限", nil)
	}
	ctx, cancel := context.WithCancel(s.root)
	client := &session{ID: randomID(), registration: reg, Expires: time.Now().Add(sessionLease), Context: ctx, Cancel: cancel}
	s.sessions[client.ID] = client
	s.publishLocked()
	return s.leaseResult(client, true)
}

func (s *Server) switchMode(raw []byte, client *session, received time.Time) Response {
	var p struct {
		SessionID  string    `json:"sessionId"`
		TargetMode core.Mode `json:"targetMode"`
		Target     Evidence  `json:"target"`
	}
	if strict(raw, &p) != nil || p.Target.PID == 0 || p.Target.WindowID == "" || (p.TargetMode != core.English && p.TargetMode != core.Chinese) {
		return result("invalid_request", "切换参数无效", nil)
	}
	if s.opts.Config().Paused {
		return result("skipped_paused", "自动切换已暂停", nil)
	}
	target, err := s.opts.Capture(p.Target)
	if err != nil || target.Valid == nil || !target.Valid() {
		return result("unsupported_context", "无法核实前台目标窗口", nil)
	}
	if !takeover.CoversIdentity(client.Programs, target.Identity()) {
		return result("scope_not_owned", "会话未接管该程序", nil)
	}
	target.At = received
	original := target.Valid
	target.Valid = func() bool {
		s.mu.Lock()
		valid := s.validLocked(client)
		s.mu.Unlock()
		return valid && original()
	}
	ctx, cancel := context.WithDeadline(client.Context, received.Add(800*time.Millisecond))
	defer cancel()
	if time.Since(received) > 300*time.Millisecond {
		return result("skipped_stale", "主体排队已超时", nil)
	}
	outcome := s.opts.Execute(ctx, target, client.ID, p.TargetMode)
	if outcome.Code == "changed" || outcome.Code == "already_selected" {
		if outcome.After.Mode != p.TargetMode || !target.Valid() {
			return result("verification_failed", "未确认目标模式或目标已失焦", nil)
		}
		return result(outcome.Code, outcome.Outcome.Message, map[string]any{"targetMode": p.TargetMode, "actualMode": outcome.After.Mode})
	}
	return result(outcome.Code, outcome.Outcome.Message, nil)
}
