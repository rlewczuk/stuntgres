package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"runtime"
	"stuntgres/reference/fixture"
	"testing"
)

func identityDisconnect(t *testing.T, p *fixture.Pair, b fixture.Backend, a *sql.Conn) {
	t.Helper()
	pid := 0
	if b.PG {
		pid = fixture.LockPID(t, p.Ctx, a)
	}
	if err := a.Raw(func(any) error { return driver.ErrBadConn }); err != driver.ErrBadConn {
		t.Fatalf("physical disconnect: %v", err)
	}
	if b.PG {
		// Observe transport-owner retirement, not a timing assumption after Close.
		for {
			var alive bool
			if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1)", pid).Scan(&alive); err != nil {
				t.Fatal(err)
			}
			if !alive {
				break
			}
			if p.Ctx.Err() != nil {
				t.Fatal(p.Ctx.Err())
			}
			runtime.Gosched()
		}
	}
}

func identityPreparedOwner(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn) {
	t.Helper()
	origin, _ := fixture.LockConnections(t, p.Ctx, b.DB)
	fixture.LockExec(t, p.Ctx, origin, "BEGIN", 0)
	id := identityID(t, p, origin)
	fixture.PreparedReferencePrepare(t, p, b, origin, "tx_identity_detached")
	identityDisconnect(t, p, b, origin)
	identityStatus(t, p, observer, id, "in progress")
	// Denied completion leaves the original identity live and completable.
	fixture.LockExec(t, p.Ctx, observer, "CREATE ROLE tx_identity_finisher", 0)
	fixture.LockExec(t, p.Ctx, observer, "SET ROLE tx_identity_finisher", 0)
	_, err := observer.ExecContext(p.Ctx, "COMMIT PREPARED 'tx_identity_detached'")
	want := fixture.ControlsReferenceDiagnostic("42501", "permission denied to finish prepared transaction")
	want.Hint = "Must be superuser or the user that prepared the transaction."
	fixture.AssertDiagnostic(t, err, &want)
	fixture.LockExec(t, p.Ctx, observer, "RESET ROLE", 0)
	identityStatus(t, p, observer, id, "in progress")
	fixture.LockExec(t, p.Ctx, observer, "COMMIT PREPARED 'tx_identity_detached'", 0)
	identityStatus(t, p, observer, id, "committed")
	fixture.LockExec(t, p.Ctx, observer, "DROP ROLE tx_identity_finisher", 0)
}

// Native cancellation uses a real admitted relation wait, not PostgreSQL's
// backend-PID/pg_sleep protocol. Both phases abort the same assigned owner.
func identityCancelOwner(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
	t.Helper()
	if b.PG {
		fixture.SessionCancel(t, p.Ctx, a, false)
		return
	}
	fixture.LockExec(t, p.Ctx, observer, "CREATE TABLE tx_identity_cancel(v INT)", 0)
	holder, _ := fixture.LockConnections(t, p.Ctx, b.DB)
	fixture.LockExec(t, p.Ctx, holder, "BEGIN", 0)
	defer fixture.LockExec(t, p.Ctx, holder, "ROLLBACK", 0)
	fixture.LockExec(t, p.Ctx, holder, "LOCK TABLE tx_identity_cancel IN ACCESS EXCLUSIVE MODE", 0)
	const query = "SELECT v FROM tx_identity_cancel"
	fixture.RelationLockActiveCancel(t, p, b, a, query, func(ctx context.Context) fixture.RelationOutcome {
		return fixture.RowQuery(ctx, a, query, false)
	})
}
