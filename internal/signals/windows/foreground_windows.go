//go:build windows

package winime

import (
	"fmt"

	"ime-pilot/internal/core"
	native "ime-pilot/internal/platform/windows"
)

func ResolveEvent(e native.ForegroundEvent) (core.Target, error) {
	t := core.Target{Window: e.Window, PID: e.PID, At: e.At, Valid: e.Valid, HistoryGap: e.HistoryGap}
	if t.PID == 0 {
		return t, fmt.Errorf("foreground window has no process")
	}
	path, err := native.ProcessPath(t.PID)
	if err != nil {
		return t, err
	}
	t.Path = path
	return t, nil
}
