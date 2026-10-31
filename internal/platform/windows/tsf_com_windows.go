//go:build windows

package windows

import (
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	winapi "golang.org/x/sys/windows"
)

const (
	profileTextService    = 1
	profileKeyboardLayout = 2
	profileEnabled        = 2
	activateForDesktop    = 0x20000000 // TF_IPPMF_FORSESSION
	comChangedMode        = 0x80010106 // RPC_E_CHANGED_MODE
)

var (
	profilesClass    = winapi.GUID{Data1: 0x33c53a50, Data2: 0xf456, Data3: 0x4884, Data4: [8]byte{0xb0, 0x49, 0x85, 0xfd, 0x64, 0x3e, 0xcf, 0xed}}
	managerIID       = winapi.GUID{Data1: 0x71c6e74c, Data2: 0x0f28, Data3: 0x11d8, Data4: [8]byte{0xa8, 0x2a, 0x00, 0x06, 0x5b, 0x84, 0x43, 0x5c}}
	profilesIID      = winapi.GUID{Data1: 0x1f02b6c5, Data2: 0x7842, Data3: 0x4ee6, Data4: [8]byte{0x8a, 0x0b, 0x9a, 0x24, 0x18, 0x3a, 0x95, 0xca}}
	keyboardCategory = winapi.GUID{Data1: 0x34745c63, Data2: 0xb2f0, Data3: 0x4784, Data4: [8]byte{0x8b, 0x67, 0x5e, 0x12, 0xc8, 0x70, 0x1a, 0x31}}

	ole32            = winapi.NewLazySystemDLL("ole32.dll")
	coInitializeEx   = ole32.NewProc("CoInitializeEx")
	coUninitialize   = ole32.NewProc("CoUninitialize")
	coCreateInstance = ole32.NewProc("CoCreateInstance")
	sysFreeString    = winapi.NewLazySystemDLL("oleaut32.dll").NewProc("SysFreeString")
)

// Native TF_INPUTPROCESSORPROFILE layout; uintptr follows the host pointer size.
// GUIDs have 4-byte alignment, so Language is followed by two padding bytes.
type inputProfile struct {
	Kind             uint32
	Language         uint16
	_                uint16
	CLSID            winapi.GUID
	Profile          winapi.GUID
	Category         winapi.GUID
	SubstituteLayout uintptr
	Capabilities     uint32
	Layout           uintptr
	Flags            uint32
}

func (p inputProfile) id() string {
	return fmt.Sprintf("tsf:%04X:%s:%s", p.Language, strings.ToUpper(p.CLSID.String()), strings.ToUpper(p.Profile.String()))
}

// Named vtable fields mirror msctf.h. Keeping unused entries preserves the COM
// ABI and makes each call's interface/method visible instead of using indexes.
type unknownVTable struct {
	QueryInterface uintptr
	AddRef         uintptr
	Release        uintptr
}

type managerVTable struct {
	unknownVTable
	ActivateProfile   uintptr
	DeactivateProfile uintptr
	GetProfile        uintptr
	EnumProfiles      uintptr
}

type profilesVTable struct {
	unknownVTable
	Register                      uintptr
	Unregister                    uintptr
	AddLanguageProfile            uintptr
	RemoveLanguageProfile         uintptr
	EnumInputProcessorInfo        uintptr
	GetDefaultLanguageProfile     uintptr
	SetDefaultLanguageProfile     uintptr
	ActivateLanguageProfile       uintptr
	GetActiveLanguageProfile      uintptr
	GetLanguageProfileDescription uintptr
	GetCurrentLanguage            uintptr
	ChangeCurrentLanguage         uintptr
}

type enumeratorVTable struct {
	unknownVTable
	Clone uintptr
	Next  uintptr
}

type profileManager struct{ vtable *managerVTable }
type profileDescriptions struct{ vtable *profilesVTable }
type profileEnumerator struct{ vtable *enumeratorVTable }

type tsfClient struct {
	manager      *profileManager
	descriptions *profileDescriptions
}

func tsfError(operation string, result uintptr) error {
	if uint32(result) == 0 {
		return nil
	}
	return fmt.Errorf("%s 未完成（HRESULT=0x%08X）", operation, uint32(result))
}

func withTSF(run func(*tsfClient) error) error {
	// COM objects must be used and released in the apartment that created them.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	result, _, _ := coInitializeEx.Call(0, 2) // COINIT_APARTMENTTHREADED
	switch uint32(result) {
	case 0, 1: // S_OK and S_FALSE both require a balancing CoUninitialize.
		defer coUninitialize.Call()
	case comChangedMode:
		// Reuse an existing apartment without balancing someone else's init.
	default:
		return tsfError("初始化 COM", result)
	}

	var client tsfClient
	result, _, _ = coCreateInstance.Call(
		uintptr(unsafe.Pointer(&profilesClass)), 0, 1, // CLSCTX_INPROC_SERVER
		uintptr(unsafe.Pointer(&managerIID)), uintptr(unsafe.Pointer(&client.manager)),
	)
	if err := tsfError("创建 TSF 管理器", result); err != nil {
		return err
	}
	defer syscall.SyscallN(client.manager.vtable.Release, uintptr(unsafe.Pointer(client.manager)))
	result, _, _ = syscall.SyscallN(client.manager.vtable.QueryInterface,
		uintptr(unsafe.Pointer(client.manager)), uintptr(unsafe.Pointer(&profilesIID)),
		uintptr(unsafe.Pointer(&client.descriptions)),
	)
	if err := tsfError("获取输入法描述接口", result); err != nil {
		return err
	}
	defer syscall.SyscallN(client.descriptions.vtable.Release, uintptr(unsafe.Pointer(client.descriptions)))
	return run(&client)
}

