//go:build windows

package windowskey

import (
	"context"

	"ime-pilot/internal/desktop"
)

func Info() desktop.SchemeInfo {
	return desktop.SchemeInfo{
		ID: ID, Name: "模拟 Shift 按键",
		Description: "适用于豆包输入法，需保持 Shift 切换中英文。",
		Platforms:   []string{"windows"},
	}
}

func Form(ctx context.Context) (desktop.Form, error) {
	if err := ctx.Err(); err != nil {
		return desktop.Form{}, err
	}
	return desktop.Form{
		Description: "请先在目标应用选中豆包输入法，并保持 Shift 切换中英文。仅在状态不符时按一次 Shift，再回读确认。不会自动选择输入法；首次使用请在测试区核对实际输入。",
		Fields:      []desktop.Field{},
	}, nil
}
