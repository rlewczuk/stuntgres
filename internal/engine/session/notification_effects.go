package session

import (
	"errors"

	"stuntgres/internal/engine/ids"
)

// NotificationStatement is a statement's share of a publication's
// notification effects: the statement's operation and transaction owner,
// which must be the publishing ones, and the LISTEN/UNLISTEN actions and
// NOTIFY events it has not merged into the owner yet.
type NotificationStatement struct {
	Op      *Operation
	Owner   *TransactionOwner
	Actions []NotificationAction
	Events  []Notification
}

// NotificationEffects returns the notification actions and events that
// publishing ref installs for op's session: the transaction owner's,
// followed by statement's when statement is non-nil. completing is true when
// the publication completes a prepared transaction, which owns none.
func NotificationEffects(op *Operation, ref ids.TxRef, completing bool, statement *NotificationStatement) ([]NotificationAction, []Notification, error) {
	// PREPARE rejects notification effects. Completion owns only the detached
	// entry: even checking its old attachment would read a reusable live owner.
	if completing {
		return nil, nil, nil
	}
	if op == nil || op.Self != op || op.Session == nil || op.Session.Transactions == nil ||
		TxOwnerOf(ref) != op.Session.Transactions {
		return nil, nil, errors.New("stuntgres: foreign notification transaction")
	}
	// The caller holds this session's transaction admission. Validate against
	// that owner, never against an owner reached through an untrusted old ref.
	owner := op.Session.Transactions
	if err := owner.Validate(ref); err != nil {
		return nil, nil, err
	}
	if statement == nil {
		return owner.NotificationActions, owner.NotificationEvents, nil
	}
	if statement.Op != op || statement.Owner != owner {
		return nil, nil, errors.New("stuntgres: foreign notification statement")
	}
	actions, events := owner.MergeNotifications(statement.Actions, statement.Events)
	return actions, events, nil
}

// MergeNotifications returns t's notification actions followed by actions,
// and t's events followed by those of events whose channel and payload are
// not already among them. It may append into the spare capacity of t's
// slices; savepoint marks keep clipped copies.
func (t *TransactionOwner) MergeNotifications(actions []NotificationAction, events []Notification) ([]NotificationAction, []Notification) {
	merged := t.NotificationActions
	if len(merged) == 0 {
		merged = actions
	} else {
		merged = append(merged, actions...)
	}
	return merged, AppendNotificationEvents(t.NotificationEvents, events)
}
