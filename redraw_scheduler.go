package main

import (
	"sync"
	"time"

	"github.com/rivo/tview"
)

// RedrawScheduler coalesces frequent redraw requests into bounded-rate draws.
type RedrawScheduler struct {
	app         *tview.Application
	minInterval time.Duration

	mu      sync.Mutex
	pending bool
	closed  bool
	timer   *time.Timer
	last    time.Time
}

func NewRedrawScheduler(app *tview.Application, minInterval time.Duration) *RedrawScheduler {
	return &RedrawScheduler{
		app:         app,
		minInterval: minInterval,
	}
}

func (r *RedrawScheduler) Request() {
	r.mu.Lock()
	if r.closed || r.pending {
		r.mu.Unlock()
		return
	}

	waitFor := r.minInterval - time.Since(r.last)
	if waitFor < 0 {
		waitFor = 0
	}
	r.pending = true
	r.timer = time.AfterFunc(waitFor, func() {
		r.app.Draw()

		r.mu.Lock()
		r.pending = false
		r.last = time.Now()
		r.mu.Unlock()
	})
	r.mu.Unlock()
}

func (r *RedrawScheduler) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.closed = true
	if r.timer != nil {
		r.timer.Stop()
	}
}
