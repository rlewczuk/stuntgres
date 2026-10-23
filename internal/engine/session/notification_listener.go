package session

import (
	"sync/atomic"

	"stuntgres/internal/engine/ids"
)

// NotificationListener is one session's LISTEN/NOTIFY state, held in the
// root session payload: unused sessions have no queue, map, channel or
// registry allocation. Except for the monotonic Used hint, all mutable fields
// below, including Busy, belong exclusively to Engine.mu, which only the root
// engine takes. Busy is relevant only to registered recipients; unused owners
// need no extra idle-boundary lock. Transaction owner admission/mu may precede
// Engine.mu; fanout never acquires recipient owner or lifecycle locks.
type NotificationListener struct {
	// Monotonic fast path only; queue authority remains under Engine.mu.
	Used atomic.Bool
	// The listening session; set before attachment and never changed. Its
	// token keys the registry; the root's end callback reaches the engine
	// through it.
	Session     *Session
	State       *NotificationState
	Busy, Ended bool
	Wake        chan struct{}
}

// SignalLocked wakes the listener's waiter, if any.
func (n *NotificationListener) SignalLocked() {
	if n.Wake != nil {
		close(n.Wake)
		n.Wake = nil
	}
}

// NotificationRegistry holds an engine's committed registrations: the
// listeners with at least one channel, by session token. It stays nil until
// the first registration. The engine holds it under Engine.mu; the …Locked
// methods require that lock and take none.
type NotificationRegistry map[ids.SessionToken]*NotificationListener

// EndLocked retires n when its session ends: it drops n's state and its
// registration, if n still holds it, and wakes n's waiter. Ending twice is a
// no-op.
func (r *NotificationRegistry) EndLocked(n *NotificationListener) {
	if n.Ended {
		return
	}
	n.Ended = true
	n.State = nil
	if token := n.Session.SessionToken; (*r)[token] == n {
		delete(*r, token)
	}
	n.SignalLocked()
}
