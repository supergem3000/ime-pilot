//go:build windows

package windowslayout

import (
	"fmt"
	"strings"

	native "ime-pilot/internal/platform/windows"
)

func configuredLayouts(c Config) (native.InputSource, native.InputSource, error) {
	// Old HKL-only configurations retain their original semantics.
	list := native.KeyboardLayouts
	if strings.HasPrefix(c.ChineseLayoutID, "tsf:") || strings.HasPrefix(c.EnglishLayoutID, "tsf:") {
		list = native.InputSources
	}
	available, err := list()
	if err != nil {
		return native.InputSource{}, native.InputSource{}, err
	}
	var chinese, english native.InputSource
	for _, item := range available {
		if item.ID == c.ChineseLayoutID && item.Language == 4 {
			chinese = item
		}
		if item.ID == c.EnglishLayoutID && item.Language == 9 {
			english = item
		}
	}
	if chinese.ID == "" || english.ID == "" {
		return native.InputSource{}, native.InputSource{}, fmt.Errorf("配置的中文或英文输入源不可用或语言不匹配，请重新选择")
	}
	return chinese, english, nil
}
