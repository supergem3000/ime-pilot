package windowslayout

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const ID = "windows.keyboard-layout"

// Keep existing field names and HKL values. TSF IDs additionally identify the
// language, service CLSID and profile GUID, distinguishing IMEs sharing an HKL.
type Config struct {
	ChineseLayoutID string `json:"chineseLayoutId"`
	EnglishLayoutID string `json:"englishLayoutId"`
}

func Decode(raw json.RawMessage) (Config, error) {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("键盘布局配置无效：%w", err)
	}
	for _, id := range []string{c.ChineseLayoutID, c.EnglishLayoutID} {
		if strings.HasPrefix(id, "tsf:") {
			if !profileIDPattern.MatchString(id) {
				return c, fmt.Errorf("输入法配置标识无效，请重新选择")
			}
			continue
		}
		value, err := strconv.ParseUint(id, 16, 32)
		if err != nil || value == 0 || id != fmt.Sprintf("%08X", value) {
			return c, fmt.Errorf("请分别选择有效的中文和英文键盘布局")
		}
	}
	if c.ChineseLayoutID == c.EnglishLayoutID {
		return c, fmt.Errorf("中文和英文不能使用同一个键盘布局")
	}
	return c, nil
}

var profileIDPattern = regexp.MustCompile(`^tsf:[0-9A-F]{4}:\{[0-9A-F]{8}-[0-9A-F]{4}-[0-9A-F]{4}-[0-9A-F]{4}-[0-9A-F]{12}\}:\{[0-9A-F]{8}-[0-9A-F]{4}-[0-9A-F]{4}-[0-9A-F]{4}-[0-9A-F]{12}\}$`)
