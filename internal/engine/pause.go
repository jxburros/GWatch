package engine

import (
	"context"
	"errors"
	"sync"
)

// Pausing the engine (#148). A restore replaces the configuration and,
// optionally, the history underneath a running engine. While it does, nothing
// the engine does on its own should write to the database or act on what it
// finds there: a check result recorded against a check the restore is about to
// remove, a rollup or retention pass over a history being re-imported, a
// scheduled backup of a half-restored database, or a scheduler running checks
// against configuration that is neither the old nor the new. Pause stops all of
// that and waits for whatever is already running to finish; Resume lets it
// carry on. The caller reloads the configuration (ReloadConfig) before
// resuming, so the scheduler picks up from the restored state.
//
// It is a gate rather than a stop: the loops keep ticking (the scheduler keeps
// its clock, so a long restore is not mistaken for the computer having been
// asleep), they just skip their work while the gate is shut. Each unit of work
// that writes enters the gate first (enterWork) and leaves it when done
// (leaveWork); Pause shuts the gate to newcomers and waits until those inside
// have left. The units are: a scheduled or requested check run, a pushed
// hardware reading being judged, a rollup/retention pass, a scheduled backup,
// a scheduled report, and the maintenance-window tracker. Anything new that
// writes on the engine's own initiative should enter the gate the same way.

// ErrPaused is returned by work refused because the engine is paused.
var ErrPaused = errors.New("monitoring is paused while a backup is being restored; try again in a moment")

// ErrAlreadyPaused is returned by Pause when the engine is already paused,
// which is how a second restore started while one is running is refused.
var ErrAlreadyPaused = errors.New("monitoring is already paused for another restore")

// workGate counts the units of work in progress and refuses new ones while
// the engine is paused.
type workGate struct {
	mu     sync.Mutex
	paused bool
	active int
	idle   chan struct{} // closed when active drops to zero during a pause
}

// enterWork admits one unit of work, or reports false while the engine is
// paused. Every true must be matched by a leaveWork.
func (e *Engine) enterWork() bool {
	g := &e.gate
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.paused {
		return false
	}
	g.active++
	return true
}

// leaveWork ends a unit of work admitted by enterWork.
func (e *Engine) leaveWork() {
	g := &e.gate
	g.mu.Lock()
	defer g.mu.Unlock()
	g.active--
	if g.active == 0 && g.idle != nil {
		close(g.idle)
		g.idle = nil
	}
}

// Paused reports whether the engine is paused.
func (e *Engine) Paused() bool {
	e.gate.mu.Lock()
	defer e.gate.mu.Unlock()
	return e.gate.paused
}

// Pause stops the engine starting any new work that writes to the database
// and waits for the work already running to finish. If ctx ends first the
// engine is resumed and ctx's error returned, so a failed Pause never leaves
// it paused. A Pause that returns nil must be matched by a Resume, on every
// path. It returns ErrAlreadyPaused if the engine is already paused.
func (e *Engine) Pause(ctx context.Context) error {
	g := &e.gate
	g.mu.Lock()
	if g.paused {
		g.mu.Unlock()
		return ErrAlreadyPaused
	}
	g.paused = true
	if g.active == 0 {
		g.mu.Unlock()
		e.log.Printf("engine paused")
		return nil
	}
	idle := make(chan struct{})
	g.idle = idle
	g.mu.Unlock()
	select {
	case <-idle:
		e.log.Printf("engine paused")
		return nil
	case <-ctx.Done():
		e.Resume()
		return ctx.Err()
	}
}

// Resume lets the engine carry on after Pause. Resuming an engine that is not
// paused does nothing.
func (e *Engine) Resume() {
	g := &e.gate
	g.mu.Lock()
	was := g.paused
	g.paused = false
	g.idle = nil
	g.mu.Unlock()
	if was {
		e.log.Printf("engine resumed")
		e.broadcast(Update{Kind: "state"})
	}
}
