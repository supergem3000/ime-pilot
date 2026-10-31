package core_test

import (
	. "ime-pilot/internal/core"
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeAndCrossKindOverlap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Editor.exe")
	if err := os.WriteFile(path, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	programs, err := Normalize([]Program{
		{Kind: "windows.executableName", Value: "EDITOR.EXE"},
		{Kind: "windows.executablePath", Value: path},
		{Kind: "windows.executableName", Value: "editor.exe"},
	})
	if err != nil || len(programs) != 2 {
		t.Fatalf("%v %v", programs, err)
	}
	if !Overlaps(programs[0], programs[1]) {
		t.Fatal("name/path overlap missed")
	}
	for _, p := range programs {
		if !Matches(p, path) {
			t.Fatalf("program does not match resolved target: %+v", p)
		}
	}
	if Overlaps(Program{Kind: "windows.executableName", Value: "other.exe"}, programs[0]) {
		t.Fatal("unrelated name overlaps")
	}
}

func TestNormalizeRejectsUnknownOrAmbiguousPrograms(t *testing.T) {
	for _, programs := range [][]Program{
		nil,
		{{Kind: "windows.executableName", Value: "*.exe"}},
		{{Kind: "windows.executableName", Value: "folder/editor.exe"}},
		{{Kind: "windows.executableName", Value: " editor.exe"}},
		{{Kind: "windows.executablePath", Value: "editor.exe"}},
		{{Kind: "unknown", Value: "editor.exe"}},
	} {
		if _, err := Normalize(programs); err == nil {
			t.Fatalf("invalid registration accepted: %+v", programs)
		}
	}
}
