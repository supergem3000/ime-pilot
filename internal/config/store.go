// Package config owns the user's application rules and durable pause state.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"ime-pilot/internal/core"
	"ime-pilot/internal/takeover"
)

type Rule struct {
	BundleID            string    `json:"bundleId,omitempty"`
	AppPath             string    `json:"appPath"`
	Name                string    `json:"name"`
	Enabled             bool      `json:"enabled"`
	SwitchOnActivation  *bool     `json:"switchOnActivation,omitempty"`
	TargetMode          core.Mode `json:"targetMode,omitempty"`
	SwitchOnInsertLeave *bool     `json:"switchOnInsertLeave,omitempty"`
}

func (r Rule) Identity() takeover.Identity {
	return takeover.Identity{Path: r.AppPath, BundleID: r.BundleID}
}

func (r Rule) OnActivation() bool  { return r.SwitchOnActivation == nil || *r.SwitchOnActivation }
func (r Rule) OnInsertLeave() bool { return r.SwitchOnInsertLeave == nil || *r.SwitchOnInsertLeave }

// ActivationMode preserves English as the default for rules saved without a mode.
func (r Rule) ActivationMode() core.Mode {
	if r.TargetMode == "" {
		return core.English
	}
	return r.TargetMode
}

type Config struct {
	SchemaVersion int    `json:"schemaVersion"`
	Revision      uint64 `json:"revision"`
	Paused        bool   `json:"paused"`
	Rules         []Rule `json:"rules"`
}

var ErrConflict = errors.New("配置已被其他操作更新，请重新加载后保存")

type Store struct {
	mu      sync.Mutex
	path    string
	current Config
	loadErr error
}

func Open(path string) *Store {
	s := &Store{path: path, current: Config{SchemaVersion: 2, Revision: 1, Rules: []Rule{}}}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return s
	}
	var data []byte
	if err == nil {
		data, err = io.ReadAll(io.LimitReader(f, 256*1024+1))
		f.Close()
	}
	if err == nil && len(data) > 256*1024 {
		err = errors.New("配置文件超过 256 KiB")
	}
	var c Config
	if err == nil {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		err = decoder.Decode(&c)
		if err == nil {
			var extra any
			if decoder.Decode(&extra) != io.EOF {
				err = errors.New("配置有多余内容")
			}
		}
		if err == nil {
			err = Validate(c)
		}
	}
	if err != nil {
		s.loadErr = fmt.Errorf("读取配置失败，自动功能已停用且不会覆盖原文件：%w", err)
		s.current.Paused = true
		return s
	}
	s.current = c
	return s
}

func Validate(c Config) error {
	if c.SchemaVersion != 2 || c.Revision == 0 {
		return errors.New("配置版本无效")
	}
	if len(c.Rules) > 128 {
		return errors.New("应用规则最多 128 条")
	}
	seen := map[string]bool{}
	for _, r := range c.Rules {
		if mode := r.ActivationMode(); mode != core.English && mode != core.Chinese {
			return errors.New("进入应用的目标模式必须为中文或英文")
		}
		if r.BundleID != "" {
			if r.AppPath != "" || !takeover.ValidBundleID(r.BundleID) {
				return errors.New("macOS 应用规则必须仅使用有效 Bundle ID")
			}
		} else if r.AppPath != strings.TrimSpace(r.AppPath) || !filepath.IsAbs(r.AppPath) || !strings.EqualFold(filepath.Ext(r.AppPath), ".exe") || strings.ContainsRune(r.AppPath, 0) {
			return errors.New("应用路径必须为绝对 .exe 路径")
		}
		if len(r.AppPath) > 32768 || len(r.Name) > 160 || strings.TrimSpace(r.Name) == "" {
			return errors.New("应用名称或路径长度无效")
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
func clone(c Config) Config { c.Rules = append([]Rule{}, c.Rules...); return c }
func (s *Store) Snapshot() (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return clone(s.current), s.loadErr
}
func (s *Store) Path() string { return s.path }

// Save publishes the new revision only after durable replacement succeeds.
func (s *Store) Save(c Config) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return clone(s.current), s.loadErr
	}
	if c.Revision != s.current.Revision {
		return clone(s.current), ErrConflict
	}
	if c.Revision == ^uint64(0) {
		return clone(s.current), errors.New("配置版本号已达到上限")
	}
	if err := Validate(c); err != nil {
		return clone(s.current), err
	}
	c = clone(c)
	c.Revision++
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return clone(s.current), err
	}
	if len(data)+1 > 256*1024 {
		return clone(s.current), errors.New("配置文件超过 256 KiB")
	}
	if err = os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return clone(s.current), err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".config-*.tmp")
	if err != nil {
		return clone(s.current), err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(append(data, '\n')); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = replaceFile(tmp, s.path)
	}
	if err != nil {
		return clone(s.current), fmt.Errorf("保存失败，原配置仍有效：%w", err)
	}
	s.current = c
	return clone(c), nil
}
