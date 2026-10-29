package core

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeBackend struct {
	states   []State
	readErr  error
	applyErr error
	reads    int
	applies  int
	onRead   func(int)
}

func (b *fakeBackend) Snapshot() (State, error) {
	if b.readErr != nil {
		return State{}, b.readErr
	}
	index := b.reads
	if index >= len(b.states) {
		index = len(b.states) - 1
	}
	b.reads++
	if b.onRead != nil {
		b.onRead(b.reads)
	}
	return b.states[index], nil
}

func (b *fakeBackend) Apply(context.Context, State, Mode) error {
	b.applies++
	return b.applyErr
}

func inputState(mode Mode) State {
	return State{Mode: mode, ContextID: "window-1/context-1", InputSourceID: "layout-0804"}
}

func TestSwitchDoesNotTreatTheSameLayoutAsEnglish(t *testing.T) {
	backend := &fakeBackend{states: []State{inputState(Chinese), inputState(Chinese)}}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	result, err := NewController(backend).Switch(ctx, English)
	if !errors.Is(err, ErrVerification) || !errors.Is(err, context.DeadlineExceeded) || result.Changed {
		t.Fatalf("unverified change reported as success: result=%+v error=%v", result, err)
	}
	if backend.applies != 1 {
		t.Fatalf("expected one setting call, got %d", backend.applies)
	}
}

func TestSwitchVerifiesTheInternalMode(t *testing.T) {
	backend := &fakeBackend{states: []State{inputState(Chinese), inputState(English)}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := NewController(backend).Switch(ctx, English)
	if err != nil || !result.Changed || result.After.Mode != English {
		t.Fatalf("unexpected result: %+v, %v", result, err)
	}
}

func TestSwitchSkipsAlreadySelected(t *testing.T) {
	backend := &fakeBackend{states: []State{inputState(English)}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := NewController(backend).Switch(ctx, English)
	if err != nil || result.Changed || backend.applies != 0 {
		t.Fatalf("already-English state was changed: %+v, %v", result, err)
	}
}

func TestSwitchRefusesUnknownState(t *testing.T) {
	for _, test := range []struct {
		name  string
		state State
		want  error
	}{
		{"unknown mode", inputState(Unknown), ErrUnsupported},
		{"unknown identity", State{Mode: Chinese}, ErrUnsupported},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := &fakeBackend{states: []State{test.state}}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, err := NewController(backend).Switch(ctx, English)
			if !errors.Is(err, test.want) || backend.applies != 0 {
				t.Fatalf("unsafe setting: applies=%d error=%v", backend.applies, err)
			}
		})
	}
}

func TestSwitchRejectsChangedContextOrLayoutDuringVerification(t *testing.T) {
	for _, change := range []string{"context", "layout"} {
		t.Run(change, func(t *testing.T) {
			after := inputState(English)
			if change == "context" {
				after.ContextID = "window-2/context-2"
			} else {
				after.InputSourceID = "another-layout"
			}
			backend := &fakeBackend{states: []State{inputState(Chinese), after}}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			result, err := NewController(backend).Switch(ctx, English)
			if !errors.Is(err, ErrFocusChanged) || result.Changed {
				t.Fatalf("verified another target: %+v, %v", result, err)
			}
		})
	}
}

func TestSwitchDoesNotContinueAfterSetFailureOrCancellation(t *testing.T) {
	setFailure := errors.New("Windows rejected setting")
	backend := &fakeBackend{states: []State{inputState(Chinese)}, applyErr: setFailure}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := NewController(backend).Switch(ctx, English)
	if !errors.Is(err, setFailure) || backend.reads != 1 || backend.applies != 1 {
		t.Fatalf("continued after rejection: %+v, %v", backend, err)
	}
	cancelled, stop := context.WithTimeout(context.Background(), time.Second)
	stop()
	backend = &fakeBackend{states: []State{inputState(Chinese)}}
	_, err = NewController(backend).Switch(cancelled, English)
	if !errors.Is(err, context.Canceled) || backend.reads != 0 || backend.applies != 0 {
		t.Fatalf("performed cancelled operation: %+v, %v", backend, err)
	}
}

func TestCancellationDuringQueryDoesNotBecomeSuccess(t *testing.T) {
	for _, cancelOnRead := range []int{1, 2} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		backend := &fakeBackend{states: []State{inputState(Chinese), inputState(English)}}
		backend.onRead = func(readNumber int) {
			if readNumber == cancelOnRead {
				cancel()
			}
		}
		result, err := NewController(backend).Switch(ctx, English)
		cancel()
		if !errors.Is(err, context.Canceled) || result.Changed {
			t.Fatalf("cancelled query %d became success: %+v, %v", cancelOnRead, result, err)
		}
		if cancelOnRead == 1 && backend.applies != 0 {
			t.Fatal("setting followed a cancelled initial query")
		}
	}
}
