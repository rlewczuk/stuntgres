package reference_test

import (
	"context"
	"database/sql"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgrowlocksModesAndOwners(t *testing.T) {
	pgrowScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
		pgrowTable(t, p, a)
		fixture.LockExec(t, p.Ctx, observer, "BEGIN READ ONLY", 0)
		identityUnassigned(t, p, observer)
		pgrowEmpty(t, p, observer)
		var original pgrowRecord
		for _, mode := range []string{"KEY SHARE", "SHARE", "NO KEY UPDATE", "UPDATE"} {
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			owner := pgrowOwner(t, p, a, "For "+map[string]string{"KEY SHARE": "Key Share", "SHARE": "Share", "NO KEY UPDATE": "No Key Update", "UPDATE": "Update"}[mode])
			identityLock(t, p, a, "pgrow_t", 1, mode)
			r := pgrowOne(t, pgrowGet(t, p, observer), false, owner)
			pgrowSameTID(t, r, pgrowOne(t, pgrowGet(t, p, a), false, owner))
			if original.tid == "" {
				original = r
			} else {
				pgrowSameTID(t, original, r)
			}
			fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
			pgrowEmpty(t, p, observer)
		}
		for _, tc := range []struct{ q, mode string }{
			{"UPDATE pgrow_t SET v=1 WHERE id=1", "No Key Update"},
			{"UPDATE pgrow_t SET id=2 WHERE id=1", "Update"},
			{"DELETE FROM pgrow_t WHERE id=1", "Update"},
		} {
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			owner := pgrowOwner(t, p, a, tc.mode)
			fixture.LockExec(t, p.Ctx, a, tc.q, 1)
			pgrowSameTID(t, original, pgrowOne(t, pgrowGet(t, p, observer), false, owner))
			pgrowEmpty(t, p, a)
			fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
			pgrowEmpty(t, p, observer)
		}
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		fixture.LockExec(t, p.Ctx, a, "INSERT INTO pgrow_t VALUES(2,9)", 1)
		identityLock(t, p, a, "pgrow_t", 2, "UPDATE")
		pgrowEmpty(t, p, observer)
		owner := pgrowOwner(t, p, a, "For Update")
		pgrowOne(t, pgrowGet(t, p, a), false, owner)
		fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
		identityUnassigned(t, p, observer)
		fixture.LockExec(t, p.Ctx, observer, "COMMIT", 0)
	})
}

func pgrowHandoff(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn, update bool, release string) pgrowRecord {
	t.Helper()
	other, _ := fixture.LockConnections(t, p.Ctx, b.DB)
	fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
	fixture.LockExec(t, p.Ctx, other, "BEGIN", 0)
	ax := pgrowOwner(t, p, a, "For Update")
	mode := "For Update"
	q := "SELECT id FROM pgrow_t WHERE id=1 FOR UPDATE"
	if update {
		mode = "No Key Update"
		q = "UPDATE pgrow_t SET v=1 WHERE id=1"
	}
	bx := pgrowOwner(t, p, other, mode)
	identityLock(t, p, a, "pgrow_t", 1, "UPDATE")
	initial := pgrowOne(t, pgrowGet(t, p, observer), false, ax)
	await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
		if update {
			return fixture.RowExecute(ctx, other, q, true)
		}
		return fixture.RowQuery(ctx, other, q, true)
	})
	pgrowWait(t, p, observer, int(bx.pid), int(ax.pid), false)
	pgrowSameTID(t, initial, pgrowOne(t, pgrowGet(t, p, observer), false, ax))
	fixture.LockExec(t, p.Ctx, a, release, 0)
	out := await()
	if out.Err != nil {
		t.Fatal(out.Err)
	}
	if update {
		fixture.LockAffected(t, out.Result, 1)
	} else {
		if out.Rows == nil {
			t.Fatal("granted waiter missing cursor")
		}
		var id int
		if !out.Rows.Next() {
			t.Fatal("granted waiter missing row", out.Rows.Err())
		}
		if err := out.Rows.Scan(&id); err != nil || id != 1 {
			t.Fatal(id, err)
		}
		if out.Rows.Next() || out.Rows.Err() != nil {
			t.Fatal("extra waiter rows", out.Rows.Err())
		}
		out.Rows.Close()
	}
	if out.Stmt != nil {
		out.Stmt.Close()
	}
	r := pgrowOne(t, pgrowGet(t, p, observer), false, bx)
	pgrowSameTID(t, initial, r)
	fixture.LockExec(t, p.Ctx, other, "ROLLBACK", 0)
	pgrowEmpty(t, p, observer)
	identityBoth(t, p, b, observer, fixture.Select("SELECT v FROM pgrow_t", []string{"v"}, []string{"INT4"}, [][]any{{int64(0)}}))
	return initial
}
func TestReferencePgrowlocksConflictingHandoff(t *testing.T) {
	pgrowScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
		pgrowTable(t, p, a)
		for _, release := range []string{"COMMIT", "ROLLBACK"} {
			for _, update := range []bool{false, true} {
				t.Run(release+strconv.FormatBool(update), func(t *testing.T) { pgrowHandoff(t, p, b, a, observer, update, release) })
			}
		}
	})
}

