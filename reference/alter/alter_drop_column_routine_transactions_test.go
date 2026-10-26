package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterDropColumnRoutineTransactions(t *testing.T) {
	for _, goTx := range []bool{false, true} {
		for _, held := range []bool{false, true} {
			name := "sql/direct"
			if goTx {
				name = "go/direct"
			}
			if held {
				name += "/held"
			}
			t.Run(name, func(t *testing.T) {
				dcrScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
					dcrCascadeSetup(t, p, b, c)
					before := dcrRows(t, p, c, dcrSnapshotSQL)
					ids := dcrRetiredIDs(t, p, c)
					peer, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer peer.Close()
					dcrExec(t, p, b, peer, "CREATE TABLE dcr_peer(v integer)")
					dcrRun(t, p, b, peer, fixture.Exec("INSERT INTO dcr_peer VALUES(41)", 1))
					begin := func() (fixture.Executor, func(bool)) {
						if !goTx {
							dcrExec(t, p, b, c, "BEGIN")
							return c, func(commit bool) {
								q := "ROLLBACK"
								if commit {
									q = "COMMIT"
								}
								dcrExec(t, p, b, c, q)
							}
						}
						tx, err := c.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { _ = tx.Rollback() })
						return tx, func(commit bool) {
							var err error
							if commit {
								err = tx.Commit()
							} else {
								err = tx.Rollback()
							}
							if err != nil {
								t.Fatal(err)
							}
						}
					}
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					for _, commit := range []bool{false, true} {
						x, end := begin()
						dcrExec(t, p, b, x, "SAVEPOINT dcr_mark")
						for range 2 {
							drop := fixture.Exec("ALTER TABLE dcr_t DROP a CASCADE", 0)
							if held {
								stmt, err := x.PrepareContext(p.Ctx, drop.Query)
								if err != nil {
									t.Fatal(err)
								}
								dcrRun(t, p, b, fixture.HeldStatement{Executor: x, Statement: stmt, SQL: drop.Query}, drop)
								stmt.Close()
							} else {
								dcrRun(t, p, b, x, drop)
							}
							dcrNotices(t, notices, dcrCascadeObjects()...)
							dcrCascadeSurvivors(t, p, b, x, ids)
							// Catalog visibility does not request the owner's table AX.
							dcrRun(t, p, b, peer, fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname='dcr_body'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
							dcrExec(t, p, b, x, "ROLLBACK TO dcr_mark")
							dcrUnchanged(t, p, b, x, before)
						}
						restrict := fixture.ErrorDiagnostic("ALTER TABLE dcr_t DROP a RESTRICT", dcrBodyAndClosureRestrict())
						restrict.ErrorStage = "execute"
						dcrRun(t, p, b, x, restrict)
						aborted := fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block"))
						aborted.ErrorStage = "execute"
						dcrRun(t, p, b, x, aborted)
						dcrExec(t, p, b, x, "ROLLBACK TO dcr_mark")
						dcrUnchanged(t, p, b, x, before)
						dcrExec(t, p, b, x, "ALTER TABLE dcr_t DROP a CASCADE", "RELEASE dcr_mark")
						dcrNotices(t, notices, dcrCascadeObjects()...)
						end(commit)
						if !commit {
							dcrUnchanged(t, p, b, c, before)
						} else {
							dcrCascadeSurvivors(t, p, b, c, ids)
						}
					}
					dcrRun(t, p, b, c, fixture.Select("SELECT v FROM dcr_peer", []string{"v"}, []string{"INT4"}, [][]any{{int64(41)}}))
				})
			})
		}
	}
}

func dcrBodyAndClosureRestrict() fixture.Diagnostic {
	return dcrRestrict("dcr_t", "a", "function dcr_body() depends on column a of table dcr_t\nfunction dcr_caller() depends on function dcr_body()\nfunction dcr_shared() depends on function dcr_body()\nview dcr_view depends on function dcr_shared()\nfunction dcr_default(integer) depends on column a of table dcr_t\nfunction dcr_both(integer) depends on column a of table dcr_t")
}
