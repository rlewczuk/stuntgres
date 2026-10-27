package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStatisticsNDistinctPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"nd_actor", "nd_owner", "nd_group"})
	fixture.SchemaReferenceCleanup(t, "nd_hidden")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			for _, q := range []string{"CREATE ROLE nd_actor", "CREATE ROLE nd_owner", "CREATE ROLE nd_group", "GRANT USAGE ON SCHEMA public TO nd_actor,nd_owner,nd_group", "CREATE TABLE nd_t(a INT,b INT)", "CREATE STATISTICS nd_s(ndistinct) ON a,b FROM nd_t", "CREATE SCHEMA nd_hidden", "CREATE TABLE nd_hidden.h(a INT,b INT)"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO nd_t VALUES(1,1),(2,2)", 2))
			run(fixture.Exec("ANALYZE nd_t", 0))
			run(fixture.Exec("INSERT INTO nd_t VALUES(3,3)", 1))
			var notices []fixture.Diagnostic
			_, actor, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			arun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, actor, rec, s) }
			arun(fixture.Exec("SET ROLE nd_actor", 0))
			defer actor.ExecContext(p.Ctx, "RESET ROLE")
			for _, prepared := range []bool{false, true} {
				notices = nil
				step := fixture.Exec("ANALYZE nd_t", 0)
				step.Prepared = prepared
				arun(step)
				want := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: `permission denied to analyze "nd_t", skipping it`}
				if !reflect.DeepEqual(notices, []fixture.Diagnostic{want}) {
					t.Fatal(notices)
				}
				step = fixture.ErrorDiagnostic("ANALYZE nd_hidden.h", fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema nd_hidden"))
				step.Prepared = prepared
				arun(step)
				step = fixture.ErrorDiagnostic("SELECT * FROM pg_statistic_ext_data", fixture.ControlsReferenceDiagnostic("42501", "permission denied for table pg_statistic_ext_data"))
				step.SelectRows = true
				step.Prepared = prepared
				step.ErrorStage = "execute"
				arun(step)
				step = statisticsError("ALTER STATISTICS nd_s SET STATISTICS 0", "42501", "must be owner of statistics object nd_s")
				step.Prepared = prepared
				arun(step)
			}
			run(ndistinctRows(b.PG, []any{"nd_s", false, `{"1, 2": 2}`}))
			run(fixture.Exec("GRANT MAINTAIN ON nd_t TO nd_actor", 0))
			notices = nil
			arun(fixture.Prepared(fixture.Exec("ANALYZE nd_t", 0)))
			if len(notices) != 0 {
				t.Fatal(notices)
			}
			run(ndistinctRows(b.PG, []any{"nd_s", false, `{"1, 2": 3}`}))
			run(fixture.Exec("REVOKE MAINTAIN ON nd_t FROM nd_actor", 0))
			run(fixture.Exec("GRANT MAINTAIN ON nd_t TO nd_group", 0))
			run(fixture.Exec("GRANT nd_group TO nd_actor", 0))
			arun(fixture.Exec("ANALYZE nd_t", 0))
			run(fixture.Exec("REVOKE nd_group FROM nd_actor", 0))
			run(fixture.Exec("ALTER TABLE nd_t OWNER TO nd_owner", 0))
			run(fixture.Exec("GRANT nd_owner TO nd_actor", 0))
			arun(fixture.Exec("ANALYZE nd_t", 0))
			// Table ownership does not substitute for independent statistics ownership.
			arun(statisticsError("ALTER STATISTICS nd_s SET STATISTICS 0", "42501", "must be owner of statistics object nd_s"))
			arun(fixture.Exec("RESET ROLE", 0))
		})
	}
}

func TestReferenceStatisticsNDistinctCancellation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					for _, q := range []string{"CREATE TABLE nd_t(a INT,b INT)", "CREATE STATISTICS nd_s(ndistinct) ON a,b FROM nd_t"} {
						run(fixture.Exec(q, 0))
					}
					run(fixture.Exec("INSERT INTO nd_t VALUES(1,1),(2,2)", 2))
					run(fixture.Exec("ANALYZE nd_t", 0))
					run(fixture.Exec("INSERT INTO nd_t VALUES(3,3)", 1))
					holder, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer holder.Close()
					defer holder.ExecContext(context.Background(), "ROLLBACK")
					fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("BEGIN", 0))
					fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("LOCK TABLE nd_t IN SHARE MODE", 0))
					worker, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer worker.Close()
					query := "ANALYZE nd_t"
					var held *sql.Stmt
					if prepared {
						held, err = worker.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						defer held.Close()
					}
					waiterPID, holderPID := 0, 0
					var oid int64
					if b.PG {
						waiterPID = fixture.LockPID(t, p.Ctx, worker)
						holderPID = fixture.LockPID(t, p.Ctx, holder)
						oid = fixture.LockOID(t, p.Ctx, "nd_t")
					}
					fixture.BinaryConversionActiveCancelAfter(t, p, b, worker, query, "(*relationRequest).wait", func(ctx context.Context) {
						if b.PG {
							fixture.LockWaitBehind(t, ctx, waiterPID, holderPID, oid, "ShareUpdateExclusiveLock")
						}
					}, func(ctx context.Context) fixture.RelationOutcome {
						var out fixture.RelationOutcome
						if prepared {
							out.Result, out.Err = held.ExecContext(ctx)
						} else {
							out.Result, out.Err = worker.ExecContext(ctx, query)
						}
						return out
					})
					run(ndistinctRows(b.PG, []any{"nd_s", false, `{"1, 2": 2}`}))
					fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("ROLLBACK", 0))
					run(fixture.Exec("ANALYZE nd_t", 0))
					run(ndistinctRows(b.PG, []any{"nd_s", false, `{"1, 2": 3}`}))
				})
			}
		})
	}
}

func TestReferenceStatisticsNDistinctDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, conn, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, rec, s) }
			for _, q := range []string{"CREATE TABLE nd_t(a INT,b INT,c INT)", "CREATE STATISTICS nd_s(ndistinct) ON a,c FROM nd_t"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO nd_t VALUES(1,1,1)", 1))
			for _, prepared := range []bool{false, true} {
				notices = nil
				step := fixture.Exec("ANALYZE nd_t(a,b)", 0)
				step.Prepared = prepared
				run(step)
				want := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "42P17", Message: `statistics object "public.nd_s" could not be computed for relation "public.nd_t"`, Schema: "public", Table: "nd_t"}
				if !reflect.DeepEqual(notices, []fixture.Diagnostic{want}) {
					t.Fatal(notices)
				}
				notices = nil
				step = fixture.Exec("ALTER STATISTICS nd_s SET STATISTICS 10001", 0)
				step.Prepared = prepared
				run(step)
				want = fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "lowering statistics target to 10000"}
				if !reflect.DeepEqual(notices, []fixture.Diagnostic{want}) {
					t.Fatal(notices)
				}
				notices = nil
				step = fixture.Exec("ALTER STATISTICS IF EXISTS absent_nd SET STATISTICS 10001", 0)
				step.Prepared = prepared
				run(step)
				missing := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `statistics object "absent_nd" does not exist, skipping`}
				if !reflect.DeepEqual(notices, []fixture.Diagnostic{want, missing}) {
					t.Fatal(notices)
				}
			}
		})
	}
}
