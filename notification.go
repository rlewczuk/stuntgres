package stuntgres

import (
	"context"
	"database/sql"
	"errors"
	"runtime"
)

// Notification is a committed event from a physical logical session in the same
// engine. PID is its full nonzero logical session identity, not an OS process ID.
type Notification struct {
	PID     uint64
	Channel string
	Payload string
}

// WaitForNotification consumes the next client-ready event on a pinned native
// connection. It does not start or end a transaction. Cancellation leaves the
// physical session, subscriptions and unconsumed queue reusable. Foreign and
// wrapped drivers are not supported.
//
// The wait holds database/sql's Raw lease: SQL, Raw and Close on the same
// *sql.Conn cannot interrupt it. Cancel ctx and await return before closing the
// borrower. Other physical sessions can execute and publish while this waits.
func WaitForNotification(ctx context.Context, connection *sql.Conn) (Notification, error) {
	if connection == nil {
		return Notification{}, errors.New("stuntgres: nil connection")
	}
	var event Notification
	err := connection.Raw(func(raw any) error {
		native, ok := raw.(*conn)
		if !ok {
			return errors.New("stuntgres: notifications require a native stuntgres connection, not a foreign or wrapped driver")
		}
		defer runtime.KeepAlive(native)
		if err := native.check(ctx); err != nil {
			return err
		}
		op, err := native.session.AcquireActivity(ctx)
		if err != nil {
			return native.terminalError(err)
		}
		defer op.Done()
		received, err := native.session.WaitForNotification(op)
		if err != nil {
			return native.terminalError(op.CommandError(err))
		}
		event = Notification{PID: received.PID, Channel: received.Channel, Payload: received.Payload}
		return nil
	})
	if err != nil {
		return Notification{}, err
	}
	return event, nil
}
