package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/stdlib"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func preparedMessageBoundaryEffects(t *testing.T) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			db := p.Native
			if b.PG {
				db = p.CopyPostgres
			}
			a, observer := fixture.LockConnections(t, p.Ctx, db)
			sender, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer sender.Close()
			for _, q := range []string{"LISTEN prepared_receiver", "BEGIN"} {
				fixture.LockExec(t, p.Ctx, a, q, 0)
			}
			fixture.LockExec(t, p.Ctx, observer, "LISTEN prepared_outgoing", 0)
			fixture.LockExec(t, p.Ctx, sender, "NOTIFY prepared_receiver, 'released-at-failed-prepare'", 0)
			fixture.LockExec(t, p.Ctx, a, "LISTEN staged_channel", 0)
			fixture.LockExec(t, p.Ctx, a, "NOTIFY prepared_outgoing, 'must-be-discarded'", 0)
			fixture.PreparedReferenceCleanup(t, b, "message_failure")
			fixture.PreparedReferenceError(t, p.Ctx, a, "PREPARE TRANSACTION 'message_failure'", "0A000", "cannot PREPARE a transaction that has executed LISTEN, UNLISTEN, or NOTIFY")
			fixture.PreparedReferenceAbsent(t, p, sender, "message_failure")
			preparedMessageReceive(t, p, b, a, "prepared_receiver", "released-at-failed-prepare")
			fixture.LockExec(t, p.Ctx, sender, "NOTIFY staged_channel, 'must-not-subscribe'", 0)
			fixture.LockExec(t, p.Ctx, sender, "NOTIFY prepared_receiver, 'sentinel'", 0)
			fixture.LockExec(t, p.Ctx, sender, "NOTIFY prepared_outgoing, 'sentinel'", 0)
			preparedMessageReceive(t, p, b, a, "prepared_receiver", "sentinel")
			preparedMessageReceive(t, p, b, observer, "prepared_outgoing", "sentinel")
		})
	}
}

func preparedMessageReceive(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, channel, payload string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 2*time.Second)
	defer cancel()
	var pid uint64
	var gotChannel, gotPayload string
	var err error
	if !b.PG {
		var event stuntgres.Notification
		event, err = stuntgres.WaitForNotification(ctx, c)
		pid, gotChannel, gotPayload = event.PID, event.Channel, event.Payload
	} else {
		err = c.Raw(func(raw any) error {
			pg, ok := raw.(*stdlib.Conn)
			if !ok {
				return errors.New("expected pgx notification oracle")
			}
			n, err := pg.Conn().WaitForNotification(ctx)
			if err == nil {
				pid, gotChannel, gotPayload = uint64(n.PID), n.Channel, n.Payload
			}
			return err
		})
	}
	if err != nil || pid == 0 || gotChannel != channel || gotPayload != payload {
		t.Fatalf("prepared-boundary notification = %d %q %q, %v; want positive PID %q %q", pid, gotChannel, gotPayload, err, channel, payload)
	}
}

func TestReferenceSessionMessagesPreparedOriginReuse(t *testing.T) {
	for _, entry := range []string{"empty", "rows"} {
		for _, ending := range []string{"ROLLBACK", "COMMIT"} {
			t.Run(entry+"-"+ending, func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						db := p.Native
						if b.PG {
							db = p.CopyPostgres
						}
						a, finisher := fixture.LockConnections(t, p.Ctx, db)
						observer, sender := fixture.LockConnections(t, p.Ctx, db)
						for _, c := range []*sql.Conn{a, finisher} {
							fixture.LockExec(t, p.Ctx, c, "LISTEN reuse_kept", 0)
							fixture.LockExec(t, p.Ctx, c, "LISTEN reuse_boundary", 0)
						}
						fixture.LockExec(t, p.Ctx, observer, "LISTEN reuse_outgoing", 0)
						fixture.LockExec(t, p.Ctx, sender, "CREATE TABLE reuse_rows(v INT)", 0)
						fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
						if entry == "rows" {
							fixture.LockExec(t, p.Ctx, a, "INSERT INTO reuse_rows VALUES(7)", 1)
						}
						fixture.PreparedReferencePrepare(t, p, b, a, "reuse_old")

						// The detached entry belongs to the old incarnation. None
						// of these new-incarnation effects belongs to its finisher.
						fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
						fixture.LockExec(t, p.Ctx, a, "NOTIFY reuse_outgoing, 'uncommitted'", 0)
						fixture.LockExec(t, p.Ctx, a, "UNLISTEN reuse_kept", 0)
						fixture.LockExec(t, p.Ctx, a, "LISTEN reuse_added", 0)
						fixture.LockExec(t, p.Ctx, finisher, "COMMIT PREPARED 'reuse_old'", 0)
						fixture.PreparedReferenceAbsent(t, p, finisher, "reuse_old")
						if entry == "rows" {
							fixture.PreparedReferenceRows(t, p, b, b.DB, "SELECT v FROM reuse_rows", 7)
						} else {
							fixture.PreparedReferenceRows(t, p, b, b.DB, "SELECT v FROM reuse_rows")
						}

						// A positive later event proves absence of early publication
						// without timing or cancellation assumptions.
						fixture.LockExec(t, p.Ctx, sender, "NOTIFY reuse_outgoing, 'before-origin-end'", 0)
						preparedMessageReceive(t, p, b, observer, "reuse_outgoing", "before-origin-end")
						// B's own committed notifications must still reach B on its
						// original subscriptions; A remains in its new transaction.
						fixture.LockExec(t, p.Ctx, finisher, "NOTIFY reuse_kept, 'finisher-self'", 0)
						fixture.LockExec(t, p.Ctx, finisher, "NOTIFY reuse_boundary, 'finisher-boundary'", 0)
						preparedMessageReceive(t, p, b, finisher, "reuse_kept", "finisher-self")
						preparedMessageReceive(t, p, b, finisher, "reuse_boundary", "finisher-boundary")

						fixture.LockExec(t, p.Ctx, a, ending, 0)
						if ending == "COMMIT" {
							preparedMessageReceive(t, p, b, observer, "reuse_outgoing", "uncommitted")
						}
						fixture.LockExec(t, p.Ctx, sender, "NOTIFY reuse_outgoing, 'after-origin-end'", 0)
						preparedMessageReceive(t, p, b, observer, "reuse_outgoing", "after-origin-end")

						// Pending peer events are filtered by A's own committed
						// subscription outcome, never installed on B instead.
						if ending == "ROLLBACK" {
							preparedMessageReceive(t, p, b, a, "reuse_kept", "finisher-self")
						}
						preparedMessageReceive(t, p, b, a, "reuse_boundary", "finisher-boundary")
						fixture.LockExec(t, p.Ctx, sender, "NOTIFY reuse_added, 'added-probe'", 0)
						fixture.LockExec(t, p.Ctx, sender, "NOTIFY reuse_kept, 'kept-probe'", 0)
						fixture.LockExec(t, p.Ctx, sender, "NOTIFY reuse_boundary, 'subscription-sentinel'", 0)
						// B must neither gain added nor lose kept.
						preparedMessageReceive(t, p, b, finisher, "reuse_kept", "kept-probe")
						preparedMessageReceive(t, p, b, finisher, "reuse_boundary", "subscription-sentinel")
						if ending == "COMMIT" {
							preparedMessageReceive(t, p, b, a, "reuse_added", "added-probe")
						} else {
							preparedMessageReceive(t, p, b, a, "reuse_kept", "kept-probe")
						}
						preparedMessageReceive(t, p, b, a, "reuse_boundary", "subscription-sentinel")
					})
				}
			})
		}
	}
}
