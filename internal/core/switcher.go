package core

import "context"

// Switcher exposes intent only; application identity and ownership stay in core.
type Switcher interface {
	SwitchEn(context.Context) error
	SwitchCN(context.Context) error
}

// Inspection and verified results are optional. Schemes without these features
// must not be presented as having verified the resulting input state.
type Inspector interface{ Inspect() (State, error) }
type Reporter interface{ Report() Result }

type SwitcherFactory func(valid func() bool) (Switcher, error)

func Switch(ctx context.Context, s Switcher, mode Mode) (Result, bool, error) {
	var err error
	switch mode {
	case Chinese:
		err = s.SwitchCN(ctx)
	case English:
		err = s.SwitchEn(ctx)
	default:
		return Result{}, false, ErrInvalidMode
	}
	if reporter, ok := s.(Reporter); ok {
		return reporter.Report(), true, err
	}
	return Result{}, false, err
}
