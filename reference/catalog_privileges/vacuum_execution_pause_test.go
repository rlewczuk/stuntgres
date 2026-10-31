package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// PostgreSQL has no host ExecutionPause API. Common SQL outcomes below are
// independent expectations on both backends; only native phases assert controls.
func TestReferenceVacuumExecutionPause(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"vacuum_pause_denied"})
	vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("prepared_%t", prepared), func(t *testing.T) {
				worker, observer := vacuumConn(t, p, b), vacuumConn(t, p, b)
				table := fmt.Sprintf("vacuum_pause_%t", prepared)
				vacuumPauseSeed(t, p, b, worker, table)
				id := copyProgressIdentify(t, p, worker, table)
				query := "VACUUM (PROCESS_TOAST false) " + table
				var executor fixture.Executor = worker
				metadata := stuntgres.NewExecutionPause("vacuum.inspect")
				t.Cleanup(metadata.Resume)
				metadataCtx := p.Ctx
				if !b.PG {
					metadataCtx = stuntgres.WithExecutionPause(metadataCtx, metadata)
				}
				statement, err := worker.PrepareContext(metadataCtx, query)
				if err != nil {
					t.Fatal("nonexecuting Prepare", err)
				}
				t.Cleanup(func() { statement.Close() })
				vacuumPauseUnadmitted(t, metadata)
				vacuumCount(t, p.Ctx, worker, table, 0)
				vacuumProgressEmpty(t, p, b, observer)
				if prepared {
					executor = fixture.HeldStatement{Executor: worker, Statement: statement, SQL: query}
				}
				observation := vacuumHeld(t, p, observer, vacuumProgressQuery)
				pause := stuntgres.NewExecutionPause("vacuum.inspect")
				ctx := p.Ctx
				if !b.PG {
					ctx = stuntgres.WithExecutionPause(ctx, pause)
				}
				r := vacuumPauseLaunch(t, ctx, executor, query, pause)
				if !b.PG {
					r.admitted(t, p.Ctx, pause)
					for _, c := range []fixture.Executor{observer, observation} {
						vacuumPauseObserve(t, p, c, id)
					}
					pause.Resume()
				}
				vacuumPauseSuccess(t, r.join())
				pause.Resume()
				r.cancel() // Late source cancellation cannot retract committed success.
				vacuumCount(t, p.Ctx, worker, table, 1)
				vacuumPauseData(t, p, b, worker, table)
				vacuumPauseHealthy(t, p, b, worker, observer)
				vacuumPauseNonadmission(t, p, b, worker, observer, table, prepared)
				if !b.PG {
					vacuumPauseNative(t, p, b, worker, observer, table, executor, query)
				}
			})
		}
		vacuumPauseDenied(t, p, b)
	})
}

func vacuumPauseNonadmission(t *testing.T, p *fixture.Pair, b fixture.Backend, worker, observer *sql.Conn, table string, prepared bool) {
	t.Helper()
	copyProgressExec(t, p.Ctx, worker, "CREATE TABLE "+table+"_empty(id INT)")
	for _, q := range []string{
		"SELECT * FROM pg_stat_progress_vacuum WHERE false",
		"VACUUM (PROCESS_MAIN false, PROCESS_TOAST false) " + table,
		"VACUUM " + table + "_empty",
		"VACUUM " + table + "_missing",
	} {
		pause := stuntgres.NewExecutionPause("vacuum.inspect")
		t.Cleanup(pause.Resume)
		ctx := p.Ctx
		if !b.PG {
			ctx = stuntgres.WithExecutionPause(ctx, pause)
		}
		var c fixture.Executor = worker
		if prepared {
			s, err := worker.PrepareContext(ctx, q)
			if err != nil {
				t.Fatal("nonexecuting metadata", q, err)
			}
			t.Cleanup(func() { s.Close() })
			c = fixture.HeldStatement{Executor: worker, Statement: s, SQL: q}
		}
		var step fixture.Step
		switch q {
		case "SELECT * FROM pg_stat_progress_vacuum WHERE false":
			// ColumnTypes/Describe must not activate a maintenance point.
			rows, err := c.QueryContext(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer rows.Close()
				vacuumProgressTypes(t, rows)
				if rows.Next() || rows.Err() != nil {
					t.Fatal("nonexecuted query returned progress")
				}
			}()
		case "VACUUM " + table + "_missing":
			step = fixture.ImplicitError(q, fixture.Diagnostic{Code: "42P01", Message: `relation "` + table + `_missing" does not exist`})
			fixture.RunStep(t, ctx, c, b.Recorder, step)
		default:
			fixture.RunStep(t, ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		vacuumPauseUnadmitted(t, pause)
		vacuumPauseHealthy(t, p, b, worker, observer)
	}
	vacuumCount(t, p.Ctx, worker, table, 1)
}

func vacuumPauseDenied(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	owner := vacuumConn(t, p, b)
	copyProgressExec(t, p.Ctx, owner, "CREATE ROLE vacuum_pause_denied")
	copyProgressExec(t, p.Ctx, owner, "GRANT USAGE ON SCHEMA public TO vacuum_pause_denied")
	copyProgressExec(t, p.Ctx, owner, "CREATE TABLE vacuum_pause_acl(id INT)")
	copyProgressExec(t, p.Ctx, owner, "INSERT INTO vacuum_pause_acl VALUES(1)")
	var notices []fixture.Diagnostic
	_, actor, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
	copyProgressExec(t, p.Ctx, actor, "SET ROLE vacuum_pause_denied")
	for _, prepared := range []bool{false, true} {
		pause := stuntgres.NewExecutionPause("vacuum.inspect")
		t.Cleanup(pause.Resume)
		ctx := p.Ctx
		if !b.PG {
			ctx = stuntgres.WithExecutionPause(ctx, pause)
		}
		step := fixture.Exec("VACUUM vacuum_pause_acl", 0)
		if prepared {
			step = fixture.Prepared(step)
		}
		notices = nil
		fixture.RunStep(t, ctx, actor, rec, step)
		vacuumWarning(t, notices, "01000", `permission denied to vacuum "vacuum_pause_acl", skipping it`)
		vacuumPauseUnadmitted(t, pause)
		vacuumProgressEmpty(t, p, b, owner)
		vacuumCount(t, p.Ctx, owner, "vacuum_pause_acl", 0)
		vacuumRows(t, p, b, owner, "vacuum_pause_acl", 1)
	}
	copyProgressExec(t, p.Ctx, actor, "RESET ROLE")
}
