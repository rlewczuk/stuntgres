package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceIndexMaintenancePreparedAdmission(t *testing.T) {
	for _, q := range []string{"REINDEX INDEX ix", "REINDEX TABLE t", "DROP INDEX ix", "ALTER INDEX ix RENAME TO fresh"} {
		t.Run(q, func(t *testing.T) {
			indexMaintenanceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
				stmt, err := a.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal("Prepare must be quiet before target exists", err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: a, Statement: stmt, SQL: q}
				target := "ix"
				if q == "REINDEX TABLE t" {
					target = "t"
				}
				missing := fixture.Diagnostic{Code: "42P01", Message: `relation "` + target + `" does not exist`}
				if q == "DROP INDEX ix" {
					missing.Code = "42704"
					missing.Message = `index "ix" does not exist`
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ImplicitError(q, missing))
				run(fixture.Exec("CREATE TABLE t(n INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
				run(fixture.Exec("CREATE INDEX ix ON t(n)", 0))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 0))
				if q == "ALTER INDEX ix RENAME TO fresh" {
					run(fixture.Exec("ALTER INDEX fresh RENAME TO ix", 0))
				}
				if q == "DROP INDEX ix" {
					run(fixture.Exec("CREATE INDEX ix ON t(n)", 0))
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 0))
				run(fixture.Select("SELECT n FROM t", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
			})
		})
	}
	for _, readOnly := range []bool{false, true} {
		for _, q := range []string{"DROP INDEX missing", "ALTER INDEX missing RENAME TO fresh", "REINDEX INDEX missing", "REINDEX TABLE missing"} {
			t.Run(q+map[bool]string{false: "/writable", true: "/readonly"}[readOnly], func(t *testing.T) {
				indexMaintenanceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
					begin := "BEGIN"
					if readOnly {
						begin = "BEGIN READ ONLY"
					}
					fixture.LockExec(t, p.Ctx, a, begin, 0)
					stmt, err := a.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal("Prepare rejected admission", err)
					}
					defer stmt.Close()
					r, err := stmt.ExecContext(p.Ctx)
					if r != nil {
						t.Fatal("failed utility returned result", r)
					}
					if readOnly && (!b.PG || q == "DROP INDEX missing" || q == "ALTER INDEX missing RENAME TO fresh") {
						tag := "REINDEX"
						if q == "DROP INDEX missing" {
							tag = "DROP INDEX"
						}
						if q == "ALTER INDEX missing RENAME TO fresh" {
							tag = "ALTER INDEX"
						}
						want := fixture.ControlsReferenceDiagnostic("25006", "cannot execute "+tag+" in a read-only transaction")
						fixture.AssertDiagnostic(t, err, &want)
					} else if !b.PG {
						want := fixture.ControlsReferenceDiagnostic("0A000", "statement is unsupported in an explicit transaction")
						fixture.AssertDiagnostic(t, err, &want)
					} else {
						want := fixture.ControlsReferenceDiagnostic("42P01", `relation "missing" does not exist`)
						if q == "DROP INDEX missing" {
							want = fixture.ControlsReferenceDiagnostic("42704", `index "missing" does not exist`)
						}
						fixture.AssertDiagnostic(t, err, &want)
					}
					fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
				})
			})
		}
	}
}

func TestReferenceIndexMaintenanceTransactionalRollback(t *testing.T) {
	for _, q := range []string{"ALTER INDEX ix RENAME TO fresh", "DROP INDEX ix", "REINDEX INDEX ix", "REINDEX TABLE t"} {
		t.Run(q, func(t *testing.T) {
			indexMaintenanceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE t(n INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
				run(fixture.Exec("CREATE INDEX ix ON t(n)", 0))
				run(fixture.Exec("BEGIN", 0))
				s := fixture.Exec(q, 0)
				if !b.PG {
					s = fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "statement is unsupported in an explicit transaction"})
				}
				run(fixture.Prepared(s))
				run(fixture.Exec("ROLLBACK", 0))
				run(fixture.Select("SELECT pg_get_indexdef('ix'::regclass)", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{"CREATE INDEX ix ON public.t USING btree (n)"}}))
				run(fixture.Select("SELECT n FROM t", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
			})
		})
	}
}

func TestReferenceIndexMaintenancePreparedSearchPath(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "ima")
	fixture.SchemaReferenceCleanup(t, "imb")
	indexMaintenanceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
		for _, q := range []string{"CREATE SCHEMA ima", "CREATE SCHEMA imb", "CREATE TABLE ima.t(n INT)", "CREATE TABLE imb.t(n INT)", "CREATE INDEX ix ON ima.t(n)", "CREATE INDEX ix ON imb.t(n)", "SET search_path=ima"} {
			run(fixture.Exec(q, 0))
		}
		stmt, err := a.PrepareContext(p.Ctx, "DROP INDEX ix")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: a, Statement: stmt, SQL: "DROP INDEX ix"}
		run(fixture.Exec("SET search_path=imb", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec("DROP INDEX ix", 0))
		run(fixture.Select("SELECT pg_get_indexdef('ima.ix'::regclass)", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{"CREATE INDEX ix ON ima.t USING btree (n)"}}))
		run(fixture.Exec("SET search_path=ima", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec("DROP INDEX ix", 0))
		run(fixture.Exec("CREATE INDEX ix ON ima.t(n)", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec("DROP INDEX ix", 0))
	})
}
