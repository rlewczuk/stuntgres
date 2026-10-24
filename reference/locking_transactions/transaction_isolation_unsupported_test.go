package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionIsolationSupportedSerializableModes(t *testing.T) {
	serializableReferenceScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend) {
		a, _ := fixture.LockConnections(t, p.Ctx, backend.DB)
		for _, mode := range []string{"ISOLATION LEVEL SERIALIZABLE", "DEFERRABLE", "NOT DEFERRABLE"} {
			fixture.RunStep(t, p.Ctx, a, backend.Recorder, fixture.Exec("BEGIN "+mode, 0))
			name, want := "transaction_deferrable", "off"
			if mode == "ISOLATION LEVEL SERIALIZABLE" {
				name, want = "transaction_isolation", "serializable"
			}
			if mode == "DEFERRABLE" {
				want = "on"
			}
			referenceIsolationShow(t, a, name, want)
			fixture.RunStep(t, p.Ctx, a, backend.Recorder, fixture.Exec("ROLLBACK", 0))
		}
	})
}

func TestReferenceTransactionIsolationUtilityUnsupportedBoundary(t *testing.T) {
	for _, query := range []string{"ALTER TABLE t RENAME TO renamed", "COMMENT ON TABLE t IS 'forbidden'"} {
		t.Run(query, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
					run := func(x fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, x, backend.Recorder, s) }
					defer a.ExecContext(p.Ctx, "ROLLBACK")
					isComment := query == "COMMENT ON TABLE t IS 'forbidden'"
					comment := func(x fixture.Executor, want any) {
						run(x, fixture.Select("SELECT obj_description('public.t'::regclass,'pg_class') AS text", []string{"text"}, []string{"TEXT"}, [][]any{{want}}))
					}
					run(b, fixture.Exec("CREATE TABLE t(v INT)", 0))
					catalog := func() {
						var count int64
						if err := b.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='renamed'").Scan(&count); err != nil || count != 0 {
							t.Fatal(count, err)
						}
						var comment any
						if err := b.QueryRowContext(p.Ctx, "SELECT obj_description('public.t'::regclass,'pg_class')").Scan(&comment); err != nil || comment != nil {
							t.Fatal(comment, err)
						}
					}
					run(b, fixture.Exec("INSERT INTO t VALUES(1)", 1))
					for _, mode := range []string{"READ ONLY", "READ WRITE"} {
						run(a, fixture.Exec("BEGIN "+mode, 0))
						stmt, err := a.PrepareContext(p.Ctx, query)
						if err != nil || stmt == nil {
							t.Fatal("inside utility Prepare", stmt, err)
						}
						result, err := stmt.ExecContext(p.Ctx)
						stmt.Close()
						if mode == "READ ONLY" {
							if result != nil {
								t.Fatal("read-only utility returned a result")
							}
							tag := "ALTER TABLE"
							if query == "COMMENT ON TABLE t IS 'forbidden'" {
								tag = "COMMENT"
							}
							readOnly := fixture.ControlsReferenceDiagnostic("25006", "cannot execute "+tag+" in a read-only transaction")
							fixture.AssertDiagnostic(t, err, &readOnly)
						} else {
							if err != nil {
								t.Fatal(err)
							}
							fixture.LockAffected(t, result, 0)
							if isComment {
								comment(a, "forbidden")
								comment(b, nil)
							}
						}
						run(a, fixture.Exec("ROLLBACK", 0))
						run(a, fixture.TransactionRows(false, "SELECT v FROM t", 1))
						catalog()
					}
					held, err := a.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					for _, prepared := range []bool{false, true} {
						run(a, fixture.Exec("BEGIN READ WRITE", 0))
						var result interface{ RowsAffected() (int64, error) }
						if prepared {
							result, err = held.ExecContext(p.Ctx)
						} else {
							result, err = a.ExecContext(p.Ctx, query)
						}
						if err != nil || result == nil {
							t.Fatal(result, err)
						}
						if n, err := result.RowsAffected(); err != nil || n != 0 {
							t.Fatal(n, err)
						}
						if isComment {
							comment(a, "forbidden")
							comment(b, nil)
						}
						run(a, fixture.Exec("ROLLBACK", 0))
						run(a, fixture.TransactionRows(false, "SELECT v FROM t", 1))
						catalog()
						if isComment {
							run(a, fixture.Exec("BEGIN READ WRITE", 0))
							if prepared {
								result, err = held.ExecContext(p.Ctx)
							} else {
								result, err = a.ExecContext(p.Ctx, query)
							}
							if err != nil || result == nil {
								t.Fatal(result, err)
							}
							if n, err := result.RowsAffected(); err != nil || n != 0 {
								t.Fatal(n, err)
							}
							comment(a, "forbidden")
							comment(b, nil)
							run(a, fixture.Exec("COMMIT", 0))
							comment(b, "forbidden")
							run(a, fixture.TransactionRows(false, "SELECT v FROM t", 1))
							run(a, fixture.Exec("COMMENT ON TABLE t IS NULL", 0))
							catalog()
						}
					}
				})
			}
		})
	}
}
