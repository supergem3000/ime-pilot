//go:build windows

package windows

import "fmt"

func InputSources() ([]InputSource, error) {
	available, err := KeyboardLayouts()
	if err != nil {
		return nil, err
	}
	err = withTSF(func(client *tsfClient) error {
		profiles, err := client.enabledProfiles()
		if err == nil {
			available = append(available, profiles...)
		}
		return err
	})
	return available, err
}

// TSF profiles can share a language HKL. It is only used to switch language;
// the subsequent ActivateProfile call selects the specific input method.
func LanguageLayout(target InputSource) (uintptr, error) {
	if target.Handle != 0 {
		return target.Handle, nil
	}
	available, err := KeyboardLayouts()
	if err != nil {
		return 0, err
	}
	var matches []uintptr
	for _, item := range available {
		if uint16(item.Handle) != target.profile.Language {
			continue
		}
		if item.Handle == target.profile.SubstituteLayout || item.Handle == target.profile.Layout {
			return item.Handle, nil
		}
		matches = append(matches, item.Handle)
	}
	if len(matches) != 1 {
		return 0, fmt.Errorf("无法确定输入法 %s 对应的已加载语言布局", target.Name)
	}
	return matches[0], nil
}

// ActivateDesktopInputSource uses TSF's desktop scope, not just one HWND.
// The guard is called before each mutation and after synchronous COM returns.
// The caller decides whether that scope and the unverified result are acceptable.
func ActivateDesktopInputSource(target InputSource, check func() error) error {
	return withTSF(func(client *tsfClient) error {
		if err := check(); err != nil {
			return err
		}
		if err := client.activate(target, check); err != nil {
			return err
		}
		return check()
	})
}
