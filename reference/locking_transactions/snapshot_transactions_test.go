package reference_test

import (
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

const snapshotCleanupTimeout = 5 * time.Second

func snapshotPeerLifecycle(t *testing.T, p *fixture.Pair, b fixture.Backend, route, completion string) {
	t.Helper()
	a, bumper, observer := snapshotConn(t, p, b), snapshotConn(t, p, b), snapshotConn(t, p, b)
	var held *sql.Stmt
	if route != "direct" {
		var err error
		// Preparing before assignment must neither allocate nor capture a snapshot.
		held, err = observer.PrepareContext(p.Ctx, "SELECT pg_current_snapshot()::text")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		identityUnassigned(t, p, observer)
		_ = snapshotRead(t, p, observer, held)
	}
	fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
	id := identityID(t, p, a)
	completed := snapshotBump(t, p, bumper, id)
	if route == "fresh-prepared" {
		held.Close()
		var err error
		held, err = observer.PrepareContext(p.Ctx, "SELECT pg_current_snapshot()::text")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
	}
	s := snapshotRead(t, p, observer, held)
	snapshotPeerPresent(t, s, id, true)
	snapshotPeerPresent(t, s, completed, false)
	snapshotVisible(t, p, b, observer, id, s, false)
	fixture.LockExec(t, p.Ctx, a, completion, 0)
	snapshotVisible(t, p, b, observer, id, s, false)
	fresh := snapshotRead(t, p, observer, held)
	snapshotPeerPresent(t, fresh, id, false)
	if snapshotNumber(t, id) >= snapshotParse(t, fresh).max {
		t.Fatalf("completed peer not below fresh xmax: %s / %s", id, fresh)
	}
	snapshotVisible(t, p, b, observer, id, fresh, true)
}

func TestReferenceSnapshotCurrent(t *testing.T) {
	for _, route := range []string{"direct", "fresh-prepared", "held"} {
		for _, completion := range []string{"COMMIT", "ROLLBACK"} {
			t.Run(route+"-"+completion, func(t *testing.T) {
				snapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					snapshotPeerLifecycle(t, p, b, route, completion)
				})
			})
		}
	}
	t.Run("latest-completed-not-last-assigned", func(t *testing.T) {
		snapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			a, o := snapshotConn(t, p, b), snapshotConn(t, p, b)
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			id := identityID(t, p, a)
			s := snapshotRead(t, p, o, nil)
			v := snapshotParse(t, s)
			if v.max > snapshotNumber(t, id) || v.contains(snapshotNumber(t, id)) {
				t.Fatalf("newest active xid must be outside latest-completed bound: %s / %s", id, s)
			}
			snapshotVisible(t, p, b, o, id, s, false)
			fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
		})
	})
	t.Run("self-no-allocation", func(t *testing.T) {
		snapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			a, bumper := snapshotConn(t, p, b), snapshotConn(t, p, b)
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			identityUnassigned(t, p, a)
			_ = snapshotRead(t, p, a, nil)
			identityUnassigned(t, p, a)
			id := identityID(t, p, a)
			snapshotBump(t, p, bumper, id)
			s := snapshotRead(t, p, a, nil)
			snapshotPeerPresent(t, s, id, false)
			v, n := snapshotParse(t, s), snapshotNumber(t, id)
			if v.min > n || n >= v.max {
				t.Fatalf("self bounds %s / %s", id, s)
			}
			snapshotVisible(t, p, b, a, id, s, true)
			fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
		})
	})
	t.Run("multiple-active", func(t *testing.T) {
		snapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			a, c, unassigned, bumper, o := snapshotConn(t, p, b), snapshotConn(t, p, b), snapshotConn(t, p, b), snapshotConn(t, p, b), snapshotConn(t, p, b)
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			first := identityID(t, p, a)
			fixture.LockExec(t, p.Ctx, c, "BEGIN", 0)
			second := identityID(t, p, c)
			fixture.LockExec(t, p.Ctx, unassigned, "BEGIN", 0)
			identityUnassigned(t, p, unassigned)
			done := snapshotBump(t, p, bumper, second)
			s := snapshotRead(t, p, o, nil)
			snapshotPeerPresent(t, s, first, true)
			snapshotPeerPresent(t, s, second, true)
			snapshotPeerPresent(t, s, done, false)
			want := [][]any{{first}, {second}}
			snapshotBoth(t, p, b, o, fixture.Select("SELECT x::text AS x FROM pg_snapshot_xip($1::pg_snapshot) AS s(x) ORDER BY s.x", []string{"x"}, []string{"TEXT"}, want, s))
			identityUnassigned(t, p, unassigned)
			fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
			fixture.LockExec(t, p.Ctx, c, "ROLLBACK", 0)
			fixture.LockExec(t, p.Ctx, unassigned, "ROLLBACK", 0)
		})
	})
	for _, completion := range []string{"COMMIT", "ROLLBACK"} {
		t.Run("detached-"+completion, func(t *testing.T) {
			snapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				const gid = "snapshot_detached"
				fixture.PreparedReferenceCleanup(t, b, gid)
				a, bumper, o := snapshotConn(t, p, b), snapshotConn(t, p, b), snapshotConn(t, p, b)
				fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
				id := identityID(t, p, a)
				fixture.PreparedReferencePrepare(t, p, b, a, gid)
				snapshotBump(t, p, bumper, id)
				s := snapshotRead(t, p, o, nil)
				snapshotPeerPresent(t, s, id, true)
				snapshotVisible(t, p, b, o, id, s, false)
				fixture.LockExec(t, p.Ctx, o, completion+" PREPARED '"+gid+"'", 0)
				snapshotVisible(t, p, b, o, id, s, false)
				fresh := snapshotRead(t, p, o, nil)
				snapshotPeerPresent(t, fresh, id, false)
				snapshotVisible(t, p, b, o, id, fresh, true)
			})
		})
	}
}

