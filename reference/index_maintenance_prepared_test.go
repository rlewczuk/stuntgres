package reference_test

import "testing"

func TestReferenceIndexMaintenancePreparedAdmission(t *testing.T) {
	for _, q := range []string{"REINDEX INDEX ix", "REINDEX TABLE t", "DROP INDEX ix", "ALTER INDEX ix RENAME TO fresh"} {
		t.Run(q, func(t *testing.T) {
			indexMaintenanceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				a, _ := referenceLockConnections(t, p.ctx, b.db)
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, b.recorder, s) }
				stmt, err := a.PrepareContext(p.ctx, q)
				if err != nil {
					t.Fatal("Prepare must be quiet before target exists", err)
				}
				defer stmt.Close()
				held := referenceHeldStatement{referenceExecutor: a, statement: stmt, query: q}
				target := "ix"
				if q == "REINDEX TABLE t" {
					target = "t"
				}
				missing := referenceDiagnostic{Code: "42P01", Message: `relation "` + target + `" does not exist`}
				if q == "DROP INDEX ix" {
					missing.Code = "42704"
					missing.Message = `index "ix" does not exist`
				}
				referenceRunStep(t, p.ctx, held, b.recorder, referenceImplicitError(q, missing))
				run(referenceExec("CREATE TABLE t(n INT)", 0))
				run(referenceExec("INSERT INTO t VALUES(1)", 1))
				run(referenceExec("CREATE INDEX ix ON t(n)", 0))
				referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(q, 0))
				if q == "ALTER INDEX ix RENAME TO fresh" {
					run(referenceExec("ALTER INDEX fresh RENAME TO ix", 0))
				}
				if q == "DROP INDEX ix" {
					run(referenceExec("CREATE INDEX ix ON t(n)", 0))
				}
				referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(q, 0))
				run(referenceSelect("SELECT n FROM t", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
			})
		})
	}
	for _, readOnly := range []bool{false, true} {
		for _, q := range []string{"DROP INDEX missing", "ALTER INDEX missing RENAME TO fresh", "REINDEX INDEX missing", "REINDEX TABLE missing"} {
			t.Run(q+map[bool]string{false: "/writable", true: "/readonly"}[readOnly], func(t *testing.T) {
				indexMaintenanceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
					a, _ := referenceLockConnections(t, p.ctx, b.db)
					begin := "BEGIN"
					if readOnly {
						begin = "BEGIN READ ONLY"
					}
					referenceLockExec(t, p.ctx, a, begin, 0)
					stmt, err := a.PrepareContext(p.ctx, q)
					if err != nil {
						t.Fatal("Prepare rejected admission", err)
					}
					defer stmt.Close()
					r, err := stmt.ExecContext(p.ctx)
					if r != nil {
						t.Fatal("failed utility returned result", r)
					}
					if readOnly && (!b.pg || q == "DROP INDEX missing" || q == "ALTER INDEX missing RENAME TO fresh") {
						tag := "REINDEX"
						if q == "DROP INDEX missing" {
							tag = "DROP INDEX"
						}
						if q == "ALTER INDEX missing RENAME TO fresh" {
							tag = "ALTER INDEX"
						}
						want := controlsReferenceDiagnostic("25006", "cannot execute "+tag+" in a read-only transaction")
						referenceAssertDiagnostic(t, err, &want)
					} else if !b.pg {
						want := controlsReferenceDiagnostic("0A000", "statement is unsupported in an explicit transaction")
						referenceAssertDiagnostic(t, err, &want)
					} else {
						want := controlsReferenceDiagnostic("42P01", `relation "missing" does not exist`)
						if q == "DROP INDEX missing" {
							want = controlsReferenceDiagnostic("42704", `index "missing" does not exist`)
						}
						referenceAssertDiagnostic(t, err, &want)
					}
					referenceLockExec(t, p.ctx, a, "ROLLBACK", 0)
				})
			})
		}
	}
}

func TestReferenceIndexMaintenanceTransactionalRollback(t *testing.T) {
	for _, q := range []string{"ALTER INDEX ix RENAME TO fresh", "DROP INDEX ix", "REINDEX INDEX ix", "REINDEX TABLE t"} {
		t.Run(q, func(t *testing.T) {
			indexMaintenanceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				a, _ := referenceLockConnections(t, p.ctx, b.db)
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, b.recorder, s) }
				run(referenceExec("CREATE TABLE t(n INT)", 0))
				run(referenceExec("INSERT INTO t VALUES(1)", 1))
				run(referenceExec("CREATE INDEX ix ON t(n)", 0))
				run(referenceExec("BEGIN", 0))
				s := referenceExec(q, 0)
				if !b.pg {
					s = referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: "statement is unsupported in an explicit transaction"})
				}
				run(referencePrepared(s))
				run(referenceExec("ROLLBACK", 0))
				run(referenceSelect("SELECT pg_get_indexdef('ix'::regclass)", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{"CREATE INDEX ix ON public.t USING btree (n)"}}))
				run(referenceSelect("SELECT n FROM t", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
			})
		})
	}
}

func TestReferenceIndexMaintenancePreparedSearchPath(t *testing.T) {
	schemaReferenceCleanup(t, "ima")
	schemaReferenceCleanup(t, "imb")
	indexMaintenanceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, _ := referenceLockConnections(t, p.ctx, b.db)
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, b.recorder, s) }
		for _, q := range []string{"CREATE SCHEMA ima", "CREATE SCHEMA imb", "CREATE TABLE ima.t(n INT)", "CREATE TABLE imb.t(n INT)", "CREATE INDEX ix ON ima.t(n)", "CREATE INDEX ix ON imb.t(n)", "SET search_path=ima"} {
			run(referenceExec(q, 0))
		}
		stmt, err := a.PrepareContext(p.ctx, "DROP INDEX ix")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: a, statement: stmt, query: "DROP INDEX ix"}
		run(referenceExec("SET search_path=imb", 0))
		referenceRunStep(t, p.ctx, held, b.recorder, referenceExec("DROP INDEX ix", 0))
		run(referenceSelect("SELECT pg_get_indexdef('ima.ix'::regclass)", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{"CREATE INDEX ix ON ima.t USING btree (n)"}}))
		run(referenceExec("SET search_path=ima", 0))
		referenceRunStep(t, p.ctx, held, b.recorder, referenceExec("DROP INDEX ix", 0))
		run(referenceExec("CREATE INDEX ix ON ima.t(n)", 0))
		referenceRunStep(t, p.ctx, held, b.recorder, referenceExec("DROP INDEX ix", 0))
	})
}
