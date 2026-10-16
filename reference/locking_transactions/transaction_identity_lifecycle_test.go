package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionIdentityStatus(t *testing.T) {
	identityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
		for _, end := range []string{"COMMIT", "ROLLBACK"} {
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			id := identityID(t, p, a)
			identityStatus(t, p, observer, id, "in progress")
			fixture.LockExec(t, p.Ctx, a, end, 0)
			want := "committed"
			if end == "ROLLBACK" {
				want = "aborted"
			}
			identityStatus(t, p, observer, id, want)
		}
		for _, id := range []string{"0", "4294967296", "18446744069414584320"} {
			identityBoth(t, p, b, observer, fixture.Select("SELECT pg_xact_status($1::xid8) AS s", []string{"s"}, []string{"TEXT"}, [][]any{{nil}}, id))
		}
		for _, id := range []string{"1", "2", "4294967297", "4294967298", "18446744069414584321", "18446744069414584322"} {
			identityStatus(t, p, observer, id, "committed")
		}
		id := identityID(t, p, a)
		identityStatus(t, p, observer, id, "committed")
	})
}

func TestReferenceTransactionIdentityLifecycle(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"tx_identity_finisher"})
	identityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
		for _, commit := range []bool{false, true} {
			tx, err := a.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			id := identityID(t, p, tx)
			identityStatus(t, p, observer, id, "in progress")
			if commit {
				err = tx.Commit()
			} else {
				err = tx.Rollback()
			}
			if err != nil {
				t.Fatal(err)
			}
			want := "aborted"
			if commit {
				want = "committed"
			}
			identityStatus(t, p, observer, id, want)
		}
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		id := identityID(t, p, a)
		fixture.LockExec(t, p.Ctx, a, "SAVEPOINT recover", 0)
		_, err := a.ExecContext(p.Ctx, "SELECT 1/0")
		fixture.State(t, err, "22012")
		fixture.LockExec(t, p.Ctx, a, "ROLLBACK TO recover", 0)
		if identityID(t, p, a) != id {
			t.Fatal("savepoint recovery changed top identity")
		}
		identityStatus(t, p, observer, id, "in progress")
		fixture.LockExec(t, p.Ctx, a, "COMMIT AND CHAIN", 0)
		identityStatus(t, p, observer, id, "committed")
		identityUnassigned(t, p, a)
		id = identityID(t, p, a)
		fixture.LockExec(t, p.Ctx, a, "ROLLBACK AND CHAIN", 0)
		identityStatus(t, p, observer, id, "aborted")
		identityUnassigned(t, p, a)
		id = identityID(t, p, a)
		_, err = a.ExecContext(p.Ctx, "SELECT 1/0")
		fixture.State(t, err, "22012")
		identityStatus(t, p, observer, id, "aborted")
		fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
		identityStatus(t, p, observer, id, "aborted")
		// An implicit multi-statement message owns a genuine transaction until its end.
		rows, err := a.QueryContext(p.Ctx, "SELECT pg_current_xact_id()::text; SELECT pg_current_xact_id()::text")
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for {
			for rows.Next() {
				var v string
				if err := rows.Scan(&v); err != nil {
					t.Fatal(err)
				}
				ids = append(ids, v)
			}
			if !rows.NextResultSet() {
				break
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil || len(ids) != 2 || ids[0] != ids[1] {
			t.Fatalf("implicit message ids %v %v", ids, err)
		}
		identityStatus(t, p, observer, ids[0], "committed")
		for _, finish := range []string{"COMMIT", "ROLLBACK"} {
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			id = identityID(t, p, a)
			gid := "tx_identity_" + strings.ToLower(finish)
			fixture.PreparedReferencePrepare(t, p, b, a, gid)
			identityStatus(t, p, observer, id, "in progress")
			_, err = observer.ExecContext(p.Ctx, "COMMIT PREPARED 'tx_identity_missing'")
			fixture.State(t, err, "42704")
			identityStatus(t, p, observer, id, "in progress")
			fixture.LockExec(t, p.Ctx, observer, finish+" PREPARED '"+gid+"'", 0)
			want := "committed"
			if finish == "ROLLBACK" {
				want = "aborted"
			}
			identityStatus(t, p, observer, id, want)
		}
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		fixture.PreparedReferencePrepare(t, p, b, a, "tx_identity_empty")
		entry := fixture.PreparedReferenceOne(t, p, observer, "tx_identity_empty")
		if entry.Xid < 3 {
			t.Fatal("empty PREPARE did not assign")
		}
		fixture.LockExec(t, p.Ctx, observer, "ROLLBACK PREPARED 'tx_identity_empty'", 0)
		identityPreparedOwner(t, p, b, observer)
		// Physical close, not returning a pooled handle, aborts a genuine owner.
		disconnected, _ := fixture.LockConnections(t, p.Ctx, b.DB)
		fixture.LockExec(t, p.Ctx, disconnected, "BEGIN", 0)
		id = identityID(t, p, disconnected)
		identityDisconnect(t, p, b, disconnected)
		identityStatus(t, p, observer, id, "aborted")
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		id = identityID(t, p, a)
		identityCancelOwner(t, p, b, a, observer)
		identityStatus(t, p, observer, id, "aborted")
		fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
		// Native replacement retires actual retained history; no fake PostgreSQL VACUUM.
		if !b.PG {
			id = identityID(t, p, a)
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE broken(")); err == nil {
				t.Fatal("malformed restore succeeded")
			}
			identityStatus(t, p, observer, id, "committed")
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			identityBoth(t, p, b, observer, fixture.Select("SELECT pg_xact_status($1::xid8) AS s", []string{"s"}, []string{"TEXT"}, [][]any{{nil}}, id))
		}
	})
}

