// Package takeover defines program ownership shared by RPC and automatic rules.
package takeover

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

type Program struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// Identity is resolved by the native platform, never accepted from an RPC caller.
// BundleID identifies a macOS app independently of its installation path.
type Identity struct {
	Path     string
	BundleID string
}

func (i Identity) Key() string {
	if i.BundleID != "" {
		return "bundle:" + i.BundleID
	}
	if i.Path == "" {
		return ""
	}
	return "path:" + strings.ToLower(filepath.Clean(i.Path))
}

func ValidBundleID(value string) bool {
	if len(value) > 255 || !strings.Contains(value, ".") {
		return false
	}
	for _, part := range strings.Split(value, ".") {
		if part == "" {
			return false
		}
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

func CoversIdentity(programs []Program, identity Identity) bool {
	for _, p := range programs {
		if p.Kind == "macos.bundleId" {
			if identity.BundleID != "" && p.Value == identity.BundleID {
				return true
			}
		} else if identity.BundleID == "" && identity.Path != "" && Matches(p, identity.Path) {
			return true
		}
	}
	return false
}

type Lease struct {
	SessionID string
	PluginID  string
	Name      string
	Programs  []Program
	Expires   time.Time
}

// Normalize validates the whole registration before any ownership is published.
func Normalize(programs []Program) ([]Program, error) {
	if len(programs) == 0 || len(programs) > 32 {
		return nil, errors.New("接管程序数量必须为 1–32")
	}
	result := make([]Program, 0, len(programs))
	seen := map[Program]bool{}
	for _, p := range programs {
		if p.Value == "" || len(p.Value) > 32768 || strings.TrimSpace(p.Value) != p.Value || strings.ContainsAny(p.Value, "*?\x00") {
			return nil, errors.New("接管程序标识无效")
		}
		switch p.Kind {
		case "windows.executableName":
			if runtime.GOOS != "windows" || strings.ContainsAny(p.Value, "/\\:") || !strings.EqualFold(filepath.Ext(p.Value), ".exe") {
				return nil, errors.New("必须提供本机可执行文件基本名")
			}
			p.Value = strings.ToLower(p.Value)
		case "windows.executablePath":
			if runtime.GOOS != "windows" || !filepath.IsAbs(p.Value) || !strings.EqualFold(filepath.Ext(p.Value), ".exe") {
				return nil, errors.New("必须提供本机绝对可执行路径")
			}
			resolved, err := filepath.EvalSymlinks(p.Value)
			if err != nil {
				return nil, fmt.Errorf("无法核实可执行路径：%w", err)
			}
			info, err := os.Stat(resolved)
			if err != nil || !info.Mode().IsRegular() {
				return nil, errors.New("可执行路径不可用")
			}
			p.Value = strings.ToLower(filepath.Clean(resolved))
		case "macos.bundleId":
			if runtime.GOOS != "darwin" || !ValidBundleID(p.Value) {
				return nil, errors.New("必须提供本机 Bundle ID")
			}
		default:
			return nil, errors.New("未知的程序标识类型")
		}
		if !seen[p] {
			seen[p] = true
			result = append(result, p)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Kind != result[j].Kind {
			return result[i].Kind < result[j].Kind
		}
		return result[i].Value < result[j].Value
	})
	return result, nil
}

func Matches(p Program, path string) bool {
	switch p.Kind {
	case "windows.executableName":
		return strings.EqualFold(filepath.Base(path), p.Value)
	case "windows.executablePath":
		return strings.EqualFold(filepath.Clean(path), p.Value)
	case "macos.bundleId":
		return path == p.Value
	}
	return false
}

func Covers(programs []Program, path string) bool {
	for _, p := range programs {
		if Matches(p, path) {
			return true
		}
	}
	return false
}

func Overlaps(a, b Program) bool {
	if a.Kind == b.Kind {
		if a.Kind == "macos.bundleId" {
			return a.Value == b.Value
		}
		return strings.EqualFold(a.Value, b.Value)
	}
	if a.Kind == "windows.executableName" && b.Kind == "windows.executablePath" {
		return strings.EqualFold(a.Value, filepath.Base(b.Value))
	}
	if b.Kind == "windows.executableName" && a.Kind == "windows.executablePath" {
		return Overlaps(b, a)
	}
	return false
}
