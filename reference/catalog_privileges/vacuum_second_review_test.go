package reference_test

import (
	"database/sql"
	"reflect"
	"strconv"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceVacuumF4FutureRelationDatabaseHorizon(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
			for _, phase := range []string{"database_only", "empty_relation", "older_birth"} {
				t.Run(phase, func(t *testing.T) {
					vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
						older, creator := vacuumConn(t, p, b), vacuumConn(t, p, b)
						run := func(c *sql.Conn, s fixture.Step) {
							if held {
								vacuumRun(t, p, b, vacuumHeld(t, p, c, s.Query), s)
							} else {
								vacuumRun(t, p, b, c, s)
							}
						}
						run(older, fixture.Exec("BEGIN ISOLATION LEVEL READ COMMITTED", 0))
						defer older.ExecContext(p.Ctx, "ROLLBACK")
						var assigned string
						if err := older.QueryRowContext(p.Ctx, "SELECT pg_current_xact_id()::text").Scan(&assigned); err != nil {
							t.Fatal(err)
						}
						full, err := strconv.ParseUint(assigned, 10, 64)
						if err != nil || full < 3 {
							t.Fatal("not a genuine older assigned publisher", assigned, err)
						}
						low := strconv.FormatUint(uint64(uint32(full)), 10)
						// Crucially there is no target table yet. No later database-only
						// or main-table VACUUM may mask this published database bound.
						run(creator, fixture.Exec("VACUUM (ONLY_DATABASE_STATS)", 0))
						if phase != "database_only" {
							run(creator, fixture.Exec("CREATE TABLE vacuum_future_birth(id INT)", 0))
						}
						if phase == "older_birth" {
							run(older, fixture.Exec("INSERT INTO vacuum_future_birth VALUES(7)", 1))
							var stillAssigned string
							if err := older.QueryRowContext(p.Ctx, "SELECT pg_current_xact_id()::text").Scan(&stillAssigned); err != nil || stillAssigned != assigned {
								t.Fatal("publisher changed", assigned, stillAssigned, err)
							}
							run(older, fixture.Exec("COMMIT", 0))
							vacuumRows(t, p, b, creator, "vacuum_future_birth", 7)
						}
						run(creator, fixture.Select("SELECT age(datfrozenxid)>=age($1::xid) AS safe FROM pg_database WHERE datname=current_database()", []string{"safe"}, []string{"BOOL"}, [][]any{{true}}, low))
						if phase != "database_only" {
							run(creator, fixture.Select("SELECT age(c.relfrozenxid)>=age($1::xid) AS relation_safe,age(d.datfrozenxid)>=age(c.relfrozenxid) AS database_safe FROM pg_class c CROSS JOIN pg_database d WHERE c.oid='vacuum_future_birth'::regclass AND d.datname=current_database()", []string{"relation_safe", "database_safe"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, low))
							vacuumCount(t, p.Ctx, creator, "vacuum_future_birth", 0)
						}
						if phase == "older_birth" {
							run(creator, fixture.Exec("VACUUM (FREEZE) vacuum_future_birth", 0))
							vacuumRows(t, p, b, creator, "vacuum_future_birth", 7)
							vacuumCount(t, p.Ctx, creator, "vacuum_future_birth", 1)
						}
					})
				})
			}
		})
	}
}