// Keep admission cancellation and member publication in the same shared schedule.
func identityQueue(t *testing.T, p *fixture.Pair, b fixture.Backend, a, other, third, observer *sql.Conn) {
	t.Helper()
	fixture.LockExec(t, p.Ctx, a, "CREATE TABLE tx_identity_queue(id INT PRIMARY KEY,v INT)", 0)
	fixture.LockExec(t, p.Ctx, a, "INSERT INTO tx_identity_queue VALUES(1,0),(2,0)", 2)
	fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
	ax := identityLow(t, identityID(t, p, a))
	fixture.LockExec(t, p.Ctx, other, "BEGIN", 0)
	bx := identityLow(t, identityID(t, p, other))
	identityLock(t, p, a, "tx_identity_queue", 1, "KEY SHARE")
	oldBag := []identityMember{{ax, "keysh"}, {bx, "fornokeyupd"}}
	old := identityNewMulti(t, p, observer, func() { identityLock(t, p, other, "tx_identity_queue", 1, "NO KEY UPDATE") }, oldBag)
	for _, cancelWait := range []bool{true, false} {
		fixture.LockExec(t, p.Ctx, third, "BEGIN", 0)
		cx := identityLow(t, identityID(t, p, third))
		holder, waiter := 0, 0
		if b.PG {
			holder, waiter = fixture.LockPID(t, p.Ctx, other), fixture.LockPID(t, p.Ctx, third)
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		await := fixture.LockOperation(t, p.Ctx, func(context.Context) fixture.RelationOutcome {
			return fixture.RowQuery(ctx, third, "SELECT id FROM tx_identity_queue WHERE id=1 FOR NO KEY UPDATE", false)
		})
		inheritanceQueryWait(t, p, b, holder, waiter)
		identityNoMulti(t, p, observer, func() { identityBag(t, p, observer, old, oldBag) })
		if cancelWait {
			if b.PG {
				var ok bool
				if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", waiter).Scan(&ok); err != nil || !ok {
					t.Fatalf("cancel: %v %v", ok, err)
				}
			} else {
				cancel()
			}
			out := await()
			cancel()
			if out.Rows != nil {
				out.Rows.Close()
			}
			if b.PG {
				want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
				want.Context = `while locking tuple (0,1) in relation "tx_identity_queue"`
				fixture.AssertDiagnostic(t, out.Err, &want)
			} else {
				fixture.DriverOrdinaryError(t, out.Err, context.Canceled)
			}
			if identityNextMulti(t, p, observer) != old+1 {
				t.Fatal("canceled waiter created a MultiXact")
			}
		} else {
			identityNewMulti(t, p, observer, func() {
				defer cancel()
				fixture.LockExec(t, p.Ctx, other, "ROLLBACK", 0)
				out := await()
				if out.Err != nil {
					t.Fatal(out.Err)
				}
				if out.Rows == nil {
					t.Fatal("missing granted cursor")
				}
				defer out.Rows.Close()
				if !out.Rows.Next() {
					t.Fatal("missing granted row", out.Rows.Err())
				}
				var id int64
				if err := out.Rows.Scan(&id); err != nil || id != 1 {
					t.Fatalf("granted row: %d %v", id, err)
				}
				if out.Rows.Next() || out.Rows.Err() != nil {
					t.Fatal("unexpected granted rows or cursor error", out.Rows.Err())
				}
			}, []identityMember{{ax, "keysh"}, {cx, "fornokeyupd"}})
		}
		fixture.LockExec(t, p.Ctx, third, "ROLLBACK", 0)
		identityBag(t, p, observer, old, oldBag)
	}
	fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
}
