package config

import (
	"ime-pilot/internal/core"
	"path/filepath"
	"testing"
)

func TestBundleRulePersistenceAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	s := Open(path)
	c, _ := s.Snapshot()
	c.Rules = []core.Rule{{BundleID: "com.example.Editor", Enabled: true, TargetMode: core.English}}
	if _, err := s.Save(c); err != nil {
		t.Fatal(err)
	}
	loaded, err := Open(path).Snapshot()
	if err != nil || loaded.Rules[0].BundleID != c.Rules[0].BundleID {
		t.Fatalf("reload: %+v %v", loaded, err)
	}
	for _, id := range []string{"editor", "com..editor", "com.example/editor", "com.example.*", "com.example.编辑器"} {
		c.Rules[0].BundleID = id
		if core.Validate(c) == nil {
			t.Fatalf("invalid bundle ID accepted: %q", id)
		}
	}
	c.Rules = append(loaded.Rules, loaded.Rules[0])
	if core.Validate(c) == nil {
		t.Fatal("duplicate bundle ID accepted")
	}
	c.Rules = c.Rules[:1]
	c.Rules[0].AppPath = filepath.Join(t.TempDir(), "Editor.exe")
	if core.Validate(c) == nil {
		t.Fatal("ambiguous path and bundle ID accepted")
	}
}