func TestReferenceVacuumF5SelectionNoticeOrder(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
			for _, deniedFirst := range []bool{true, false} {
				t.Run(map[bool]string{true: "denied_then_locked", false: "locked_then_denied"}[deniedFirst], func(t *testing.T) {
					fixture.RoleReferenceCleanup(t, []string{"vacuum_notice_actor"})
					vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
						owner, holder := vacuumConn(t, p, b), vacuumConn(t, p, b)
						run := func(s fixture.Step) { vacuumRun(t, p, b, owner, s) }
						run(fixture.Exec("CREATE ROLE vacuum_notice_actor", 0))
						run(fixture.Exec("GRANT USAGE ON SCHEMA public TO vacuum_notice_actor", 0))
						for _, table := range []string{"vacuum_notice_denied", "vacuum_notice_locked"} {
							run(fixture.Exec("CREATE TABLE "+table+"(id INT)", 0))
							run(fixture.Exec("INSERT INTO "+table+" VALUES(1),(2)", 2))
							run(fixture.Exec("DELETE FROM "+table+" WHERE id=1", 1))
							run(fixture.Exec("VACUUM "+table, 0))
							vacuumCount(t, p.Ctx, owner, table, 1)
						}
						run(fixture.Exec("GRANT MAINTAIN ON vacuum_notice_locked TO vacuum_notice_actor", 0))
						type facts struct {
							Count, Live, Dead int64
							Last              time.Time
							Tuples            float64
						}
						read := func(table string) facts {
							var f facts
							if err := owner.QueryRowContext(p.Ctx, "SELECT s.vacuum_count,s.n_live_tup,s.n_dead_tup,s.last_vacuum,c.reltuples FROM pg_stat_all_tables s JOIN pg_class c ON c.oid=s.relid WHERE s.relid=$1::regclass", table).Scan(&f.Count, &f.Live, &f.Dead, &f.Last, &f.Tuples); err != nil {
								t.Fatal(err)
							}
							f.Last = f.Last.UTC() // Compare the exact instant, not per-driver location pointers.
							return f
						}
						deniedBefore, lockedBefore := read("vacuum_notice_denied"), read("vacuum_notice_locked")
						for _, f := range []facts{deniedBefore, lockedBefore} {
							if f.Count != 1 || f.Live != 1 || f.Dead != 0 || f.Tuples != 1 || f.Last.IsZero() {
								t.Fatal("independent real-vacuum baseline", f)
							}
						}
						var notices []fixture.Diagnostic
						_, actor, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
						arun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, actor, rec, s) }
						arun(fixture.Exec("SET ROLE vacuum_notice_actor", 0))
						arun(fixture.Exec("SET lock_timeout='100ms'", 0))
						list := "vacuum_notice_denied,vacuum_notice_locked"
						permission := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: `permission denied to vacuum "vacuum_notice_denied", skipping it`}
						unavailable := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "55P03", Message: `skipping vacuum of "vacuum_notice_locked" --- lock not available`}
						want := []fixture.Diagnostic{permission, unavailable}
						if !deniedFirst {
							list = "vacuum_notice_locked,vacuum_notice_denied"
							want = []fixture.Diagnostic{unavailable, permission}
						}
						q := "VACUUM (SKIP_LOCKED) " + list
						notices = nil
						h := vacuumHeld(t, p, actor, q)
						if len(notices) != 0 {
							t.Fatal("quiet prepare emitted diagnostics", notices)
						}
						vacuumRun(t, p, b, holder, fixture.Exec("BEGIN", 0))
						defer holder.ExecContext(p.Ctx, "ROLLBACK")
						vacuumRun(t, p, b, holder, fixture.Exec("LOCK TABLE vacuum_notice_locked IN ACCESS EXCLUSIVE MODE", 0))
						notices = nil
						if held {
							fixture.RunStep(t, p.Ctx, h, rec, fixture.Exec(q, 0))
						} else {
							arun(fixture.Exec(q, 0))
						}
						if !reflect.DeepEqual(notices, want) {
							t.Errorf("ordered full diagnostics got %#v want %#v", notices, want)
						}
						if got := read("vacuum_notice_denied"); got != deniedBefore {
							t.Errorf("permission-skipped facts changed: got %#v want %#v", got, deniedBefore)
						}
						if got := read("vacuum_notice_locked"); got != lockedBefore {
							t.Errorf("lock-skipped facts changed: got %#v want %#v", got, lockedBefore)
						}
						vacuumProgressEmpty(t, p, b, owner)
						vacuumRun(t, p, b, holder, fixture.Exec("ROLLBACK", 0))
						vacuumRows(t, p, b, owner, "vacuum_notice_denied", 2)
						vacuumRows(t, p, b, owner, "vacuum_notice_locked", 2)
						arun(fixture.Exec("RESET lock_timeout", 0))
						arun(fixture.Exec("RESET ROLE", 0))
					})
				})
			}
		})
	}
}
