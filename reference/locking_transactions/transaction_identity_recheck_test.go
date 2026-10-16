package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// The updater changes the predicate while C is parked on the actual predecessor.
// C must lock the successor before EPQ rejects it, not invent an UPDATE action.
func identityRecheck(t *testing.T, p *fixture.Pair, b fixture.Backend, a, updater, c, observer *sql.Conn) {
	t.Helper()
	fixture.LockExec(t, p.Ctx, a, "CREATE TABLE tx_identity_recheck(id INT PRIMARY KEY,v INT)", 0)
	fixture.LockExec(t, p.Ctx, a, "INSERT INTO tx_identity_recheck VALUES(1,0)", 1)
	fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
	ax := identityLow(t, identityID(t, p, a))
	fixture.LockExec(t, p.Ctx, updater, "BEGIN", 0)
	bx := identityLow(t, identityID(t, p, updater))
	identityLock(t, p, a, "tx_identity_recheck", 1, "KEY SHARE")
	oldBag := []identityMember{{ax, "keysh"}, {bx, "nokeyupd"}}
	old := identityNewMulti(t, p, observer, func() { fixture.LockExec(t, p.Ctx, updater, "UPDATE tx_identity_recheck SET v=1 WHERE id=1", 1) }, oldBag)
	fixture.LockExec(t, p.Ctx, c, "BEGIN", 0)
	cx := identityLow(t, identityID(t, p, c))
	holder, waiter := 0, 0
	if b.PG {
		holder, waiter = fixture.LockPID(t, p.Ctx, updater), fixture.LockPID(t, p.Ctx, c)
	}
	await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
		return fixture.RowQuery(ctx, c, "SELECT id FROM tx_identity_recheck WHERE id=1 AND v=0 FOR NO KEY UPDATE", false)
	})
	inheritanceQueryWait(t, p, b, holder, waiter)
	identityNoMulti(t, p, observer, func() { identityBag(t, p, observer, old, oldBag) })
	identityNewMulti(t, p, observer, func() {
		fixture.LockExec(t, p.Ctx, updater, "COMMIT", 0)
		out := await()
		if out.Err != nil {
			t.Fatal(out.Err)
		}
		if out.Rows == nil {
			t.Fatal("missing EPQ cursor")
		}
		defer out.Rows.Close()
		if out.Rows.Next() || out.Rows.Err() != nil {
			t.Fatalf("lost predicate returned row: %v", out.Rows.Err())
		}
	}, []identityMember{{ax, "keysh"}, {cx, "fornokeyupd"}})
	identityBag(t, p, observer, old, oldBag)
	fixture.LockExec(t, p.Ctx, c, "ROLLBACK", 0)
	fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
}
