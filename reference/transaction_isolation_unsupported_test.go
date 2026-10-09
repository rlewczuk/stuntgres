package reference_test

import "testing"

func TestReferenceTransactionIsolationSupportedSerializableModes(t *testing.T) {
	serializableReferenceScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend) {
		a, _ := referenceLockConnections(t, p.ctx, backend.db)
		for _, mode := range []string{"ISOLATION LEVEL SERIALIZABLE", "DEFERRABLE", "NOT DEFERRABLE"} {
			referenceRunStep(t, p.ctx, a, backend.recorder, referenceExec("BEGIN "+mode, 0))
			name, want := "transaction_deferrable", "off"
			if mode == "ISOLATION LEVEL SERIALIZABLE" {
				name, want = "transaction_isolation", "serializable"
			}
			if mode == "DEFERRABLE" {
				want = "on"
			}
			referenceIsolationShow(t, a, name, want)
			referenceRunStep(t, p.ctx, a, backend.recorder, referenceExec("ROLLBACK", 0))
		}
	})
}

func TestReferenceTransactionIsolationUtilityUnsupportedBoundary(t *testing.T) {
	for _, query := range []string{"ALTER TABLE t RENAME TO renamed", "COMMENT ON TABLE t IS 'forbidden'"} {
		t.Run(query, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					a, b := referenceLockConnections(t, p.ctx, backend.db)
					run := func(x referenceExecutor, s referenceStep) { referenceRunStep(t, p.ctx, x, backend.recorder, s) }
					run(b, referenceExec("CREATE TABLE t(v INT)", 0))
					catalog := func() {
						var count int64
						if err := b.QueryRowContext(p.ctx, "SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='renamed'").Scan(&count); err != nil || count != 0 {
							t.Fatal(count, err)
						}
						var comment any
						if err := b.QueryRowContext(p.ctx, "SELECT obj_description('public.t'::regclass,'pg_class')").Scan(&comment); err != nil || comment != nil {
							t.Fatal(comment, err)
						}
					}
					run(b, referenceExec("INSERT INTO t VALUES(1)", 1))
					want := controlsReferenceDiagnostic("0A000", "statement is unsupported in an explicit transaction")
					for _, mode := range []string{"READ ONLY", "READ WRITE"} {
						run(a, referenceExec("BEGIN "+mode, 0))
						stmt, err := a.PrepareContext(p.ctx, query)
						if !backend.pg {
							if stmt != nil {
								stmt.Close()
								t.Fatal("unsupported transaction Prepare returned a handle")
							}
							referenceAssertDiagnostic(t, err, &want)
						} else {
							if err != nil || stmt == nil {
								t.Fatal("PG inside utility Prepare", stmt, err)
							}
							result, err := stmt.ExecContext(p.ctx)
							stmt.Close()
							if mode == "READ ONLY" {
								if result != nil {
									t.Fatal("read-only utility returned a result")
								}
								tag := "ALTER TABLE"
								if query == "COMMENT ON TABLE t IS 'forbidden'" {
									tag = "COMMENT"
								}
								readOnly := controlsReferenceDiagnostic("25006", "cannot execute "+tag+" in a read-only transaction")
								referenceAssertDiagnostic(t, err, &readOnly)
							} else {
								if err != nil {
									t.Fatal(err)
								}
								referenceLockAffected(t, result, 0)
							}
						}
						run(a, referenceExec("ROLLBACK", 0))
						if backend.pg {
							run(a, referenceTransactionRows(false, "SELECT v FROM t", 1))
							catalog()
						}
					}
					held, err := a.PrepareContext(p.ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					for _, prepared := range []bool{false, true} {
						run(a, referenceExec("BEGIN READ WRITE", 0))
						var result interface{ RowsAffected() (int64, error) }
						if prepared {
							result, err = held.ExecContext(p.ctx)
						} else {
							result, err = a.ExecContext(p.ctx, query)
						}
						if backend.pg {
							if err != nil || result == nil {
								t.Fatal(result, err)
							}
							if n, err := result.RowsAffected(); err != nil || n != 0 {
								t.Fatal(n, err)
							}
						} else {
							if result != nil {
								t.Fatal("unsupported utility returned a result")
							}
							referenceAssertDiagnostic(t, err, &want)
						}
						run(a, referenceExec("ROLLBACK", 0))
						run(a, referenceTransactionRows(false, "SELECT v FROM t", 1))
						if backend.pg {
							catalog()
						}
					}
				})
			}
		})
	}
}
