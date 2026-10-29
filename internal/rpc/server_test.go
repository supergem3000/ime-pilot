package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ime-pilot/internal/automation"
	"ime-pilot/internal/config"
	"ime-pilot/internal/core"
	"ime-pilot/internal/takeover"
)

func testServer(t *testing.T, execute func(context.Context, automation.Target, string, core.Mode) automation.ModeResult) *Server {
	t.Helper()
	if execute == nil {
		execute = func(_ context.Context, _ automation.Target, _ string, mode core.Mode) automation.ModeResult {
			return automation.ModeResult{Code: "changed", After: core.State{Mode: mode}}
		}
	}
	s, err := Start(context.Background(), t.TempDir(), Options{
		Config: func() config.Config { return config.Config{Revision: 7} },
		Capture: func(e Evidence) (automation.Target, error) {
			if e.PID != 123 || e.WindowID != "42" {
				return automation.Target{}, core.ErrFocusChanged
			}
			return automation.Target{PID: e.PID, Window: 42, Path: "editor.exe", Valid: func() bool { return true }}, nil
		},
		Execute: execute,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func (s *Server) testCall(t *testing.T, id, method string, p any) Response {
	t.Helper()
	raw, _ := json.Marshal(p)
	request := Request{RequestID: id, Method: method, Params: raw}
	data, _ := json.Marshal(request)
	req, _ := http.NewRequest("POST", "http://"+s.host+"/api/rpc", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.discovery.Token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("HTTP %d", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || len(fields) != 3 || fields["code"] == nil || fields["data"] == nil || fields["message"] == nil {
		t.Fatalf("invalid response envelope: %s", body)
	}
	var response Response
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func registrationParams(plugin, instance string, programs ...takeover.Program) registration {
	if len(programs) == 0 {
		programs = []takeover.Program{{Kind: "windows.executableName", Value: "editor.exe"}}
	}
	return registration{PluginID: plugin, Name: plugin, Version: "test", InstanceID: instance, Programs: programs}
}

func connect(t *testing.T, s *Server, instance string) string {
	t.Helper()
	r := s.testCall(t, "register-"+instance, "registerPlugin", registrationParams("test.plugin", instance))
	if r.Code != "ok" {
		t.Fatal(r)
	}
	return r.Data.(map[string]any)["sessionId"].(string)
}

func switchParams(session string, mode core.Mode) map[string]any {
	return map[string]any{"sessionId": session, "targetMode": mode, "target": Evidence{PID: 123, WindowID: "42"}}
}

func TestHTTPBoundary(t *testing.T) {
	s := testServer(t, nil)
	for _, tc := range []struct {
		name   string
		status int
		change func(*http.Request)
	}{
		{"auth", 401, func(r *http.Request) { r.Header.Del("Authorization") }},
		{"origin", 403, func(r *http.Request) { r.Header.Set("Origin", "http://example.test") }},
		{"host", 403, func(r *http.Request) { r.Host = "localhost:123" }},
		{"media", 415, func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }},
		{"oversize", 413, func(r *http.Request) {
			r.Body = io.NopCloser(strings.NewReader(strings.Repeat("x", maxBody+1)))
			r.ContentLength = int64(maxBody + 1)
		}},
		{"unknown field", 400, func(r *http.Request) {
			r.Body = io.NopCloser(strings.NewReader(`{"requestId":"a","method":"renewSession","params":{},"unexpected":1}`))
			r.ContentLength = -1
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest("POST", "http://"+s.host+"/api/rpc", strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+s.discovery.Token)
			tc.change(req)
			response, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			var body map[string]any
			if response.StatusCode != tc.status || json.NewDecoder(response.Body).Decode(&body) != nil || len(body) != 3 || body["data"] != nil || body["code"] == nil || body["message"] == nil {
				t.Fatalf("unexpected HTTP response: %d %v", response.StatusCode, body)
			}
		})
	}
}

func TestRegistrationAndLeaseSemantics(t *testing.T) {
	s := testServer(t, nil)
	a := connect(t, s, "instance-a")
	if duplicate := connect(t, s, "instance-a"); duplicate != a {
		t.Fatal("duplicate session created")
	}
	b := connect(t, s, "instance-b")
	if a == b || s.Snapshot().Clients != 2 {
		t.Fatal("instances not independent")
	}

	conflict := registrationParams("other.plugin", "other")
	if r := s.testCall(t, "conflict", "registerPlugin", conflict); r.Code != "scope_conflict" {
		t.Fatal(r)
	}
	if s.Snapshot().Clients != 2 {
		t.Fatal("partial registration escaped")
	}

	changed := registrationParams("test.plugin", "instance-a")
	changed.Name = "changed"
	if r := s.testCall(t, "modified", "registerPlugin", changed); r.Code != "registration_conflict" {
		t.Fatal(r)
	}

	if r := s.testCall(t, "bye-a", "unregisterPlugin", map[string]any{"sessionId": a}); r.Code != "ok" {
		t.Fatal(r)
	}
	if s.Snapshot().Clients != 1 {
		t.Fatal("other instance lost")
	}
	if r := s.testCall(t, "bye-again", "unregisterPlugin", map[string]any{"sessionId": a}); r.Code != "ok" {
		t.Fatal(r)
	}
	if r := s.testCall(t, "renew-a", "renewSession", map[string]any{"sessionId": a}); r.Code != "session_expired" {
		t.Fatal(r)
	}

	s.mu.Lock()
	s.sessions[b].Expires = time.Now().Add(-time.Second)
	s.mu.Unlock()
	// 即使尚未运行定时清理，也不能续期已经到期的会话。
	if r := s.testCall(t, "renew-b", "renewSession", map[string]any{"sessionId": b}); r.Code != "session_expired" {
		t.Fatal(r)
	}
	if fresh := connect(t, s, "instance-b"); fresh == b {
		t.Fatal("expired identity restored")
	}
}

func TestSwitchModesDedupAndTargetValidation(t *testing.T) {
	var calls atomic.Int32
	s := testServer(t, func(_ context.Context, target automation.Target, _ string, mode core.Mode) automation.ModeResult {
		calls.Add(1)
		if !target.Valid() {
			t.Error("invalid target")
		}
		return automation.ModeResult{Code: "changed", After: core.State{Mode: mode}}
	})
	id := connect(t, s, "instance")
	for _, mode := range []core.Mode{core.English, core.Chinese} {
		for range 2 {
			r := s.testCall(t, string(mode), "setInputMode", switchParams(id, mode))
			if r.Code != "changed" || r.Data.(map[string]any)["actualMode"] != string(mode) {
				t.Fatal(r)
			}
		}
	}
	if calls.Load() != 2 {
		t.Fatal("duplicate native switch")
	}
	if r := s.testCall(t, "english", "setInputMode", switchParams(id, core.Chinese)); r.Code != "invalid_request" {
		t.Fatal(r)
	}

	invalid := switchParams(id, core.English)
	invalid["target"] = Evidence{PID: 999, WindowID: "42"}
	if r := s.testCall(t, "wrong-target", "setInputMode", invalid); r.Code != "unsupported_context" {
		t.Fatal(r)
	}
	if r := s.testCall(t, "missing-session", "setInputMode", switchParams("missing", core.English)); r.Code != "session_expired" {
		t.Fatal(r)
	}

	other := registrationParams("test.plugin", "other", takeover.Program{Kind: "windows.executableName", Value: "other.exe"})
	registered := s.testCall(t, "register-other", "registerPlugin", other)
	otherID := registered.Data.(map[string]any)["sessionId"].(string)
	if r := s.testCall(t, "not-owned", "setInputMode", switchParams(otherID, core.English)); r.Code != "scope_not_owned" {
		t.Fatal(r)
	}
	if calls.Load() != 2 {
		t.Fatal("invalid request reached controller")
	}
}

func TestUnregisterCancelsInFlightAndDuplicateWaits(t *testing.T) {
	entered := make(chan struct{})
	var once sync.Once
	var calls atomic.Int32
	s := testServer(t, func(ctx context.Context, target automation.Target, _ string, _ core.Mode) automation.ModeResult {
		calls.Add(1)
		once.Do(func() { close(entered) })
		<-ctx.Done()
		if target.Valid() {
			t.Error("revoked context still valid")
		}
		return automation.ModeResult{Code: "focus_changed"}
	})
	id := connect(t, s, "instance")
	p, _ := json.Marshal(switchParams(id, core.English))
	r := Request{RequestID: "inflight", Method: "setInputMode", Params: p}
	done := make(chan Response, 2)
	go func() { done <- s.dispatch(r, time.Now()) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("operation not started")
	}
	go func() { done <- s.dispatch(r, time.Now()) }()
	if r := s.testCall(t, "unregister", "unregisterPlugin", map[string]any{"sessionId": id}); r.Code != "ok" {
		t.Fatal(r)
	}
	for range 2 {
		select {
		case response := <-done:
			if response.Code != "focus_changed" && response.Code != "session_expired" {
				t.Fatal(response)
			}
		case <-time.After(time.Second):
			t.Fatal("operation not cancelled")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("duplicate in-flight execution")
	}
}

func TestNamePathConflictAndAtomicRegistration(t *testing.T) {
	s := testServer(t, nil)
	exe := filepath.Join(t.TempDir(), "editor.exe")
	if err := os.WriteFile(exe, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	connect(t, s, "owner")
	p := registrationParams("other.plugin", "other",
		takeover.Program{Kind: "windows.executableName", Value: "unclaimed.exe"},
		takeover.Program{Kind: "windows.executablePath", Value: exe})
	if r := s.testCall(t, "name-path", "registerPlugin", p); r.Code != "scope_conflict" {
		t.Fatal(r)
	}
	if s.Snapshot().Clients != 1 {
		t.Fatal("partially registered unclaimed program")
	}
}

func TestRuntimeAndRestart(t *testing.T) {
	s := testServer(t, nil)
	id := connect(t, s, "instance")
	data, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	json.Unmarshal(data, &fields)
	if len(fields) != 2 || fields["port"] == nil || fields["token"] == nil {
		t.Fatal(string(data))
	}
	s.Close()
	if _, err := os.Stat(s.path); !os.IsNotExist(err) {
		t.Fatal("runtime remains")
	}
	restarted := testServer(t, nil)
	if r := restarted.testCall(t, "old-session", "renewSession", map[string]any{"sessionId": id}); r.Code != "session_expired" {
		t.Fatal(r)
	}
}
func TestRPCAndSchedulerShareOwnershipWithoutRules(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var applies atomic.Int32
	var english atomic.Bool
	cfg := config.Config{SchemaVersion: 2, Revision: 1, Rules: []config.Rule{}}
	agent := automation.New(ctx, cfg, &sync.Mutex{}, func(target automation.Target) core.Backend {
		return &integrationBackend{target: target, applies: &applies, english: &english}
	}, nil)
	s, err := Start(ctx, t.TempDir(), Options{
		Config: func() config.Config { return cfg },
		Capture: func(e Evidence) (automation.Target, error) {
			return automation.Target{Path: "editor.exe", PID: e.PID, Window: 42, Valid: func() bool { return true }}, nil
		},
		Execute: agent.SubmitPlugin, Publish: agent.SetTakeovers,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close(); cancel(); <-agent.Done() }()
	id := connect(t, s, "integrated")
	for _, mode := range []core.Mode{core.English, core.Chinese} {
		r := s.testCall(t, "integrated-"+string(mode), "setInputMode", switchParams(id, mode))
		if r.Code != "changed" {
			t.Fatal(r)
		}
	}
	if applies.Load() != 2 {
		t.Fatal("switch did not reach shared controller")
	}
	if r := s.testCall(t, "bye-integrated", "unregisterPlugin", map[string]any{"sessionId": id}); r.Code != "ok" {
		t.Fatal(r)
	}
	if r := s.testCall(t, "after-revoke", "setInputMode", switchParams(id, core.English)); r.Code != "session_expired" {
		t.Fatal(r)
	}
}

type integrationBackend struct {
	target  automation.Target
	applies *atomic.Int32
	english *atomic.Bool
}

func (b *integrationBackend) Snapshot() (core.State, error) {
	if !b.target.Valid() {
		return core.State{}, core.ErrFocusChanged
	}
	mode := core.Chinese
	if b.english.Load() {
		mode = core.English
	}
	return core.State{ContextID: "test-context", InputSourceID: "test-ime", Mode: mode}, nil
}

func (b *integrationBackend) Apply(ctx context.Context, _ core.State, mode core.Mode) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !b.target.Valid() {
		return core.ErrFocusChanged
	}
	b.applies.Add(1)
	b.english.Store(mode == core.English)
	return nil
}
