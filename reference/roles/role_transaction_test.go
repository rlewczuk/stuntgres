package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleTransactionMutationBoundary(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, q := range []string{"CREATE ROLE tx_created", "ALTER ROLE tx_retained LOGIN", "ALTER USER tx_retained CREATEDB", "DROP ROLE tx_retained", "SET SESSION AUTHORIZATION tx_reader", "RESET SESSION AUTHORIZATION"} {
			t.Run(q+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"tx_created", "tx_retained", "tx_reader"})
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						c, err := backend.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer c.Close()
						for _, s := range []fixture.Step{fixture.Exec("CREATE ROLE tx_retained", 0), fixture.Exec("CREATE USER tx_reader", 0), fixture.Exec("CREATE TABLE tx_sentinel(id INT)", 0), fixture.Exec("INSERT INTO tx_sentinel VALUES(7)", 1)} {
							fixture.RunStep(t, p.Ctx, c, backend.Recorder, s)
						}
						tx, err := c.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						bootstrap := "postgres"
						if backend.PG {
							bootstrap = "stuntgres"
						}
						var session, current string
						if err := tx.QueryRowContext(p.Ctx, "SELECT session_user,current_user").Scan(&session, &current); err != nil || session != bootstrap || current != bootstrap {
							t.Fatal(session, current, err)
						}
						fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.Exec("INSERT INTO tx_sentinel VALUES(8)", 1))
						if prepared {
							stmt, e := tx.PrepareContext(p.Ctx, q)
							err = e
							if stmt != nil {
								if backend.PG {
									_, err = stmt.ExecContext(p.Ctx)
								}
								stmt.Close()
							}
						} else {
							_, err = tx.ExecContext(p.Ctx, q)
						}
						if backend.PG {
							fixture.State(t, err, "")
							if q == "SET SESSION AUTHORIZATION tx_reader" {
								if _, err := tx.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
									t.Fatal(err)
								}
							}
							fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.Exec("INSERT INTO tx_sentinel VALUES(9)", 1))
						} else {
							want := fixture.ControlsReferenceDiagnostic("0A000", "statement is unsupported in an explicit transaction")
							fixture.AssertDiagnostic(t, err, &want)
							_, err = tx.ExecContext(p.Ctx, "INSERT INTO tx_sentinel VALUES(9)")
							want = fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")
							fixture.AssertDiagnostic(t, err, &want)
						}
						if err := tx.Rollback(); err != nil {
							t.Fatal(err)
						}
						roleScenarioIdentity(t, c, bootstrap, nil, backend.PG)
						var id int
						if err := c.QueryRowContext(p.Ctx, "SELECT id FROM tx_sentinel").Scan(&id); err != nil || id != 7 {
							t.Fatal(id, err)
						}
						if backend.PG {
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
