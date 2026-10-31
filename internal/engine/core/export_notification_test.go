package core

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

// NotificationEffects calls notificationEffects.
func NotificationEffects(op *session.Operation, ref ids.TxRef, phase *session.PreparedPublication, scope *StatementScope) ([]session.NotificationAction, []session.Notification, error) {
	return notificationEffects(op, ref, phase, scope)
}

// NotificationSessionEnd names notificationSessionEnd.
type NotificationSessionEnd = notificationSessionEnd

// EngineNotificationPublicationUnchangedLocked calls x.notificationPublicationUnchangedLocked.
func EngineNotificationPublicationUnchangedLocked(x *Engine, p *notificationPublication) bool {
	return x.notificationPublicationUnchangedLocked(p)
}

// EnginePrepareNotificationPublication calls x.prepareNotificationPublication.
func EnginePrepareNotificationPublication(x *Engine, sender *session.Session, actions []session.NotificationAction, events []session.Notification) *notificationPublication {
	return x.prepareNotificationPublication(sender, actions, events)
}

// FinishNotificationBoundary calls finishNotificationBoundary.
func FinishNotificationBoundary(t *session.TransactionOwner) {
	finishNotificationBoundary(t)
}

// NotificationPublicationError calls notificationPublicationError.
func NotificationPublicationError(p *notificationPublication) error {
	return notificationPublicationError(p)
}

// EngineInstallNotificationPublicationLocked calls x.installNotificationPublicationLocked.
func EngineInstallNotificationPublicationLocked(x *Engine, p *notificationPublication) {
	x.installNotificationPublicationLocked(p)
}

// EngineNotificationQueueUsageLocked calls x.notificationQueueUsageLocked.
func EngineNotificationQueueUsageLocked(x *Engine) float64 {
	return x.notificationQueueUsageLocked()
}

// EnginePollNotification calls x.pollNotification.
func EnginePollNotification(x *Engine, s *session.Session, check func() error) (session.Notification, bool, <-chan struct{}, error) {
	return x.pollNotification(s, check)
}
