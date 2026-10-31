//go:build windows

package windowslayout

import (
	"context"
	"ime-pilot/internal/desktop"
	native "ime-pilot/internal/platform/windows"
)

func Info() desktop.SchemeInfo {
	return desktop.SchemeInfo{
		ID: ID, Name: "输入法与键盘布局切换",
		Description: "在指定的中文输入法与英文键盘布局之间切换。",
		Platforms:   []string{"windows"},
	}
}

func Form(ctx context.Context) (desktop.Form, error) {
	if err := ctx.Err(); err != nil {
		return desktop.Form{}, err
	}
	available, err := native.InputSources()
	if err != nil {
		return desktop.Form{}, err
	}
	chinese := desktop.Field{Key: "chineseLayoutId", Label: "中文键盘布局 / 输入法", Type: "select", Required: true}
	english := desktop.Field{Key: "englishLayoutId", Label: "英文键盘布局", Type: "select", Required: true}
	for _, item := range available {
		option := desktop.Option{Value: item.ID, Label: item.Name + " — " + item.ID}
		if item.IsTextService() {
			option.Label = item.Name
		} else if item.Language == 4 {
			option.Label += "（不指定具体输入法）"
		}
		switch item.Language {
		case 4:
			chinese.Options = append(chinese.Options, option)
		case 9:
			english.Options = append(english.Options, option)
		}
	}
	chinese.Help = "具体输入法的激活会影响当前桌面的其他应用；不控制输入法内部中英文状态。"
	english.Help = "列出系统已加载的英文布局。"
	return desktop.Form{Description: "选择具体输入法时使用桌面范围切换，仅报告调用结果，请用实际输入验证。仅选择键盘布局时仍按目标窗口切换并回读布局。", Fields: []desktop.Field{chinese, english}}, nil
}
