//go:build darwin && cgo

package macosinputsource

import (
	"context"
	"ime-pilot/internal/desktop"
)

func Info() desktop.SchemeInfo {
	return desktop.SchemeInfo{
		ID: ID, Name: "系统输入源切换",
		Description: "适用于 macOS 已启用的输入法与键盘布局。",
		Platforms:   []string{"darwin"},
	}
}

func Form(ctx context.Context) (desktop.Form, error) {
	if err := ctx.Err(); err != nil {
		return desktop.Form{}, err
	}
	sources, err := InputSources()
	if err != nil {
		return desktop.Form{}, err
	}
	chinese := desktop.Field{Key: "chineseSourceId", Label: "中文输入源", Type: "select"}
	english := desktop.Field{Key: "englishSourceId", Label: "英文键盘布局", Type: "select"}
	for _, s := range sources {
		option := desktop.Option{Value: s.ID, Label: s.Name + " — " + s.ID}
		if s.Chinese {
			chinese.Options = append(chinese.Options, option)
		}
		if s.English {
			english.Options = append(english.Options, option)
		}
	}
	return desktop.Form{Description: "选择已启用的系统输入源。回读仅确认输入源 ID，不代表输入法内部中英文状态。", Fields: []desktop.Field{chinese, english}}, nil
}
