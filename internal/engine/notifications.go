package engine

import (
	"context"
	"errors"
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// notificationSessionEnd is a session's listener seen as its Notifications
// resource. It is a pointer conversion of the listener, so attaching it
// allocates nothing.
type notificationSessionEnd session.NotificationListener

func (r *notificationSessionEnd) EndSession(token ids.SessionToken, _ session.EndReason) error {
	n := (*session.NotificationListener)(r)
	if token != n.Session.SessionToken {
		return errors.New("stuntgres: foreign notification session")
	}
	e := engineOf(n.Session)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.notifications.EndLocked(n)
	return nil
}

// WaitForNotification consumes the next client-ready notification of op's
// session, waiting until one arrives or op's context ends. The listener is root
// state guarded by Engine.mu, so polling lives on the engine.
func (e *Engine) WaitForNotification(op *session.Operation) (session.Notification, error) {
	if op == nil || op.Self != op || op.Session == nil || engineOf(op.Session) != e {
		return session.Notification{}, errors.New("stuntgres: foreign notification operation")
	}
	for {
		event, available, wake, err := e.pollNotification(op.Session, op.Context().Err)
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
func (e *Engine) PollProtocolNotification(s *session.Session) (session.Notification, bool, <-chan struct{}, error) {
	if s == nil || engineOf(s) != e {
		return session.Notification{}, false, nil, errors.New("stuntgres: foreign notification session")
	}
	return e.pollNotification(s, func() error { return s.Check(context.Background()) })
}

func (e *Engine) pollNotification(s *session.Session, check func() error) (session.Notification, bool, <-chan struct{}, error) {
	n := &payloadOf(s).notifications
	for {
		if check != nil {
			if err := check(); err != nil {
				return session.Notification{}, false, nil, err
			}
		}
		e.mu.Lock()
		if n.Ended {
			e.mu.Unlock()
			if check != nil {
				if err := check(); err != nil {
					return session.Notification{}, false, nil, err
				}
			}
			return session.Notification{}, false, nil, errors.New("stuntgres: notification session ended")
		}
		original := n.State
		var queue session.NotificationFIFO
		if original != nil {
			queue = original.Ready
		}
		if queue.Front != nil || queue.Back != nil {
			e.mu.Unlock()
			queue = queue.Normalize()
			event := queue.Front.Batch[queue.Offset]
			queue.Offset++
			if queue.Offset == len(queue.Front.Batch) {
				queue.Front = queue.Front.Next
				queue.Offset = 0
			}
			next := *original
			next.Ready = queue
			e.mu.Lock()
			if n.State != original || n.Ended {
				e.mu.Unlock()
				continue
			}
			if check != nil {
				if err := check(); err != nil {
					e.mu.Unlock()
					return session.Notification{}, false, nil, err
				}
			}
			n.State = &next
			e.mu.Unlock()
			return event, true, nil, nil
		}
		wake := n.Wake
		if wake == nil {
			e.mu.Unlock()
			candidate := make(chan struct{})
			e.mu.Lock()
			if n.State != original || n.Ended {
				e.mu.Unlock()
				continue
			}
			if n.Wake == nil {
				n.Wake = candidate
			}
			wake = n.Wake
		}
		e.mu.Unlock()
		return session.Notification{}, false, wake, nil
	}
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
		s.notificationEvents = append(s.notificationEvents, session.Notification{PID: uint64(s.op.Session.SessionToken.ID()), Channel: raw.Channel.Text, Payload: raw.Payload})
	case *parser.Listen:
		s.notificationActions = append(s.notificationActions, session.NotificationAction{Channel: raw.Channel.Text, Listen: true})
	case *parser.Unlisten:
		e := s.engine()
		e.mu.Lock()
		state := payloadOf(s.op.Session).notifications.State
		history := state != nil && state.History
		e.mu.Unlock()
		if history || len(s.owner.NotificationActions) != 0 || len(s.notificationActions) != 0 {
			s.notificationActions = append(s.notificationActions, session.NotificationAction{Channel: raw.Channel.Text, All: raw.All})
		}
	}
	return Result{}, nil
}

// Called owner-first only at the outer idle/message boundary. Abort drops local
// effects but must still release committed peer events. CHAIN and intermediate
// COMMIT segments deliberately leave busy true and do not scan.
func finishNotificationBoundary(t *session.TransactionOwner) {
	if t.State != session.TxIdle || t.RetainsTransaction() {
		return
	}
	e, n := engineOf(t.Session), &payloadOf(t.Session).notifications
	if !n.Used.Load() {
		return
	}
	for {
		e.mu.Lock()
		original := n.State
		if n.Ended || original == nil || original.Pending == nil && !original.Unread {
			n.Busy = false
			e.mu.Unlock()
			return
		}
		e.mu.Unlock()
		next := *original
		next.Ready = session.ScanNotificationPending(next.Ready, next.Pending, next.Channels)
		next.Pending = nil
		next.Unread = false
		next.Position = session.NotificationQueuePosition{}
		e.mu.Lock()
		if n.State != original || n.Ended {
			e.mu.Unlock()
			continue
		}
		n.State = &next
		n.Busy = false
		n.SignalLocked()
		e.mu.Unlock()
		return
	}
}
