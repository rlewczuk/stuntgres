package engine

import (
	"errors"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

func notificationEffects(op *session.Operation, ref ids.TxRef, phase *preparedPublication, scope *StatementScope) ([]session.NotificationAction, []session.Notification, error) {
	// PREPARE rejects notification effects. Completion owns only the detached
	// entry: even checking its old attachment would read a reusable live owner.
	if phase != nil && !phase.preparing {
		return nil, nil, nil
	}
	if op == nil || op.Self != op || op.Session == nil || op.Session.Transactions == nil ||
		session.TxOwnerOf(ref) != op.Session.Transactions {
		return nil, nil, errors.New("stuntgres: foreign notification transaction")
	}
	// The caller holds this session's transaction admission. Validate against
	// that owner, never against an owner reached through an untrusted old ref.
	owner := op.Session.Transactions
	if err := owner.Validate(ref); err != nil {
		return nil, nil, err
	}
	if scope != nil && (scope.op != op || scope.owner != owner) {
		return nil, nil, errors.New("stuntgres: foreign notification statement")
	}
	actions, events := owner.NotificationActions, owner.NotificationEvents
	if scope != nil {
		if len(actions) == 0 {
			actions = scope.notificationActions
		} else {
			actions = append(actions, scope.notificationActions...)
		}
		events = session.AppendNotificationEvents(events, scope.notificationEvents)
	}
	return actions, events, nil
}

// Capture under Engine.mu, prepare independent immutable replacements unlocked,
// then validate exact roots/membership in the existing row/catalog publisher.
// There is no secondary notification commit authority or counter to exhaust.
func (e *Engine) prepareNotificationPublication(sender *session.Session, actions []session.NotificationAction, events []session.Notification) *session.NotificationPublication {
	if len(actions) == 0 && len(events) == 0 {
		return nil
	}
	p := &session.NotificationPublication{}
	own := &payloadOf(sender).notifications
	e.mu.Lock()
	e.notifications.CaptureLocked(p, own)
	e.mu.Unlock()
	p.Prepare(actions, events)
	return p
}
