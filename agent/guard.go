package main

import (
	"fmt"
	"sync/atomic"
	"time"
)

// Collectors run one after another in the main loop. Without a guard, one that
// hangs (a stale network mount, a stuck system tool) stops all telemetry for the
// machine and one that panics takes the whole agent down. A guarded call has a
// deadline and recovers panics; while a timed-out call is still running its next
// cycle is skipped instead of piling up another goroutine.
const (
	collectTimeout     = 45 * time.Second
	slowCollectTimeout = 5 * time.Minute
)

type collectGuard struct{ running atomic.Bool }

// guardSet keeps one guard per collector name. It is used from a single loop.
type guardSet map[string]*collectGuard

func (s guardSet) run(name string, timeout time.Duration, fn func() (map[string]interface{}, error)) (map[string]interface{}, error) {
	g, ok := s[name]
	if !ok {
		g = &collectGuard{}
		s[name] = g
	}
	return g.run(name, timeout, fn)
}

func (g *collectGuard) run(name string, timeout time.Duration, fn func() (map[string]interface{}, error)) (map[string]interface{}, error) {
	if !g.running.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("%s is still running from an earlier cycle; skipped", name)
	}
	type result struct {
		v   map[string]interface{}
		err error
	}
	ch := make(chan result, 1) // buffered: a late finisher never blocks
	go func() {
		defer g.running.Store(false)
		defer func() {
			if r := recover(); r != nil {
				ch <- result{err: fmt.Errorf("%s panicked: %v", name, r)}
			}
		}()
		v, err := fn()
		ch <- result{v, err}
	}()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-time.After(timeout):
		return nil, fmt.Errorf("%s timed out after %v", name, timeout)
	}
}
