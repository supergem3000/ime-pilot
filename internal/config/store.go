// Package config persists core configuration with atomic replacement.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"sync"

	"ime-pilot/internal/core"
)

type Store struct {
	mu      sync.Mutex
	path    string
	current core.Config
	loadErr error
}

func Open(path string) *Store {
	return OpenWithDefaults(path, core.Config{SchemaVersion: 2, Revision: 1, Rules: []core.Rule{}})
}

// Defaults apply only when no file exists. JSON fields not defined in Config
// are ignored; existing files are not migrated or filled with defaults.
func OpenWithDefaults(path string, defaults core.Config) *Store {
	s := &Store{path: path, current: clone(defaults)}
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
	var c core.Config
	if err == nil {
		err = json.Unmarshal(data, &c)
		if err == nil {
			err = core.Validate(c)
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

func clone(c core.Config) core.Config {
	c.Rules = append([]core.Rule{}, c.Rules...)
	c.SchemeConfig = append([]byte(nil), c.SchemeConfig...)
	return c
}
func (s *Store) Snapshot() (core.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return clone(s.current), s.loadErr
}
func (s *Store) Path() string { return s.path }

// Save publishes the new revision only after durable replacement succeeds.
func (s *Store) Save(c core.Config) (core.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return clone(s.current), s.loadErr
	}
	if c.Revision != s.current.Revision {
		return clone(s.current), core.ErrConflict
	}
	if c.Revision == ^uint64(0) {
		return clone(s.current), errors.New("配置版本号已达到上限")
	}
	if err := core.Validate(c); err != nil {
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