func (c *tsfClient) enabledProfiles() ([]InputSource, error) {
	var enumerator *profileEnumerator
	result, _, _ := syscall.SyscallN(c.manager.vtable.EnumProfiles,
		uintptr(unsafe.Pointer(c.manager)), 0, uintptr(unsafe.Pointer(&enumerator)),
	)
	if err := tsfError("枚举输入法", result); err != nil {
		return nil, err
	}
	defer syscall.SyscallN(enumerator.vtable.Release, uintptr(unsafe.Pointer(enumerator)))

	var available []InputSource
	for count := 0; count < 4096; count++ {
		var profile inputProfile
		var fetched uint32
		result, _, _ = syscall.SyscallN(enumerator.vtable.Next,
			uintptr(unsafe.Pointer(enumerator)), 1,
			uintptr(unsafe.Pointer(&profile)), uintptr(unsafe.Pointer(&fetched)),
		)
		if uint32(result) == 1 && fetched == 0 { // S_FALSE: end of enumeration.
			return available, nil
		}
		if err := tsfError("读取输入法配置", result); err != nil {
			return nil, err
		}
		if fetched != 1 {
			return nil, fmt.Errorf("TSF 返回了无效的输入法列表")
		}
		if profile.Kind != profileTextService || profile.Flags&profileEnabled == 0 || profile.Category != keyboardCategory {
			continue
		}
		available = append(available, InputSource{
			ID: profile.id(), Name: c.description(profile),
			Language: profile.Language & 0x3ff, profile: profile,
		})
	}
	return nil, fmt.Errorf("TSF 输入法列表超过读取上限")
}

func (c *tsfClient) description(profile inputProfile) string {
	var name *uint16 // COM returns a BSTR owned by this caller.
	result, _, _ := syscall.SyscallN(c.descriptions.vtable.GetLanguageProfileDescription,
		uintptr(unsafe.Pointer(c.descriptions)), uintptr(unsafe.Pointer(&profile.CLSID)),
		uintptr(profile.Language), uintptr(unsafe.Pointer(&profile.Profile)), uintptr(unsafe.Pointer(&name)),
	)
	if name != nil {
		defer sysFreeString.Call(uintptr(unsafe.Pointer(name)))
		if uint32(result) == 0 {
			if label := winapi.UTF16PtrToString(name); label != "" {
				return label
			}
		}
	}
	return "输入法 " + profile.Profile.String()
}

func (c *tsfClient) activate(target InputSource, check func() error) error {
	profile := target.profile
	if profile.Kind == 0 {
		profile = inputProfile{Kind: profileKeyboardLayout, Language: uint16(target.Handle), Layout: target.Handle}
	}
	// ActivateProfile requires the requested language to be current. Change it
	// on our COM thread first; never use ENABLEPROFILE or alter user defaults.
	var language uint16
	result, _, _ := syscall.SyscallN(c.descriptions.vtable.GetCurrentLanguage,
		uintptr(unsafe.Pointer(c.descriptions)), uintptr(unsafe.Pointer(&language)),
	)
	if err := tsfError("读取输入语言", result); err != nil {
		return err
	}
	if language != profile.Language {
		if err := check(); err != nil {
			return err
		}
		result, _, _ = syscall.SyscallN(c.descriptions.vtable.ChangeCurrentLanguage,
			uintptr(unsafe.Pointer(c.descriptions)), uintptr(profile.Language),
		)
		if err := tsfError("切换输入语言", result); err != nil {
			return err
		}
	}
	if err := check(); err != nil {
		return err
	}
	handle := profile.Layout
	if profile.Kind == profileTextService {
		handle = 0 // TSF requires a null HKL for a text-service profile.
	}
	result, _, _ = syscall.SyscallN(c.manager.vtable.ActivateProfile,
		uintptr(unsafe.Pointer(c.manager)), uintptr(profile.Kind), uintptr(profile.Language),
		uintptr(unsafe.Pointer(&profile.CLSID)), uintptr(unsafe.Pointer(&profile.Profile)), handle,
		activateForDesktop,
	)
	// A synchronous COM call cannot be interrupted by context cancellation.
	// The caller checks its deadline again and never treats a late result as confirmed.
	return tsfError("激活桌面输入源 "+target.Name, result)
}
