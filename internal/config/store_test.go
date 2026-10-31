package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ime-pilot/internal/core"
)

func TestActivationModePersistenceAndValidation(t *testing.T) {
	for _, mode := range []core.Mode{core.English, core.Chinese} {
		t.Run(string(mode), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			s := Open(path)
			c, _ := s.Snapshot()
			c.Rules = []core.Rule{{AppPath: filepath.Join(t.TempDir(), "Editor.exe"), Enabled: true, TargetMode: mode}}
			if _, err := s.Save(c); err != nil {
				t.Fatal(err)
			}
			loaded, err := Open(path).Snapshot()
			if err != nil || loaded.Rules[0].TargetMode != mode {
				t.Fatalf("reload: %+v, %v", loaded, err)
			}
			c = loaded
			c.Rules[0].TargetMode = "invalid"
			if _, err := s.Save(c); err == nil {
				t.Fatal("invalid mode accepted")
			}
		})
	}
}

func TestDurableRevisionPauseAndSnapshotIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	s := Open(path)
	c, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	c.Paused = true
	c.Rules = []core.Rule{{AppPath: filepath.Join(t.TempDir(), "Editor.exe"), Enabled: true, TargetMode: core.English}}
	next, err := s.Save(c)
	if err != nil {
		t.Fatal(err)
	}
	if next.Revision != 2 {
		t.Fatalf("revision=%d", next.Revision)
	}
	next.Rules[0].Enabled = false
	reloaded, err := Open(path).Snapshot()
	if err != nil || !reloaded.Paused || !reloaded.Rules[0].Enabled {
		t.Fatalf("reload: %+v %v", reloaded, err)
	}
	if _, err = s.Save(c); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("stale edit accepted: %v", err)
	}
}
func TestSaveFailureKeepsMemoryAndDisk(t *testing.T) {
	dir := t.TempDir()
	s := Open(filepath.Join(dir, "config.json"))
	c, _ := s.Snapshot()
	c, err := s.Save(c)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	// Make replacement impossible while retaining the previously published data.
	original := s.path
	s.path = dir
	c.Paused = true
	if _, err = s.Save(c); err == nil {
		t.Fatal("save to directory succeeded")
	}
	current, _ := s.Snapshot()
	if current.Paused || current.Revision != 2 {
		t.Fatalf("failed save published: %+v", current)
	}
	after, _ := os.ReadFile(original)
	if string(before) != string(after) {
		t.Fatal("old configuration changed")
	}
}
func TestCorruptConfigIsNotOverwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	raw := []byte(`{"schemaVersion":999,"revision":1}`)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	s := Open(path)
	c, err := s.Snapshot()
	if err == nil || !c.Paused {
		t.Fatal("corrupt config not blocked")
	}
	if _, err = s.Save(c); err == nil {
		t.Fatal("corrupt configuration overwritten")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(raw) {
		t.Fatal("original lost")
	}
}
func TestDuplicatePathsRejectedAndUnknownFieldsIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Editor.exe")
	c := core.Config{SchemaVersion: 2, Revision: 1, Rules: []core.Rule{{AppPath: path, TargetMode: core.English}, {AppPath: path, TargetMode: core.English}}}
	if core.Validate(c) == nil {
		t.Fatal("duplicate application accepted")
	}
	p := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(p, []byte(`{"schemaVersion":2,"revision":1,"paused":false,"rules":[],"unknown":true}`), 0600)
	loaded, err := Open(p).Snapshot()
	if err != nil || loaded.Revision != 1 || loaded.Paused {
		t.Fatalf("unknown field prevented loading known fields: config=%+v err=%v", loaded, err)
	}
}
