package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

// notificationEffects binds a publication's prepared phase and statement
// scope to session.NotificationEffects.
func notificationEffects(op *session.Operation, ref ids.TxRef, phase *preparedPublication, scope *StatementScope) ([]session.NotificationAction, []session.Notification, error) {
	var statement *session.NotificationStatement
	if scope != nil {
		statement = &session.NotificationStatement{Op: scope.op, Owner: scope.owner, Actions: scope.notificationActions, Events: scope.notificationEvents}
	}
	return session.NotificationEffects(op, ref, phase != nil && !phase.preparing, statement)
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
