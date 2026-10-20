package engine

import (
	"errors"
	"maps"

	"stuntgres/internal/engine/ids"
)

type notificationCapture struct {
	resource    *sessionNotifications
	original    *notificationState
	busy, ended bool
	next        *notificationState
}
type notificationPublication struct {
	captured []notificationCapture
	registry map[ids.SessionToken]*sessionNotifications
	count    int
}

func notificationEffects(op *Operation, ref ids.TxRef, phase *preparedPublication, scope *StatementScope) ([]notificationAction, []Notification, error) {
	// PREPARE rejects notification effects. Completion owns only the detached
	// entry: even checking its old attachment would read a reusable live owner.
	if phase != nil && !phase.preparing {
		return nil, nil, nil
	}
	if op == nil || op.self != op || op.session == nil || op.session.transactions == nil ||
		txOwnerOf(ref) != op.session.transactions {
		return nil, nil, errors.New("stuntgres: foreign notification transaction")
	}
	// The caller holds this session's transaction admission. Validate against
	// that owner, never against an owner reached through an untrusted old ref.
	owner := op.session.transactions
	if err := owner.validate(ref); err != nil {
		return nil, nil, err
	}
	if scope != nil && (scope.op != op || scope.owner != owner) {
		return nil, nil, errors.New("stuntgres: foreign notification statement")
	}
	actions, events := owner.notificationActions, owner.notificationEvents
	if scope != nil {
		if len(actions) == 0 {
			actions = scope.notificationActions
		} else {
			actions = append(actions, scope.notificationActions...)
		}
		events = appendNotificationEvents(events, scope.notificationEvents)
	}
	return actions, events, nil
}

// Capture under Engine.mu, prepare independent immutable replacements unlocked,
// then validate exact roots/membership in the existing row/catalog publisher.
// There is no secondary notification commit authority or counter to exhaust.
func (e *Engine) prepareNotificationPublication(sender *Session, actions []notificationAction, events []Notification) *notificationPublication {
	if len(actions) == 0 && len(events) == 0 {
		return nil
	}
	p := &notificationPublication{}
	e.mu.Lock()
	p.count = len(e.notifications)
	p.captured = make([]notificationCapture, 0, p.count+1)
	own := &payloadOf(sender).notifications
	ownFound := false
	for _, n := range e.notifications {
		p.captured = append(p.captured, notificationCapture{resource: n, original: n.state, busy: n.busy, ended: n.ended})
		ownFound = ownFound || n == own
	}
	if !ownFound {
		p.captured = append(p.captured, notificationCapture{resource: own, original: own.state, busy: own.busy, ended: own.ended})
	}
	e.mu.Unlock()
	batch := events[:len(events):len(events)]
	for i := range p.captured {
		c := &p.captured[i]
		if c.ended {
			continue
		}
		var next notificationState
		if c.original != nil {
			next = *c.original
		}
		wasRegistered := len(next.channels) != 0
		changed := c.resource == own && len(actions) != 0
		if c.resource == own && len(actions) != 0 {
			next.channels = maps.Clone(next.channels)
			for _, action := range actions {
				if action.listen {
					if next.channels == nil {
						next.channels = make(map[string]struct{})
					}
					next.channels[action.channel] = struct{}{}
					next.history = true
				} else if action.all {
					clear(next.channels)
				} else {
					delete(next.channels, action.channel)
				}
			}
			if len(next.channels) == 0 {
				next.pending = nil
			}
			if wasRegistered != (len(next.channels) != 0) {
				p.registry = make(map[ids.SessionToken]*sessionNotifications, p.count+1)
				for _, entry := range p.captured {
					if entry.resource != own && !entry.ended && entry.original != nil && len(entry.original.channels) != 0 {
						p.registry[entry.resource.token] = entry.resource
					}
				}
				if len(next.channels) != 0 {
					p.registry[own.token] = own
				}
			}
		}
		if len(batch) != 0 && len(next.channels) != 0 {
			if c.busy {
				next.pending = &notificationLink{batch: batch, next: next.pending}
				changed = true
			} else {
				ready := next.ready.append(filterNotificationBatch(batch, next.channels))
				changed = changed || ready != next.ready
				next.ready = ready
			}
		}
		if changed {
			c.next = &notificationState{channels: next.channels, history: next.history, pending: next.pending, ready: next.ready}
		}
	}
	return p
}
func (e *Engine) notificationPublicationUnchangedLocked(p *notificationPublication) bool {
	if p == nil {
		return true
	}
	if len(e.notifications) != p.count {
		return false
	}
	for _, c := range p.captured {
		n := c.resource
		if n.state != c.original || n.busy != c.busy || n.ended != c.ended {
			return false
		}
		registered := c.original != nil && len(c.original.channels) != 0 && !c.ended
		if (e.notifications[n.token] == n) != registered {
			return false
		}
	}
	return true
}
func (e *Engine) installNotificationsLocked(p *notificationPublication) {
	if p == nil {
		return
	}
	if p.registry != nil {
		e.notifications = p.registry
	}
	for _, c := range p.captured {
		if c.next != nil {
			c.resource.state = c.next
			c.resource.used.Store(true)
			c.resource.signalLocked()
		}
	}
}
