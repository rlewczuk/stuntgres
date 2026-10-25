package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

// notificationEffects binds the existing sole publisher's transaction effects.
func notificationEffects(op *session.Operation, ref ids.TxRef, phase *session.PreparedPublication, scope *StatementScope) ([]session.NotificationAction, []session.Notification, error) {
	var statement *session.NotificationStatement
	if scope != nil {
		statement = &session.NotificationStatement{Op: scope.Op, Owner: scope.Owner, Actions: scope.notificationActions, Events: scope.notificationEvents}
	}
	return session.NotificationEffects(op, ref, phase != nil && !phase.Preparing, statement)
}

type notificationTargetPublication struct {
	name        string
	engine      *Engine
	publication session.NotificationPublication
}

// notificationPublication captures all cluster roots under the shared mutex.
// Preparation is unlocked; installation is pointer-only at the existing fence.
type notificationPublication struct {
	head    *session.NotificationQueuePosition
	next    *session.NotificationQueuePosition
	targets []notificationTargetPublication
	err     error
}

func (e *Engine) prepareNotificationPublication(sender *session.Session, actions []session.NotificationAction, events []session.Notification) *notificationPublication {
	if len(actions) == 0 && len(events) == 0 {
		return nil
	}
	p := &notificationPublication{}
	e.Mu.Lock()
	p.head = e.Cluster.notificationHead
	p.targets = make([]notificationTargetPublication, 0, len(e.Cluster.Targets))
	for name, runtime := range e.Cluster.Targets {
		p.targets = append(p.targets, notificationTargetPublication{name: name, engine: runtime})
		target := &p.targets[len(p.targets)-1]
		var own *session.NotificationListener
		if runtime == e {
			own = &PayloadOf(sender).notifications
		}
		runtime.notifications.CaptureLocked(&target.publication, own)
	}
	e.Mu.Unlock()
	var start session.NotificationQueuePosition
	if p.head != nil {
		start = *p.head
	}
	head := start.Advance(events)
	if len(events) != 0 {
		p.next = new(session.NotificationQueuePosition)
		*p.next = head
	}
	tail := head
	for i := range p.targets {
		target := &p.targets[i]
		var localActions []session.NotificationAction
		if target.engine == e {
			localActions = actions
		}
		target.publication.Prepare(localActions, events, start, target.engine == e)
		tail = target.publication.OldestPrepared(tail)
	}
	if head.Page-tail.Page >= session.NotificationQueuePages {
		p.err = &sqlstate.Error{Code: "54000", Message: "too many notifications in the NOTIFY queue"}
	}
	return p
}

func (e *Engine) notificationPublicationUnchangedLocked(p *notificationPublication) bool {
	if p == nil {
		return true
	}
	if e.Cluster.notificationHead != p.head || len(e.Cluster.Targets) != len(p.targets) {
		return false
	}
	for i := range p.targets {
		target := &p.targets[i]
		if e.Cluster.Targets[target.name] != target.engine || !target.engine.notifications.UnchangedLocked(&target.publication) {
			return false
		}
	}
	return true
}

func (e *Engine) installNotificationPublicationLocked(p *notificationPublication) {
	if p == nil {
		return
	}
	if p.next != nil {
		e.Cluster.notificationHead = p.next
	}
	for i := range p.targets {
		target := &p.targets[i]
		target.engine.notifications.InstallLocked(&target.publication)
	}
}

func notificationPublicationError(p *notificationPublication) error {
	if p == nil {
		return nil
	}
	return p.err
}

// notificationQueueUsageLocked eagerly accounts idle processing at publication
// and outer boundaries; only registered busy unread cursors retain pages.
func (e *Engine) notificationQueueUsageLocked() float64 {
	if e.Cluster.notificationHead == nil {
		return 0
	}
	head := *e.Cluster.notificationHead
	tail := head
	for _, runtime := range e.Cluster.Targets {
		for _, listener := range runtime.notifications {
			state := listener.State
			if !listener.Ended && state != nil && state.Unread && state.Position.Before(tail) {
				tail = state.Position
			}
		}
	}
	return float64(head.Page-tail.Page) / float64(session.NotificationQueuePages)
}
