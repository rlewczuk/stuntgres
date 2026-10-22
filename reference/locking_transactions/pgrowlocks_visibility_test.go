package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgrowlocksVisibilityAndVersions(t *testing.T) {
	t.Run("repeatable-read-ended-updater", func(t *testing.T) {
		pgrowScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
			pgrowTable(t, p, a)
			updater, rc := fixture.LockConnections(t, p.Ctx, b.DB)
			fixture.LockExec(t, p.Ctx, observer, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
			identityBoth(t, p, b, observer, fixture.Select("SELECT v FROM pgrow_t", []string{"v"}, []string{"INT4"}, [][]any{{int64(0)}}))
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			ax := pgrowOwner(t, p, a, "For Key Share")
			identityLock(t, p, a, "pgrow_t", 1, "KEY SHARE")
			old := pgrowOne(t, pgrowGet(t, p, observer), false, ax)
			fixture.LockExec(t, p.Ctx, updater, "BEGIN", 0)
			ux := pgrowOwner(t, p, updater, "No Key Update")
			fixture.LockExec(t, p.Ctx, updater, "UPDATE pgrow_t SET v=1 WHERE id=1", 1)
			mixed := pgrowOne(t, pgrowGet(t, p, observer), true, ax, ux)
			pgrowSameTID(t, old, mixed)
			pgrowSameTID(t, old, pgrowOne(t, pgrowGet(t, p, rc), true, ax, ux))
			// Caller cannot report its own modified predecessor. The visible own
			// successor instead inherits the ORIGINAL singleton key-share header.
			own := pgrowOne(t, pgrowGet(t, p, updater), false, ax)
			if own.tid == old.tid {
				t.Fatal("own successor substituted predecessor")
			}
			fixture.LockExec(t, p.Ctx, updater, "COMMIT", 0)
			ux.pid = 0
			ended := pgrowOne(t, pgrowGet(t, p, observer), true, ax, ux)
			pgrowSameTID(t, old, ended)
			if ended.locker != mixed.locker {
				t.Fatal("RR changed raw mixed identity", ended, mixed)
			}
			current := pgrowOne(t, pgrowGet(t, p, rc), false, ax)
			pgrowSameTID(t, own, current)
			if current.tid == old.tid {
				t.Fatal("RC selected RR predecessor")
			}
			identityBoth(t, p, b, observer, fixture.Select("SELECT v FROM pgrow_t", []string{"v"}, []string{"INT4"}, [][]any{{int64(0)}}))
			identityBoth(t, p, b, rc, fixture.Select("SELECT v FROM pgrow_t", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
			fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
			pgrowEmpty(t, p, observer)
			pgrowEmpty(t, p, rc)
			fixture.LockExec(t, p.Ctx, observer, "ROLLBACK", 0)
		})
	})
	t.Run("own-header-and-rollback", func(t *testing.T) {
		pgrowScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
			pgrowTable(t, p, a)
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			ax := pgrowOwner(t, p, a, "For Update")
			identityLock(t, p, a, "pgrow_t", 1, "UPDATE")
			old := pgrowOne(t, pgrowGet(t, p, observer), false, ax)
			fixture.LockExec(t, p.Ctx, a, "UPDATE pgrow_t SET v=1 WHERE id=1", 1)
			// heap_update inherits singleton xmax as KEYSHR|LOCK_ONLY even
			// when that original singleton was the caller's FOR UPDATE.
			inherited := ax
			inherited.mode = "For Key Share"
			own := pgrowOne(t, pgrowGet(t, p, a), false, inherited)
			if own.tid == old.tid {
				t.Fatal("own successor has predecessor TID")
			}
			// The predecessor retains the stronger FOR UPDATE header when
			// the same owner performs a non-key update.
			ax.mode = "Update"
			pgrowSameTID(t, old, pgrowOne(t, pgrowGet(t, p, observer), false, ax))
			fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
			pgrowEmpty(t, p, observer)
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			ax = pgrowOwner(t, p, a, "For Update")
			identityLock(t, p, a, "pgrow_t", 1, "UPDATE")
			pgrowSameTID(t, old, pgrowOne(t, pgrowGet(t, p, observer), false, ax))
			fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			fixture.LockExec(t, p.Ctx, a, "INSERT INTO pgrow_t VALUES(2,2)", 1)
			fixture.LockExec(t, p.Ctx, a, "UPDATE pgrow_t SET v=3 WHERE id=2", 1)
			pgrowEmpty(t, p, a)
			pgrowEmpty(t, p, observer)
			fixture.LockExec(t, p.Ctx, a, "DELETE FROM pgrow_t WHERE id=2", 1)
			pgrowEmpty(t, p, a)
			fixture.LockExec(t, p.Ctx, a, "DELETE FROM pgrow_t WHERE id=1", 1)
			ax = pgrowOwner(t, p, a, "Update")
			pgrowOne(t, pgrowGet(t, p, observer), false, ax)
			pgrowEmpty(t, p, a)
			fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
		})
	})
	t.Run("raw-multi-inherited-successor", func(t *testing.T) {
		pgrowScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
			pgrowTable(t, p, a)
			other, updater := fixture.LockConnections(t, p.Ctx, b.DB)
			for _, x := range []*sql.Conn{a, other, updater} {
				fixture.LockExec(t, p.Ctx, x, "BEGIN", 0)
			}
			ax, bx := pgrowOwner(t, p, a, "For Key Share"), pgrowOwner(t, p, other, "For Key Share")
			identityLock(t, p, a, "pgrow_t", 1, "KEY SHARE")
			identityLock(t, p, other, "pgrow_t", 1, "KEY SHARE")
			raw := pgrowOne(t, pgrowGet(t, p, observer), true, ax, bx)
			ux := pgrowOwner(t, p, updater, "No Key Update")
			fixture.LockExec(t, p.Ctx, updater, "UPDATE pgrow_t SET v=1 WHERE id=1", 1)
			pgrowSameTID(t, raw, pgrowOne(t, pgrowGet(t, p, observer), true, ax, bx, ux))
			own := pgrowOne(t, pgrowGet(t, p, updater), true, ax, bx)
			if own.tid == raw.tid || own.locker != raw.locker {
				t.Fatal("successor did not inherit original raw bag", raw, own)
			}
			fixture.LockExec(t, p.Ctx, updater, "ROLLBACK", 0)
			pgrowSameTID(t, raw, pgrowOne(t, pgrowGet(t, p, observer), true, ax, bx, pgrowMember{ux.xid, ux.mode, 0}))
			fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
			fixture.LockExec(t, p.Ctx, other, "ROLLBACK", 0)
			pgrowEmpty(t, p, observer)
		})
	})
}
