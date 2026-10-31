//go:build windows || darwin

package desktop

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"ime-pilot/internal/automation"
	"ime-pilot/internal/config"
	localrpc "ime-pilot/internal/rpc"
)

type Dashboard struct {
	Platform     string             `json:"platform"`
	Config       config.Config      `json:"config"`
	ConfigPath   string             `json:"configPath"`
	Error        string             `json:"error"`
	MonitorError string             `json:"monitorError"`
	Recent       automation.Outcome `json:"recent"`
	RPC          localrpc.Status    `json:"rpc"`
	RPCError     string             `json:"rpcError"`
}

func (s *InputService) GetStatus() Dashboard {
	c, err := s.store.Snapshot()
	d := Dashboard{Platform: platformName, Config: c, ConfigPath: s.store.Path()}
	if err != nil {
		d.Error = err.Error()
	}
	s.stateMu.Lock()
	d.MonitorError = s.monitorError
	d.RPCError = s.rpcError
	server := s.rpcServer
	s.stateMu.Unlock()
	if server != nil {
		d.RPC = server.Snapshot()
	}
	if s.agent != nil {
		d.Recent = s.agent.Recent()
	}
	return d
}
func (s *InputService) ListApps() ([]platformApp, error) { return runningApps() }
func (s *InputService) SaveConfig(c config.Config) (Dashboard, error) {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	if err := validatePlatformConfig(c); err != nil {
		return s.GetStatus(), err
	}
	next, err := s.store.Save(c)
	if err != nil {
		return s.GetStatus(), err
	}
	s.agent.Update(next)
	s.notify()
	return s.GetStatus(), nil
}
func (s *InputService) SetPaused(paused bool) (Dashboard, error) {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	c, err := s.store.Snapshot()
	if err != nil {
		return s.GetStatus(), err
	}
	c.Paused = paused
	next, err := s.store.Save(c)
	if err != nil {
		return s.GetStatus(), err
	}
	s.agent.Update(next)
	s.notify()
	return s.GetStatus(), nil
}
func (s *InputService) notify() {
	if s.changed != nil {
		s.changed()
	}
}
func (s *InputService) startAutomation(ctx context.Context) {
	monitor, err := startMonitor(ctx, s.agent.Invalidate)
	if err != nil {
		s.stateMu.Lock()
		s.monitorError = err.Error()
		s.stateMu.Unlock()
		return
	}
	s.stateMu.Lock()
	s.monitorDone = monitor.Done
	s.monitor = monitor
	s.stateMu.Unlock()
	go func() {
		first := true
		for event := range monitor.Events {
			target, err := resolveEvent(event)
			if err != nil {
				target.Path = ""
			}
			s.agent.Observe(target, event.Token, first || event.Baseline)
			first = false
		}
		if ctx.Err() == nil {
			s.agent.Invalidate()
			s.stateMu.Lock()
			s.monitorError = "前台监听已停止，请重启 ime-pilot"
			s.stateMu.Unlock()
			s.notify()
		}
	}()
}

// Shutdown is bounded; an already submitted native message cannot be undone.
func (s *InputService) waitAutomation() {
	s.stateMu.Lock()
	server := s.rpcServer
	monitorDone := s.monitorDone
	s.stateMu.Unlock()
	if server != nil {
		server.Close()
	}
	timeout := time.NewTimer(time.Second)
	defer timeout.Stop()
	select {
	case <-s.agent.Done():
	case <-timeout.C:
		return
	}
	if monitorDone != nil {
		select {
		case <-monitorDone:
		case <-timeout.C:
		}
	}
}

func (s *InputService) startRPC(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	server, err := localrpc.Start(ctx, filepath.Dir(s.store.Path()), localrpc.Options{
		Config: func() config.Config { c, _ := s.store.Snapshot(); return c },
		Capture: func(e localrpc.Evidence) (automation.Target, error) {
			s.stateMu.Lock()
			monitor := s.monitor
			s.stateMu.Unlock()
			if monitor == nil {
				return automation.Target{}, fmt.Errorf("foreground monitor unavailable")
			}
			target, _, err := captureBinding(monitor, e)
			return target, err
		},
		Execute: s.agent.SubmitPlugin, Publish: s.agent.SetTakeovers, Changed: s.notify,
	})
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if err != nil {
		s.rpcError = "本地接口启动失败：" + err.Error()
	} else {
		s.rpcServer = server
	}
}
