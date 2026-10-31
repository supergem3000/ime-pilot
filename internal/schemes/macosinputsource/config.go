package macosinputsource

import (
	"encoding/json"
	"fmt"
	"strings"
)

const ID = "macos.input-source"

type Config struct {
	ChineseSourceID string `json:"chineseSourceId,omitempty"`
	EnglishSourceID string `json:"englishSourceId,omitempty"`
}

func Decode(raw json.RawMessage) (Config, error) {
	var c Config
	if len(raw) == 0 {
		return c, nil
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, err
	}
	for _, id := range []string{c.ChineseSourceID, c.EnglishSourceID} {
		if len(id) > 512 || strings.TrimSpace(id) != id || strings.ContainsRune(id, 0) {
			return c, fmt.Errorf("输入源标识无效")
		}
	}
	if c.ChineseSourceID != "" && c.ChineseSourceID == c.EnglishSourceID {
		return c, fmt.Errorf("中文和英文不能使用同一个输入源")
	}
	return c, nil
}
