package reference_test

import "testing"

func TestReferenceRoleTransactionMutationBoundary(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, q := range []string{"CREATE ROLE tx_created", "ALTER ROLE tx_retained LOGIN", "ALTER USER tx_retained CREATEDB", "DROP ROLE tx_retained", "SET SESSION AUTHORIZATION tx_reader", "RESET SESSION AUTHORIZATION"} {
			t.Run(q+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				roleReferenceCleanup(t, []string{"tx_created", "tx_retained", "tx_reader"})
				p := newReferenceScenario(t)
				for _, backend := range p.backends() {
					t.Run(backend.name, func(t *testing.T) {
						c, err := backend.db.Conn(p.ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer c.Close()
						for _, s := range []referenceStep{referenceExec("CREATE ROLE tx_retained", 0), referenceExec("CREATE USER tx_reader", 0), referenceExec("CREATE TABLE tx_sentinel(id INT)", 0), referenceExec("INSERT INTO tx_sentinel VALUES(7)", 1)} {
							referenceRunStep(t, p.ctx, c, backend.recorder, s)
						}
						tx, err := c.BeginTx(p.ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						bootstrap := "postgres"
						if backend.pg {
							bootstrap = "stuntgres"
						}
						var session, current string
						if err := tx.QueryRowContext(p.ctx, "SELECT session_user,current_user").Scan(&session, &current); err != nil || session != bootstrap || current != bootstrap {
							t.Fatal(session, current, err)
						}
						referenceRunStep(t, p.ctx, tx, backend.recorder, referenceExec("INSERT INTO tx_sentinel VALUES(8)", 1))
						if prepared {
							stmt, e := tx.PrepareContext(p.ctx, q)
							err = e
							if stmt != nil {
								if backend.pg {
									_, err = stmt.ExecContext(p.ctx)
								}
								stmt.Close()
							}
						} else {
							_, err = tx.ExecContext(p.ctx, q)
						}
						if backend.pg {
							referenceState(t, err, "")
							if q == "SET SESSION AUTHORIZATION tx_reader" {
								if _, err := tx.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION"); err != nil {
									t.Fatal(err)
								}
							}
							referenceRunStep(t, p.ctx, tx, backend.recorder, referenceExec("INSERT INTO tx_sentinel VALUES(9)", 1))
						} else {
							want := controlsReferenceDiagnostic("0A000", "statement is unsupported in an explicit transaction")
							referenceAssertDiagnostic(t, err, &want)
							_, err = tx.ExecContext(p.ctx, "INSERT INTO tx_sentinel VALUES(9)")
							want = controlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")
							referenceAssertDiagnostic(t, err, &want)
						}
						if err := tx.Rollback(); err != nil {
							t.Fatal(err)
						}
						roleScenarioIdentity(t, c, bootstrap, nil, backend.pg)
						var id int
						if err := c.QueryRowContext(p.ctx, "SELECT id FROM tx_sentinel").Scan(&id); err != nil || id != 7 {
							t.Fatal(id, err)
						}
						if backend.pg {
							roleAttributesFacts(t, p, "tx_retained", roleAttributesDefaults(false))
						}
						roleScenarioCommand(t, c, "CREATE ROLE tx_created", 0, false)
						roleScenarioCommand(t, c, "DROP ROLE tx_retained,tx_created", 0, false)
					})
				}
			})
		}
	}
}