func TestReferencePgrowlocksRawMultiAndPID(t *testing.T) {
	pgrowScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
		pgrowTable(t, p, a)
		other, _ := fixture.LockConnections(t, p.Ctx, b.DB)
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		fixture.LockExec(t, p.Ctx, other, "BEGIN", 0)
		ax, bx := pgrowOwner(t, p, a, "For Key Share"), pgrowOwner(t, p, other, "For Share")
		identityLock(t, p, a, "pgrow_t", 1, "KEY SHARE")
		single := pgrowOne(t, pgrowGet(t, p, observer), false, ax)
		identityLock(t, p, other, "pgrow_t", 1, "SHARE")
		multi := pgrowOne(t, pgrowGet(t, p, observer), true, ax, bx)
		pgrowSameTID(t, single, multi)
		id, err := strconv.ParseUint(multi.locker, 10, 32)
		if err != nil {
			t.Fatal(err)
		}
		identityBag(t, p, observer, uint32(id), []identityMember{{ax.xid, "keysh"}, {bx.xid, "sh"}})
		fixture.LockExec(t, p.Ctx, other, "ROLLBACK", 0)
		bx.pid = 0
		ended := pgrowOne(t, pgrowGet(t, p, observer), true, ax, bx)
		if ended.locker != multi.locker {
			t.Fatal("raw MultiXact changed after member ended", ended, multi)
		}
		fixture.LockExec(t, p.Ctx, other, "BEGIN", 0)
		newOwner := pgrowOwner(t, p, other, "For Share")
		if newOwner.xid == bx.xid {
			t.Fatal("new transaction reused ended xid")
		}
		pgrowOne(t, pgrowGet(t, p, observer), true, ax, bx)
		fixture.LockExec(t, p.Ctx, other, "ROLLBACK", 0)
		fixture.PreparedReferencePrepare(t, p, b, a, "pgrow_prepared_owner")
		ax.pid = 0
		pgrowOne(t, pgrowGet(t, p, observer), true, ax, bx)
		fixture.LockExec(t, p.Ctx, observer, "ROLLBACK PREPARED 'pgrow_prepared_owner'", 0)
		pgrowEmpty(t, p, observer)
	})
}

func TestReferencePgrowlocksFeatureSmoke(t *testing.T) {
	pgrowScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
		pgrowTable(t, p, a)
		initial := pgrowHandoff(t, p, b, a, observer, true, "ROLLBACK")
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		owner := pgrowOwner(t, p, a, "For Update")
		identityLock(t, p, a, "pgrow_t", 1, "UPDATE")
		pgrowSameTID(t, initial, pgrowOne(t, pgrowGet(t, p, observer), false, owner))
		stmt, err := observer.PrepareContext(p.Ctx, "SELECT * FROM pgrowlocks($1::text)")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		rows, err := stmt.QueryContext(p.Ctx, "pgrow_t")
		if err != nil {
			t.Fatal(err)
		}
		pgrowOne(t, pgrowRead(t, rows), false, owner)
		rows, err = stmt.QueryContext(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := pgrowRead(t, rows); len(got) != 0 {
			t.Fatal("strict NULL", got)
		}
		fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
		other, _ := fixture.LockConnections(t, p.Ctx, b.DB)
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		fixture.LockExec(t, p.Ctx, other, "BEGIN", 0)
		ax, bx := pgrowOwner(t, p, a, "For Key Share"), pgrowOwner(t, p, other, "For Share")
		identityLock(t, p, a, "pgrow_t", 1, "KEY SHARE")
		identityLock(t, p, other, "pgrow_t", 1, "SHARE")
		pgrowOne(t, pgrowGet(t, p, observer), true, ax, bx)
		fixture.LockExec(t, p.Ctx, other, "ROLLBACK", 0)
		bx.pid = 0
		pgrowOne(t, pgrowGet(t, p, observer), true, ax, bx)
		fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
		pgrowEmpty(t, p, observer)
		fixture.LockExec(t, p.Ctx, a, "UPDATE pgrow_t SET v=2 WHERE id=1", 1)
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		owner = pgrowOwner(t, p, a, "For Update")
		identityLock(t, p, a, "pgrow_t", 1, "UPDATE")
		if successor := pgrowOne(t, pgrowGet(t, p, observer), false, owner); successor.tid == initial.tid {
			t.Fatal("committed UPDATE reused predecessor TID")
		}
		fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
	})
}
