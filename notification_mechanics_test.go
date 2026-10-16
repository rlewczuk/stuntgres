package stuntgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A real native physical Close cancels an admitted activity before waiting for
// its lease. This is intentionally not concurrent sql.Conn.Close/Raw: those
// database/sql operations serialize and cannot interrupt one another.
func TestNotificationPhysicalCloseWake(t *testing.T) {
	native := adapterMechanicsConn(t, NewDriver(), "notification-physical-close")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	op, err := native.session.AcquireActivity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// op is already registered and leased, regardless of goroutine scheduling.
	received := make(chan error, 1)
	go func() {
		_, err := native.session.WaitForNotification(op)
		op.Done()
		received <- err
	}()
	closed := make(chan error, 1)
	go func() { closed <- native.Close() }()
	select {
	case err := <-received:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("physical close receiver outcome", err)
		}
	case <-ctx.Done():
		t.Fatal("physical close did not cancel receive before drain")
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("physical close failed to drain receiver lease")
	}
	if native.session.IsLive() {
		t.Fatal("physical owner remained live")
	}
}
