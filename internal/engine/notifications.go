package engine

import (
	"context"
	"errors"
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"sync/atomic"
)

// Notification carries a committed local event. PID is the full logical session
// identity, not an operating-system process ID.
type Notification struct {
	PID              uint64
	Channel, Payload string
}
type notificationAction struct {
	channel     string
	listen, all bool
}

// Each immutable link belongs to one recipient; event storage is shared by all
// recipients of a publication. Back lists are newest-first, front lists FIFO.
type notificationLink struct {
	batch []Notification
	next  *notificationLink
}
type notificationFIFO struct {
	front, back *notificationLink
	offset      int
}

func (q notificationFIFO) append(batch []Notification) notificationFIFO {
	if len(batch) != 0 {
		q.back = &notificationLink{batch: batch, next: q.back}
	}
	return q
}
func reverseNotificationLinks(p *notificationLink) *notificationLink {
	var result *notificationLink
	for ; p != nil; p = p.next {
		result = &notificationLink{batch: p.batch, next: result}
	}
	return result
}
func (q notificationFIFO) normalize() notificationFIFO {
	if q.front == nil && q.back != nil {
		if q.back.next == nil {
			q.front = q.back
		} else {
			q.front = reverseNotificationLinks(q.back)
		}
		q.back = nil
		q.offset = 0
	}
	return q
}

type notificationState struct {
	channels map[string]struct{}
	history  bool
	pending  *notificationLink
	ready    notificationFIFO
}

// Held in the session payload: unused sessions have no queue, map, channel or registry
// allocation. Except for the monotonic used hint, all mutable fields below,
// including busy, belong exclusively to Engine.mu. Busy is relevant only to
// registered recipients; unused owners need no extra idle-boundary lock.
// Transaction owner admission/mu may precede Engine.mu; fanout never acquires
// recipient owner or lifecycle locks.
type sessionNotifications struct {
	// Monotonic fast path only; queue authority remains under Engine.mu.
	used        atomic.Bool
	engine      *Engine // Owns the registry; set with token before attachment.
	token       ids.SessionToken
	state       *notificationState
	busy, ended bool
	wake        chan struct{}
}

func (n *sessionNotifications) signalLocked() {
	if n.wake != nil {
		close(n.wake)
		n.wake = nil
	}
}
func (n *sessionNotifications) EndSession(token ids.SessionToken, _ EndReason) error {
	if token != n.token {
		return errors.New("stuntgres: foreign notification session")
	}
	e := n.engine
	e.mu.Lock()
	defer e.mu.Unlock()
	if !n.ended {
		n.ended = true
		n.state = nil
		if e.notifications[token] == n {
			delete(e.notifications, token)
		}
		n.signalLocked()
	}
	return nil
}

// WaitForNotification consumes the next client-ready notification of op's
// session, waiting until one arrives or op's context ends. The listener is root
// state guarded by Engine.mu, so polling lives on the engine.
func (e *Engine) WaitForNotification(op *Operation) (Notification, error) {
	if op == nil || op.self != op || op.session == nil || engineOf(op.session) != e {
		return Notification{}, errors.New("stuntgres: foreign notification operation")
	}
	for {
		event, available, wake, err := e.pollNotification(op.session, op.Context().Err)
		if err != nil || available {
			return event, err
		}
		select {
		case <-op.Context().Done():
		case <-wake:
		}
	}
}

// PollProtocolNotification observes s's queue and registers its wake atomically.
// It owns no operation/activity lease: idle timeout and retirement stay live.
// The serialized wire dispatcher supplies its separate TxIdle delivery gate.
func (e *Engine) PollProtocolNotification(s *Session) (Notification, bool, <-chan struct{}, error) {
	if s == nil || engineOf(s) != e {
		return Notification{}, false, nil, errors.New("stuntgres: foreign notification session")
	}
	return e.pollNotification(s, func() error { return s.Check(context.Background()) })
}

