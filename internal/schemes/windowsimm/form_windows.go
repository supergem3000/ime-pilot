//go:build windows

package winime

import (
	"context"
	"ime-pilot/internal/desktop"
)

func Info() desktop.SchemeInfo {
	return desktop.SchemeInfo{
		ID: ID, Name: "IMM 状态切换",
		Description: "适用于微软拼音，直接切换输入法内部中英文状态。",
		Platforms:   []string{"windows"},
	}
}

func Form(context.Context) (desktop.Form, error) {
	return desktop.Form{Description: "切换微软拼音的中英文状态，无需额外配置。", Fields: []desktop.Field{}}, nil
}
