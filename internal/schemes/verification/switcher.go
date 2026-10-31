package verification

import (
	"context"
	"ime-pilot/internal/core"
)

// Executor preserves the existing query/apply/readback workflow for schemes
// that support it, without making it part of the mandatory Switcher contract.
type Executor struct {
	controller *Controller
	last       core.Result
}

func New(backend Backend) *Executor                    { return &Executor{controller: NewController(backend)} }
func (s *Executor) SwitchEn(ctx context.Context) error { return s.switchMode(ctx, core.English) }
func (s *Executor) SwitchCN(ctx context.Context) error { return s.switchMode(ctx, core.Chinese) }
func (s *Executor) switchMode(ctx context.Context, mode core.Mode) error {
	var err error
	s.last, err = s.controller.Switch(ctx, mode)
	return err
}
func (s *Executor) Inspect() (core.State, error) { return s.controller.Inspect() }
func (s *Executor) Report() core.Result          { return s.last }
