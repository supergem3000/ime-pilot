//go:build windows || darwin

package desktop

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"ime-pilot/internal/automation"
	"ime-pilot/internal/config"
	"ime-pilot/internal/core"
	localrpc "ime-pilot/internal/rpc"
)

//go:embed frontend/*
var frontend embed.FS

// InputService exposes only the manual mode operation to the embedded page.
type InputService struct {
	window       *application.WebviewWindow
	root         context.Context
	mu           sync.Mutex
	operation    sync.Mutex
	cancel       context.CancelFunc
	store        *config.Store
	agent        *automation.Agent
	stateMu      sync.Mutex
	saveMu       sync.Mutex
	monitorError string
	monitorDone  <-chan struct{}
	monitor      *platformMonitor
	rpcServer    *localrpc.Server
	rpcError     string
	changed      func()
}

type Reply struct {
	State   core.State `json:"state"`
	Message string     `json:"message"`
	OK      bool       `json:"ok"`
}

func (s *InputService) cancelCurrent() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
}

// Perform executes on a service goroutine; only window handle access runs on
// the native owner thread. No editor text is passed to Go or persisted.
func (s *InputService) Perform(callCtx context.Context, mode string) Reply {
	if mode != "inspect" && mode != string(core.Chinese) && mode != string(core.English) {
		return Reply{Message: "无效操作。"}
	}
	if !s.operation.TryLock() {
		return Reply{Message: "已有操作进行中，请稍后重试。"}
	}
	defer s.operation.Unlock()
	ctx, cancel := context.WithTimeout(s.root, 500*time.Millisecond)
	defer cancel()
	stopCall := context.AfterFunc(callCtx, cancel)
	defer stopCall()
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.cancel = nil; s.mu.Unlock() }()
	var hwnd uintptr
	application.InvokeSync(func() { hwnd = uintptr(s.window.NativeWindow()) })
	if err := ctx.Err(); err != nil {
		return Reply{Message: explainError(err)}
	}
	backend, err := newManualBackend(hwnd, s.store)
	if err != nil {
		return Reply{Message: explainError(err)}
	}
	controller := core.NewController(backend)
	if mode == "inspect" {
		state, err := controller.Inspect()
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			return Reply{Message: explainError(err)}
		}
		return Reply{State: state, OK: true, Message: "查询完成。此结果只代表本窗口刚才读取的状态。"}
	}
	result, err := controller.Switch(ctx, core.Mode(mode))
	if err != nil {
		return Reply{Message: explainError(err)}
	}
	message := "已是" + modeName(core.Mode(mode)) + "模式，没有重复设置。"
	if platformName == "darwin" {
		message = "已选中配置的" + modeName(core.Mode(mode)) + "输入源，没有重复设置。"
	}
	if result.Changed {
		message = switchMessage(core.Mode(mode))
	}
	return Reply{State: result.After, OK: true, Message: message}
}

func Run() error {
	assets, err := fs.Sub(frontend, "frontend")
	if err != nil {
		return err
	}
	root, stop := context.WithCancel(context.Background())
	defer stop()
	service := &InputService{root: root}
	path, err := configPath()
	if err != nil {
		return err
	}
	service.store = config.Open(path)
	cfg, _ := service.store.Snapshot()
	service.agent = automation.New(root, cfg, &service.operation, func(t automation.Target) core.Backend { return newTargetBackend(t, service.store) }, service.notify)
	app := application.New(application.Options{
		Name: "ime-pilot", Description: "按应用自动切换输入法英文状态", Icon: appIcon(),
		Mac:        application.MacOptions{ActivationPolicy: application.ActivationPolicyAccessory},
		Services:   []application.Service{application.NewService(service)},
		Assets:     application.AssetOptions{Handler: application.BundledAssetFileServer(assets)},
		OnShutdown: func() { stop(); service.cancelCurrent(); service.waitAutomation() },
	})
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "ime-pilot", Title: "ime-pilot · 应用输入法规则", Width: 900, Height: 900,
		MinWidth: 580, MinHeight: 520, URL: "/",
	})
	service.window = window
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		service.cancelCurrent()
		e.Cancel()
		window.Hide()
	})
	window.OnWindowEvent(events.Common.WindowLostFocus, func(_ *application.WindowEvent) { service.cancelCurrent() })
	tray := app.SystemTray.New().SetIcon(appIcon())
	tray.SetTooltip("ime-pilot")
	open := func() { window.Show(); window.Focus() }
	if platformName == "windows" {
		tray.OnClick(open)
	}
	menu := app.NewMenu()
	menu.Add("打开 ime-pilot").OnClick(func(_ *application.Context) { open() })
	menu.AddSeparator()
	pause := menu.AddCheckbox("暂停自动切换", cfg.Paused)
	pause.OnClick(func(c *application.Context) {
		_, err := service.SetPaused(c.ClickedMenuItem().Checked())
		if err != nil {
			snapshot, _ := service.store.Snapshot()
			pause.SetChecked(snapshot.Paused)
			app.Dialog.Error().SetTitle("暂停状态未保存").SetMessage(err.Error()).Show()
		}
	})
	menu.AddSeparator()
	menu.Add("退出").OnClick(func(_ *application.Context) { stop(); service.cancelCurrent(); app.Quit() })
	tray.SetMenu(menu)
	service.changed = func() {
		if root.Err() != nil {
			return
		}
		c, _ := service.store.Snapshot()
		application.InvokeAsync(func() { pause.SetChecked(c.Paused) })
		if root.Err() == nil {
			app.Event.Emit("agent:changed")
		}
	}
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(_ *application.ApplicationEvent) {
		service.startAutomation(root)
		service.startRPC(root)
		service.notify()
	})
	return app.Run()
}

func modeName(mode core.Mode) string {
	switch mode {
	case core.Chinese:
		return "中文"
	case core.English:
		return "英文"
	default:
		return "未知"
	}
}
func explainError(err error) string {
	switch {
	case errors.Is(err, core.ErrFocusChanged):
		return "已停止：ime-pilot 窗口失焦或输入上下文已变化。"
	case errors.Is(err, core.ErrUnsupported):
		return unsupportedMessage() + err.Error()
	case errors.Is(err, core.ErrVerification):
		return "未确认切换成功，请核对系统输入法状态。"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "操作已取消或超时，未确认切换结果。"
	default:
		return "操作失败，未确认切换结果：" + err.Error()
	}
}
