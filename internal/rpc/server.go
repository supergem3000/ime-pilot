package rpc

import (
	"context"
	"encoding/json"
	"ime-pilot/internal/core"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const maxBody = 65536

type Options = core.SessionOptions
type Evidence = core.Evidence
type Request = core.Request
type Response = core.Response
type Status = core.Status
type Server struct {
	mu         sync.Mutex
	http       *http.Server
	listener   net.Listener
	root       context.Context
	cancel     context.CancelFunc
	stopped    chan struct{}
	host, path string
	discovery  discovery
	registry   *core.Sessions
	slots      chan struct{}
	rateAt     time.Time
	rateTokens float64
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
		host: listener.Addr().String(), path: filepath.Join(dir, "runtime.json"),
		registry: core.NewSessions(root, opts),
		slots:    make(chan struct{}, 16), rateAt: time.Now(), rateTokens: 40,
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
				s.registry.Expire()
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
	s.registry.Close()
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
}

func (s *Server) Snapshot() Status { return s.registry.Snapshot() }
