package core

import "context"

type App struct {
	Name     string `json:"name"`
	Path     string `json:"path,omitempty"`
	BundleID string `json:"bundleId,omitempty"`
}

type Activation struct {
	Target   Target
	Token    uint64
	Baseline bool
}

type SignalSource interface {
	Run(context.Context, func() uint64, func(Activation)) error
	Capture(Evidence) (Target, error)
	Apps() ([]App, error)
}
