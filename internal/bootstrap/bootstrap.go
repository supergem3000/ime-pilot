//go:build windows || darwin

package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"ime-pilot/internal/config"
	"ime-pilot/internal/core"
	"ime-pilot/internal/desktop"
	"ime-pilot/internal/rpc"
	"path/filepath"
	"runtime"
)

// Registration is a composition record, not an interface every scheme must
// implement. Only this package knows concrete schemes and connects their parts.
type registration struct {
	Info     desktop.SchemeInfo
	Form     func(context.Context) (desktop.Form, error)
	Validate func(json.RawMessage) error
	Create   func(json.RawMessage, func() bool, uintptr) (core.Switcher, error)
}

func Run() error {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	path, err := configPath()
	if err != nil {
		return err
	}
	registered := schemes()
	find := func(id string) (registration, error) {
		for _, entry := range registered {
			if entry.Info.ID == id {
				for _, platform := range entry.Info.Platforms {
					if platform == runtime.GOOS {
						return entry, nil
					}
				}
			}
		}
		return registration{}, fmt.Errorf("当前系统没有可用的切换方案：%s", id)
	}
	store := config.OpenWithDefaults(path, core.Config{
		SchemaVersion: 2, Revision: 1, Rules: []core.Rule{},
		SchemeID: registered[0].Info.ID, SchemeConfig: json.RawMessage(`{}`),
	})
	service := core.NewService(ctx, core.ServiceOptions{
		Store: store, Signals: newSignals(),
		Validate: func(c core.Config) error {
			if err := core.Validate(c); err != nil {
				return err
			}
			if err := validateApps(c); err != nil {
				return err
			}
			entry, err := find(c.SchemeID)
			if err != nil {
				return err
			}
			return entry.Validate(c.SchemeConfig)
		},
		Create: func(c core.Config, valid func() bool, window uintptr) (core.Switcher, error) {
			entry, err := find(c.SchemeID)
			if err != nil {
				return nil, err
			}
			return entry.Create(c.SchemeConfig, valid, window)
		},
	})
	infos := make([]desktop.SchemeInfo, 0, len(registered))
	for _, entry := range registered {
		infos = append(infos, entry.Info)
	}
	return desktop.Run(desktop.Options{Context: ctx, Service: service, Stop: stop, Schemes: infos,
		AppInfo: appInfo,
		Form: func(ctx context.Context, id string) (desktop.Form, error) {
			entry, err := find(id)
			if err != nil {
				return desktop.Form{}, err
			}
			return entry.Form(ctx)
		},
		Start: func() {
			service.StartSignals()
			server, err := rpc.Start(ctx, filepath.Dir(path), service.SessionOptions())
			service.SetEndpoint(server, err)
		},
	})
}
