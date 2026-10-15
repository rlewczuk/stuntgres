package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceViewLocksMetadata(t *testing.T) {
	viewLocksScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c, _ := blockingConn(t, p, b)
		q := viewLocksQuery("false")
		viewLocksEmpty(t, p, b, c, "false")
		s, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		x := fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
		viewLocksEmpty(t, p, b, x, "false")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT relkind,relnatts FROM pg_class WHERE relnamespace=11 AND relname='pg_locks'", []string{"relkind", "relnatts"}, []string{"CHAR", "INT2"}, [][]any{{"v", int64(16)}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT reltype='pg_catalog.pg_locks'::regtype::oid AS row_link FROM pg_class WHERE relnamespace=11 AND relname='pg_locks'", []string{"row_link"}, []string{"BOOL"}, [][]any{{true}}))
		if b.PG {
			// PostgreSQL supplies the additional pg_type SQL source. Native
			// reciprocal row/array ownership is checked through catalog provider
			// APIs by TestPGLocksProjectionModes, without implementing pg_type here.
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT t.typrelid=c.oid AS row_link,a.typelem=t.oid AS array_link FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.relnamespace=11 AND c.relname='pg_locks'", []string{"row_link", "array_link"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
		}
		if !b.PG {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT reltype FROM pg_class WHERE relnamespace=11 AND relname='pg_locks'", []string{"reltype"}, []string{"OID"}, [][]any{{int64(12075)}}))
		}
	})
}

func TestReferenceViewLocksRelationModes(t *testing.T) {
	viewLocksScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, aid := blockingConn(t, p, b)
		o, _ := blockingConn(t, p, b)
		namespaceExec(t, p, b, a, "CREATE TABLE view_locks_modes(v INT)")
		oid := viewLocksOID(t, p, o, "SELECT 'view_locks_modes'::regclass::oid")
		db := viewLocksDatabase(t, p, o)
		for _, mode := range []struct{ sql, public string }{
			{"ACCESS SHARE", "AccessShareLock"}, {"ROW SHARE", "RowShareLock"}, {"ROW EXCLUSIVE", "RowExclusiveLock"}, {"SHARE UPDATE EXCLUSIVE", "ShareUpdateExclusiveLock"}, {"SHARE", "ShareLock"}, {"SHARE ROW EXCLUSIVE", "ShareRowExclusiveLock"}, {"EXCLUSIVE", "ExclusiveLock"}, {"ACCESS EXCLUSIVE", "AccessExclusiveLock"},
		} {
			namespaceExec(t, p, b, a, "BEGIN", "LOCK TABLE view_locks_modes IN "+mode.sql+" MODE", "LOCK TABLE view_locks_modes IN "+mode.sql+" MODE")
			r := viewLocksOne(t, viewLocksRead(t, p.Ctx, b, o, "relation=$1 AND pid=$2", oid, aid))
			viewLocksTarget(t, r, "relation", db, oid)
			if !r.Granted || r.Mode != mode.public {
				t.Fatal("mode/reentry", r, mode)
			}
			if r.Owner != viewLocksOwner(t, p, b, o, aid).Owner {
				t.Fatal("relation owner", r)
			}
			namespaceExec(t, p, b, a, "ROLLBACK")
			viewLocksEmpty(t, p, b, o, "relation=$1 AND pid=$2", oid, aid)
		}
		namespaceExec(t, p, b, a, "BEGIN", "LOCK TABLE view_locks_modes IN ACCESS SHARE MODE", "SAVEPOINT modes", "LOCK TABLE view_locks_modes IN ACCESS EXCLUSIVE MODE")
		rows := viewLocksRead(t, p.Ctx, b, o, "relation=$1 AND pid=$2", oid, aid)
		if len(rows) != 2 {
			t.Fatal("conversion lost held mode", rows)
		}
		modes := map[string]bool{}
		for _, r := range rows {
			modes[r.Mode] = r.Granted
			viewLocksTarget(t, r, "relation", db, oid)
		}
		if !modes["AccessShareLock"] || !modes["AccessExclusiveLock"] {
			t.Fatal(modes)
		}
		namespaceExec(t, p, b, a, "ROLLBACK TO modes")
		r := viewLocksOne(t, viewLocksRead(t, p.Ctx, b, o, "relation=$1 AND pid=$2", oid, aid))
		if r.Mode != "AccessShareLock" || !r.Granted {
			t.Fatal("savepoint mode restore", r)
		}
		namespaceExec(t, p, b, a, "ROLLBACK")
	})
}