func (e *Engine) pollNotification(s *Session, check func() error) (Notification, bool, <-chan struct{}, error) {
	n := &payloadOf(s).notifications
	for {
		if check != nil {
			if err := check(); err != nil {
				return Notification{}, false, nil, err
			}
		}
		e.mu.Lock()
		if n.ended {
			e.mu.Unlock()
			if check != nil {
				if err := check(); err != nil {
					return Notification{}, false, nil, err
				}
			}
			return Notification{}, false, nil, errors.New("stuntgres: notification session ended")
		}
		original := n.state
		var queue notificationFIFO
		if original != nil {
			queue = original.ready
		}
		if queue.front != nil || queue.back != nil {
			e.mu.Unlock()
			queue = queue.normalize()
			event := queue.front.batch[queue.offset]
			queue.offset++
			if queue.offset == len(queue.front.batch) {
				queue.front = queue.front.next
				queue.offset = 0
			}
			next := *original
			next.ready = queue
			e.mu.Lock()
			if n.state != original || n.ended {
				e.mu.Unlock()
				continue
			}
			if check != nil {
				if err := check(); err != nil {
					e.mu.Unlock()
					return Notification{}, false, nil, err
				}
			}
			n.state = &next
			e.mu.Unlock()
			return event, true, nil, nil
		}
		wake := n.wake
		if wake == nil {
			e.mu.Unlock()
			candidate := make(chan struct{})
			e.mu.Lock()
			if n.state != original || n.ended {
				e.mu.Unlock()
				continue
			}
			if n.wake == nil {
				n.wake = candidate
			}
			wake = n.wake
		}
		e.mu.Unlock()
		return Notification{}, false, wake, nil
	}
}

func appendNotificationEvents(base, added []Notification) []Notification {
	if len(base) == 0 && len(added) <= 1 {
		return added
	}
	for _, event := range added {
		duplicate := false
		for _, old := range base {
			if old.Channel == event.Channel && old.Payload == event.Payload {
				duplicate = true
				break
			}
		}
		if !duplicate {
			base = append(base, event)
		}
	}
	return base
}
func (s *StatementScope) executeNotification(stmt parser.Statement, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	switch raw := stmt.(type) {
	case *parser.Notify:
		if len(raw.Payload) >= 8000 {
			return Result{}, sqlError("22023", "payload string too long")
		}
		s.notificationEvents = append(s.notificationEvents, Notification{PID: uint64(s.op.session.token.ID()), Channel: raw.Channel.Text, Payload: raw.Payload})
	case *parser.Listen:
		s.notificationActions = append(s.notificationActions, notificationAction{channel: raw.Channel.Text, listen: true})
	case *parser.Unlisten:
		e := s.engine()
		e.mu.Lock()
		state := payloadOf(s.op.session).notifications.state
		history := state != nil && state.history
		e.mu.Unlock()
		if history || len(s.owner.notificationActions) != 0 || len(s.notificationActions) != 0 {
			s.notificationActions = append(s.notificationActions, notificationAction{channel: raw.Channel.Text, all: raw.All})
		}
	}
	return Result{}, nil
}

// Called owner-first only at the outer idle/message boundary. Abort drops local
// effects but must still release committed peer events. CHAIN and intermediate
// COMMIT segments deliberately leave busy true and do not scan.
func finishNotificationBoundary(t *transactionOwner) {
	if t.state != TxIdle || t.retainsTransaction() {
		return
	}
	e, n := engineOf(t.session), &payloadOf(t.session).notifications
	if !n.used.Load() {
		return
	}
	for {
		e.mu.Lock()
		original := n.state
		if n.ended || original == nil || original.pending == nil {
			n.busy = false
			e.mu.Unlock()
			return
		}
		e.mu.Unlock()
		next := *original
		next.ready = scanNotificationPending(next.ready, next.pending, next.channels)
		next.pending = nil
		e.mu.Lock()
		if n.state != original || n.ended {
			e.mu.Unlock()
			continue
		}
		n.state = &next
		n.busy = false
		n.signalLocked()
		e.mu.Unlock()
		return
	}
}
func filterNotificationBatch(batch []Notification, channels map[string]struct{}) []Notification {
	count := 0
	for _, event := range batch {
		if _, ok := channels[event.Channel]; ok {
			count++
		}
	}
	if count == len(batch) {
		return batch
	}
	if count == 0 {
		return nil
	}
	filtered := make([]Notification, 0, count)
	for _, event := range batch {
		if _, ok := channels[event.Channel]; ok {
			filtered = append(filtered, event)
		}
	}
	return filtered
}

func scanNotificationPending(ready notificationFIFO, pending *notificationLink, channels map[string]struct{}) notificationFIFO {
	if pending == nil {
		return ready
	}
	if pending.next == nil {
		return ready.append(filterNotificationBatch(pending.batch, channels))
	}
	count := 0
	for p := pending; p != nil; p = p.next {
		count++
	}
	order := make([]*notificationLink, 0, count)
	for p := pending; p != nil; p = p.next {
		order = append(order, p)
	}
	for i := len(order) - 1; i >= 0; i-- {
		ready = ready.append(filterNotificationBatch(order[i].batch, channels))
	}
	return ready
}
