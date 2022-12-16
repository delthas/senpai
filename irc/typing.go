package irc

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Typing is an event of Name actively typing in Target.
type Typing struct {
	Target string
	Name   string
}

// Typings keeps track of typing notification timeouts.
type Typings struct {
	l        sync.Mutex
	closed   bool                 // whether Close has been called
	targets  map[Typing]time.Time // @+typing TAGMSG timestamps.
	timeouts chan Typing          // transmits unfiltered timeout notifications.
	stops    chan Typing          // transmits filtered timeout notifications.
}

// NewTypings initializes the Typings structures and filtering coroutine.
func NewTypings() *Typings {
	ts := &Typings{
		targets:  map[Typing]time.Time{},
		timeouts: make(chan Typing, 16),
		stops:    make(chan Typing, 16),
	}
	go func() {
		for t := range ts.timeouts {
			now := time.Now()
			ts.l.Lock()
			oldT, ok := ts.targets[t]
			if ok && 6.0 < now.Sub(oldT).Seconds() {
				delete(ts.targets, t)
				ts.l.Unlock()
				ts.stops <- t
			} else {
				ts.l.Unlock()
			}
		}
	}()
	return ts
}

// Close cleanly closes all channels and stops all goroutines.
func (ts *Typings) Close() {
	ts.l.Lock()
	defer ts.l.Unlock()

	close(ts.timeouts)
	close(ts.stops)
	ts.closed = true
}

// Stops is a channel that transmits typing timeouts.
func (ts *Typings) Stops() <-chan Typing {
	return ts.stops
}

// Active should be called when a user is typing to some target.
func (ts *Typings) Active(target, name string) {
	ts.l.Lock()
	t := Typing{target, name}
	ts.targets[t] = time.Now()
	ts.l.Unlock()

	go func() {
		time.Sleep(6 * time.Second)

		ts.l.Lock()
		closed := ts.closed
		ts.l.Unlock()

		if !closed {
			ts.timeouts <- t
		}
	}()
}

// Done should be called when a user is done typing to some target.
func (ts *Typings) Done(target, name string) {
	ts.l.Lock()
	delete(ts.targets, Typing{target, name})
	ts.l.Unlock()
}

func (ts *Typings) List(target string) []string {
	ts.l.Lock()
	defer ts.l.Unlock()

	var res []string
	for t := range ts.targets {
		if target == t.Target {
			res = append(res, t.Name)
		}
	}
	return res
}

type typingStamp struct {
	Last  time.Time
	Type  int
	Limit *rate.Limiter
}