func TestReferenceViewLocksTransitions(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
			viewLocksScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				a, aid := blockingConn(t, p, b)
				w, wid := blockingConn(t, p, b)
				o, _ := blockingConn(t, p, b)
				namespaceExec(t, p, b, a, "CREATE TABLE view_locks_transition(v INT)")
				oid := viewLocksOID(t, p, o, "SELECT 'view_locks_transition'::regclass::oid")
				db := viewLocksDatabase(t, p, o)
				namespaceExec(t, p, b, w, "BEGIN")
				q := "LOCK TABLE view_locks_transition IN ACCESS EXCLUSIVE MODE"
				var s *sql.Stmt
				if held {
					var err error
					s, err = w.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = s.Close() })
				}
				namespaceExec(t, p, b, a, "BEGIN", q)
				before := time.Now()
				await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
					var r sql.Result
					var err error
					if held {
						r, err = s.ExecContext(ctx)
					} else {
						r, err = w.ExecContext(ctx, q)
					}
					return fixture.RelationOutcome{Result: r, Err: err}
				})
				query := viewLocksQuery("relation=$1")
				observerStmt, err := o.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer observerStmt.Close()
				x := fixture.HeldStatement{Executor: o, Statement: observerStmt, SQL: query}
				rows := viewLocksPoll(t, p, b, x, "relation=$1", func(rows []viewLockRow) bool {
					return len(rows) == 2 && viewLocksHasPending(rows, wid, "AccessExclusiveLock")
				}, oid)
				pending := viewLocksPending(t, rows, wid, "AccessExclusiveLock", before)
				for _, r := range rows {
					viewLocksTarget(t, r, "relation", db, oid)
					if r.Mode != "AccessExclusiveLock" || !r.PID.Valid || r.PID.Int64 != int64(aid) && r.PID.Int64 != int64(wid) || r.PID.Int64 == int64(aid) && !r.Granted {
						t.Fatal("transition rows", rows)
					}
				}
				again := viewLocksPending(t, viewLocksRead(t, p.Ctx, b, x, "relation=$1", oid), wid, "AccessExclusiveLock", before)
				if !again.Wait.Time.Equal(pending.Wait.Time) {
					t.Fatal("observation reset enqueue timestamp")
				}
				saved := append([]viewLockRow(nil), rows...)
				namespaceExec(t, p, b, a, "COMMIT")
				blockingLockDone(t, await)
				r := viewLocksOne(t, viewLocksRead(t, p.Ctx, b, x, "relation=$1", oid))
				if !r.Granted || r.Wait.Valid || r.PID.Int64 != int64(wid) {
					t.Fatal("grant", r)
				}
				if !reflect.DeepEqual(saved, rows) {
					t.Fatal("owned snapshot changed after release")
				}
				namespaceExec(t, p, b, w, "ROLLBACK")
				viewLocksEmpty(t, p, b, x, "relation=$1", oid)
			})
		})
	}
}

func TestReferenceViewLocksPublicVisibility(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"view_locks_reader"})
	viewLocksScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, _ := blockingConn(t, p, b)
		w, wid := blockingConn(t, p, b)
		o, _ := blockingConn(t, p, b)
		reader, _ := blockingConn(t, p, b)
		namespaceExec(t, p, b, a, "CREATE ROLE view_locks_reader", "CREATE TABLE view_locks_private(v INT)")
		oid := viewLocksOID(t, p, o, "SELECT 'view_locks_private'::regclass::oid")
		if b.PG {
			namespaceExec(t, p, b, a, "GRANT USAGE ON SCHEMA public TO view_locks_reader")
		} else {
			// Native schema grants remain unavailable. Transfer schema visibility,
			// not the bootstrap-owned table or its SELECT privilege.
			namespaceExec(t, p, b, a, "ALTER SCHEMA public OWNER TO view_locks_reader")
		}
		namespaceExec(t, p, b, reader, "SET ROLE view_locks_reader")
		fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.ErrorDiagnostic("SELECT * FROM view_locks_private", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table view_locks_private"}))
		namespaceExec(t, p, b, a, "BEGIN", "LOCK TABLE view_locks_private IN ACCESS EXCLUSIVE MODE")
		namespaceExec(t, p, b, w, "BEGIN")
		await := blockingLock(t, p, w, "LOCK TABLE view_locks_private IN ACCESS EXCLUSIVE MODE", false)
		admin := viewLocksPoll(t, p, b, o, "relation=$1", func(rows []viewLockRow) bool {
			return len(rows) == 2 && viewLocksHasPending(rows, wid, "AccessExclusiveLock")
		}, oid)
		public := viewLocksRead(t, p.Ctx, b, reader, "relation=$1", oid)
		// Bag comparison, including full identities and enqueue timestamps.
		if len(public) != len(admin) {
			t.Fatal("PUBLIC redaction", public, admin)
		}
		for _, want := range admin {
			if !slicesContainsLock(public, want) {
				t.Fatal("PUBLIC row/detail filtering", public, admin)
			}
		}
		namespaceExec(t, p, b, a, "ROLLBACK")
		blockingLockDone(t, await)
		namespaceExec(t, p, b, w, "ROLLBACK")
		namespaceExec(t, p, b, reader, "RESET ROLE")
	})
}

func slicesContainsLock(rows []viewLockRow, want viewLockRow) bool {
	for _, row := range rows {
		if reflect.DeepEqual(row, want) {
			return true
		}
	}
	return false
}
