package core

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
)

type Rule struct {
	BundleID   string `json:"bundleId,omitempty"`
	AppPath    string `json:"appPath,omitempty"`
	Enabled    bool   `json:"enabled"`
	TargetMode Mode   `json:"targetMode"`
}

func (r Rule) Identity() Identity {
	return Identity{Path: r.AppPath, BundleID: r.BundleID}
}

type Config struct {
	SchemaVersion int    `json:"schemaVersion"`
	Revision      uint64 `json:"revision"`
	Paused        bool   `json:"paused"`
	// Empty means leave the input mode unchanged for apps without a rule.
	DefaultMode  Mode            `json:"defaultMode"`
	Rules        []Rule          `json:"rules"`
	SchemeID     string          `json:"schemeId,omitempty"`
	SchemeConfig json.RawMessage `json:"schemeConfig,omitempty"`
}

var ErrConflict = errors.New("配置已被其他操作更新，请重新加载后保存")

func Validate(c Config) error {
	if c.DefaultMode != "" && c.DefaultMode != English && c.DefaultMode != Chinese {
		return errors.New("其他应用的默认行为必须为不切换、中文或英文")
	}
	if len(c.SchemeID) > 128 || strings.TrimSpace(c.SchemeID) != c.SchemeID || strings.ContainsRune(c.SchemeID, 0) {
		return errors.New("切换方案标识无效")
	}
	if len(c.SchemeConfig) > 0 {
		var fields map[string]json.RawMessage
		if json.Unmarshal(c.SchemeConfig, &fields) != nil || fields == nil {
			return errors.New("方案配置必须为 JSON 对象")
		}
	}
	if c.SchemaVersion != 2 || c.Revision == 0 {
		return errors.New("配置版本无效")
	}
	if len(c.Rules) > 128 {
		return errors.New("应用规则最多 128 条")
	}
	seen := map[string]bool{}
	for _, r := range c.Rules {
		if r.TargetMode != English && r.TargetMode != Chinese {
			return errors.New("进入应用的目标模式必须为中文或英文")
		}
		if r.BundleID != "" {
			if r.AppPath != "" || !ValidBundleID(r.BundleID) {
				return errors.New("macOS 应用规则必须仅使用有效 Bundle ID")
			}
		} else if r.AppPath != strings.TrimSpace(r.AppPath) || !filepath.IsAbs(r.AppPath) || !strings.EqualFold(filepath.Ext(r.AppPath), ".exe") || strings.ContainsRune(r.AppPath, 0) {
			return errors.New("应用路径必须为绝对 .exe 路径")
		}
		if len(r.AppPath) > 32768 {
			return errors.New("应用路径长度无效")
		}
		key := r.Identity().Key()
		if seen[key] {
			return errors.New("不能重复添加同一应用")
		}
		seen[key] = true
	}
	return nil
}
func Key(path string) string {
	if path == "" {
		return ""
	}
	return strings.ToLower(filepath.Clean(path))
}

type ConfigStore interface {
	Snapshot() (Config, error)
	Save(Config) (Config, error)
	Path() string
}
