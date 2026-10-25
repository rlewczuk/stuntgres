package reference_test

import (
	"database/sql"
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceVacuumF1SelectionSkipLocked(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
			vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				var notices []fixture.Diagnostic
				_, worker, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
				holder := vacuumConn(t, p, b)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, worker, rec, s) }
				for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE vacuum_review_skip(id INT)", 0), fixture.Exec("INSERT INTO vacuum_review_skip VALUES(1),(2)", 2), fixture.Exec("DELETE FROM vacuum_review_skip WHERE id=1", 1)} {
					run(s)
				}
				q := "VACUUM (SKIP_LOCKED) vacuum_review_skip"
				statement := vacuumHeld(t, p, worker, q)
				run(fixture.Exec("CREATE TABLE vacuum_review_open(id INT)", 0))
				run(fixture.Exec("INSERT INTO vacuum_review_open VALUES(5)", 1))
				multiSQL := "VACUUM (SKIP_LOCKED) vacuum_review_skip,vacuum_review_open"
				multi := vacuumHeld(t, p, worker, multiSQL)
				run(fixture.Exec("SET lock_timeout='100ms'", 0))
				vacuumRun(t, p, b, holder, fixture.Exec("BEGIN", 0))
				defer holder.ExecContext(p.Ctx, "ROLLBACK")
				vacuumRun(t, p, b, holder, fixture.Exec("LOCK TABLE vacuum_review_skip IN ACCESS EXCLUSIVE MODE", 0))
				notices = nil
				if held {
					fixture.RunStep(t, p.Ctx, statement, rec, fixture.Exec(q, 0))
				} else {
					run(fixture.Exec(q, 0))
				}
				vacuumWarning(t, notices, "55P03", `skipping vacuum of "vacuum_review_skip" --- lock not available`)
				vacuumCount(t, p.Ctx, worker, "vacuum_review_skip", 0)
				run(fixture.Select("SELECT last_vacuum IS NULL AS untouched FROM pg_stat_all_tables WHERE relid='vacuum_review_skip'::regclass", []string{"untouched"}, []string{"BOOL"}, [][]any{{true}}))
				vacuumProgressEmpty(t, p, b, worker)
				if b.PG {
					pid := fixture.LockPID(t, p.Ctx, worker)
					var queued int
					if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_locks WHERE pid=$1 AND NOT granted", pid).Scan(&queued); err != nil {
						t.Fatal(err)
					}
					if queued != 0 {
						t.Fatal("skipped selection left a queued request", queued)
					}
				}
				notices = nil
				if held {
					fixture.RunStep(t, p.Ctx, multi, rec, fixture.Exec(multiSQL, 0))
				} else {
					run(fixture.Exec(multiSQL, 0))
				}
				vacuumWarning(t, notices, "55P03", `skipping vacuum of "vacuum_review_skip" --- lock not available`)
				vacuumCount(t, p.Ctx, worker, "vacuum_review_skip", 0)
				vacuumCount(t, p.Ctx, worker, "vacuum_review_open", 1)
				vacuumRows(t, p, b, worker, "vacuum_review_open", 5)
				vacuumProgressEmpty(t, p, b, worker)
				vacuumRun(t, p, b, holder, fixture.Exec("ROLLBACK", 0))
				run(fixture.Exec("RESET lock_timeout", 0))
				notices = nil
				if held {
					fixture.RunStep(t, p.Ctx, statement, rec, fixture.Exec(q, 0))
				} else {
					run(fixture.Exec(q, 0))
				}
				if len(notices) != 0 {
					t.Fatal("healthy selection recovery warned", notices)
				}
				vacuumCount(t, p.Ctx, worker, "vacuum_review_skip", 1)
				vacuumRows(t, p, b, worker, "vacuum_review_skip", 2)
			})
		})
	}
}

func TestReferenceVacuumF2OlderPublisherHorizon(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
			for _, phase := range []string{"initial", "empty_vacuum", "older_birth"} {
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
							t.Fatal("not a genuine assigned publisher", assigned, err)
						}
						low := strconv.FormatUint(uint64(uint32(full)), 10)
						run(creator, fixture.Exec("CREATE TABLE vacuum_review_birth(id INT)", 0))
						if phase != "initial" {
							run(creator, fixture.Exec("VACUUM (FREEZE) vacuum_review_birth", 0))
							run(creator, fixture.Exec("VACUUM (ONLY_DATABASE_STATS)", 0))
						}
						if phase == "older_birth" {
							run(older, fixture.Exec("INSERT INTO vacuum_review_birth VALUES(7)", 1))
							var stillAssigned string
							if err := older.QueryRowContext(p.Ctx, "SELECT pg_current_xact_id()::text").Scan(&stillAssigned); err != nil || stillAssigned != assigned {
								t.Fatal("older publisher identity changed", assigned, stillAssigned, err)
							}
							run(older, fixture.Exec("COMMIT", 0))
							// Database-only refresh observes bounds without freezing/repairing rows.
							run(creator, fixture.Exec("VACUUM (ONLY_DATABASE_STATS)", 0))
							vacuumRows(t, p, b, creator, "vacuum_review_birth", 7)
						}
						q := "SELECT age(relfrozenxid)>=age($1::xid) AS safe FROM pg_class WHERE oid='vacuum_review_birth'::regclass"
						run(creator, fixture.Select(q, []string{"safe"}, []string{"BOOL"}, [][]any{{true}}, low))
						if phase == "older_birth" {
							run(creator, fixture.Select("SELECT age(datfrozenxid)>=age($1::xid) AS safe FROM pg_database WHERE datname=current_database()", []string{"safe"}, []string{"BOOL"}, [][]any{{true}}, low))
							run(creator, fixture.Exec("VACUUM (FREEZE) vacuum_review_birth", 0))
							vacuumRows(t, p, b, creator, "vacuum_review_birth", 7)
							run(creator, fixture.Select("SELECT age(relfrozenxid)>=0 AS valid FROM pg_class WHERE oid='vacuum_review_birth'::regclass", []string{"valid"}, []string{"BOOL"}, [][]any{{true}}))
						}
					})
				})
			}
		})
	}
}