func TestReferenceSnapshotIsolation(t *testing.T) {
	for _, isolation := range []string{"READ COMMITTED", "REPEATABLE READ", "SERIALIZABLE"} {
		for _, prepared := range []bool{false, true} {
			name := isolation
			if prepared {
				name += "-held"
			}
			t.Run(name, func(t *testing.T) {
				snapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					a, bumper, o := snapshotConn(t, p, b), snapshotConn(t, p, b), snapshotConn(t, p, b)
					var held *sql.Stmt
					if prepared {
						var err error
						held, err = o.PrepareContext(p.Ctx, "SELECT pg_current_snapshot()::text")
						if err != nil {
							t.Fatal(err)
						}
						defer held.Close()
					}
					fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
					id := identityID(t, p, a)
					snapshotBump(t, p, bumper, id)
					fixture.LockExec(t, p.Ctx, o, "BEGIN ISOLATION LEVEL "+isolation, 0)
					s := snapshotRead(t, p, o, held)
					snapshotPeerPresent(t, s, id, true)
					fixture.LockExec(t, p.Ctx, o, "SAVEPOINT retained", 0)
					fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
					fixture.LockExec(t, p.Ctx, o, "ROLLBACK TO SAVEPOINT retained", 0)
					after := snapshotRead(t, p, o, held)
					if isolation == "READ COMMITTED" {
						snapshotPeerPresent(t, after, id, false)
						snapshotVisible(t, p, b, o, id, after, true)
					} else {
						if after != s {
							t.Fatalf("stable isolation replaced snapshot %s with %s", s, after)
						}
						snapshotVisible(t, p, b, o, id, after, false)
					}
					snapshotVisible(t, p, b, o, id, s, false)
					fixture.LockExec(t, p.Ctx, o, "COMMIT", 0)
					fresh := snapshotRead(t, p, o, held)
					snapshotPeerPresent(t, fresh, id, false)
					snapshotVisible(t, p, b, o, id, fresh, true)
				})
			})
		}
	}
}

func TestReferenceSnapshotFeatureSmoke(t *testing.T) {
	snapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		snapshotBoth(t, p, b, b.DB, fixture.Select("SELECT '12:16:14,14'::pg_snapshot::text AS s,pg_snapshot_xmin('12:16:14,14')::text AS lo,pg_snapshot_xmax('12:16:14,14')::text AS hi,pg_visible_in_snapshot('13','12:16:14,14') AS a,pg_visible_in_snapshot('14','12:16:14,14') AS b,pg_visible_in_snapshot('16','12:16:14,14') AS c", []string{"s", "lo", "hi", "a", "b", "c"}, []string{"TEXT", "TEXT", "TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{"12:16:14", "12", "16", true, false, false}}))
		snapshotBoth(t, p, b, b.DB, snapshotTypedStep(b, "SELECT x FROM pg_snapshot_xip('12:16:14,14') AS s(x) ORDER BY x", []string{"x"}, []string{"XID8"}, [][]any{{"14"}}))
		snapshotBoth(t, p, b, b.DB, snapshotTypedStep(b, "SELECT x FROM pg_snapshot_xip(NULL::pg_snapshot) AS s(x)", []string{"x"}, []string{"XID8"}, nil))
		snapshotPeerLifecycle(t, p, b, "held", "COMMIT")
	})
}
