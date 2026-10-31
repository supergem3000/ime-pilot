//go:build windows || darwin

package desktop

import (
	"context"
	"embed"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"ime-pilot/assets/icons"
	"ime-pilot/internal/core"
	"io/fs"
	"runtime"
)

//go:embed frontend/*
var frontend embed.FS

type Options struct {
	Context context.Context
	Service *core.Service
	Start   func()
	Stop    func()
	Schemes []SchemeInfo
	Form    func(context.Context, string) (Form, error)
	AppInfo func(string, string) AppInfo
}
type InputService struct {
	window *application.WebviewWindow
	core   *core.Service
	opts   Options
}

func (s *InputService) GetStatus() core.Dashboard                        { return s.core.GetStatus() }
func (s *InputService) ListApps() ([]core.App, error)                    { return s.core.ListApps() }
func (s *InputService) SaveConfig(c core.Config) (core.Dashboard, error) { return s.core.SaveConfig(c) }
func (s *InputService) SetPaused(paused bool) (core.Dashboard, error) {
	return s.core.SetPaused(paused)
}
func (s *InputService) ListSchemes() []SchemeInfo { return s.opts.Schemes }
func (s *InputService) GetAppInfo(path, bundleID string) AppInfo {
	return s.opts.AppInfo(path, bundleID)
}
func (s *InputService) GetSchemeForm(ctx context.Context, id string) (Form, error) {
	return s.opts.Form(ctx, id)
}
func (s *InputService) Perform(ctx context.Context, mode string) core.Reply {
	var window uintptr
	application.InvokeSync(func() { window = uintptr(s.window.NativeWindow()) })
	return s.core.Perform(ctx, mode, window)
}
func Run(opts Options) error {
	assets, err := fs.Sub(frontend, "frontend")
	if err != nil {
		return err
	}
	root, stop := opts.Context, opts.Stop
	service := &InputService{core: opts.Service, opts: opts}
	cfg := opts.Service.Config()
	app := application.New(application.Options{
		Name: "IME Pilot", Description: "按应用自动切换输入法英文状态", Icon: icons.App,
		Mac:        application.MacOptions{ActivationPolicy: application.ActivationPolicyAccessory},
		Services:   []application.Service{application.NewService(service)},
		Assets:     application.AssetOptions{Handler: application.BundledAssetFileServer(assets)},
		OnShutdown: func() { stop(); service.core.CancelManual(); service.core.Close() },
	})
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "ime-pilot", Title: "IME Pilot", Width: 900, Height: 640,
		MinWidth: 580, MinHeight: 520, URL: "/",
	})
	service.window = window
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		service.core.CancelManual()
		e.Cancel()
		window.Hide()
	})
	window.OnWindowEvent(events.Common.WindowLostFocus, func(_ *application.WindowEvent) { service.core.CancelManual() })
	tray := app.SystemTray.New()
	if runtime.GOOS == "darwin" {
		// Let macOS tint the menu bar icon for light and dark backgrounds.
		tray.SetTemplateIcon(icons.Tray)
	} else {
		tray.SetIcon(icons.Tray)
	}
	tray.SetTooltip("IME Pilot")
	open := func() { window.Show(); window.Focus() }
	if runtime.GOOS == "windows" {
		tray.OnClick(open)
	}
	menu := app.NewMenu()
	menu.Add("打开 IME Pilot").OnClick(func(_ *application.Context) { open() })
	menu.AddSeparator()
	pause := menu.AddCheckbox("暂停自动切换", cfg.Paused)
	pause.OnClick(func(c *application.Context) {
		_, err := service.SetPaused(c.ClickedMenuItem().Checked())
		if err != nil {
			snapshot := service.core.Config()
			pause.SetChecked(snapshot.Paused)
			app.Dialog.Error().SetTitle("暂停状态未保存").SetMessage(err.Error()).Show()
		}
	})
	menu.AddSeparator()
	menu.Add("退出").OnClick(func(_ *application.Context) { stop(); service.core.CancelManual(); app.Quit() })
	tray.SetMenu(menu)
	service.core.SetChanged(func() {
		if root.Err() != nil {
			return
		}
		c := service.core.Config()
		application.InvokeAsync(func() { pause.SetChecked(c.Paused) })
		if root.Err() == nil {
			app.Event.Emit("agent:changed")
		}
	})
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(_ *application.ApplicationEvent) {
		opts.Start()
		service.core.Notify()
	})
	return app.Run()
}
