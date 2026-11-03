package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatioUserSequencesDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
			statioSequenceExec(t, p, b, a, "CREATE SEQUENCE statio_user_diagnostic")
			oid := statioSequenceOID(t, p, a, "public.statio_user_diagnostic")
			// DML reaches the underlying joined all view; utility commands retain
			// the original user target, including their distinct prepared stages.
			for _, c := range []struct {
				q              string
				want           fixture.Diagnostic
				query, utility bool
			}{
				{"INSERT INTO pg_catalog.pg_statio_user_sequences DEFAULT VALUES", statioSequenceWriteDiagnostic("insert into"), false, false},
				{"UPDATE pg_catalog.pg_statio_user_sequences SET blks_hit=0 WHERE false", statioSequenceWriteDiagnostic("update"), false, false},
				{"DELETE FROM pg_catalog.pg_statio_user_sequences WHERE false", statioSequenceWriteDiagnostic("delete from"), false, false},
				{"TRUNCATE pg_catalog.pg_statio_user_sequences", fixture.ControlsReferenceDiagnostic("42809", `"pg_statio_user_sequences" is not a table`), false, true},
				{"SELECT stats_reset FROM pg_catalog.pg_statio_user_sequences", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: `column "stats_reset" does not exist`, Position: "8"}, true, false},
			} {
				t.Run(c.q, func(t *testing.T) {
					for _, prepared := range []bool{false, true} {
						name := "direct"
						if prepared {
							name = "prepared"
						}
						t.Run(name, func(t *testing.T) {
							var err error
							stage := "execute"
							if prepared {
								stmt, e := a.PrepareContext(p.Ctx, c.q)
								err, stage = e, "prepare"
								if err != nil && stmt != nil {
									stmt.Close()
									t.Error("failed admission returned a live statement")
								}
								if err == nil {
									stage = "execute"
									if c.query {
										rows, e := stmt.QueryContext(p.Ctx)
										err = e
										if rows != nil {
											rows.Close()
											t.Error("rejected query exposed rows")
										}
									} else {
										_, err = stmt.ExecContext(p.Ctx)
									}
									stmt.Close()
								}
							} else if c.query {
								rows, e := a.QueryContext(p.Ctx, c.q)
								err = e
								if rows != nil {
									rows.Close()
									t.Error("rejected query exposed rows")
								}
							} else {
								_, err = a.ExecContext(p.Ctx, c.q)
							}
							wantStage := "execute"
							if prepared && !c.utility {
								wantStage = "prepare"
							}
							if stage != wantStage {
								t.Errorf("failure stage=%s want=%s", stage, wantStage)
							}
							fixture.AssertDiagnostic(t, err, &c.want)
						})
					}
				})
			}
			pool := fixture.CopyScenarioPool(t, p, b.PG, "", "")
			copyConn, err := pool.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer copyConn.Close()
			const copySQL = "COPY pg_catalog.pg_statio_user_sequences FROM STDIN"
			wantCopy := fixture.ControlsReferenceDiagnostic("42809", `cannot copy to view "pg_statio_user_sequences"`)
			wantCopy.Hint = "To enable copying to a view, provide an INSTEAD OF INSERT trigger."
			t.Run("copy_stream", func(t *testing.T) {
				_, err := fixture.CopyFrom(p.Ctx, copyConn, b.PG, copySQL, strings.NewReader(""))
				fixture.AssertDiagnostic(t, err, &wantCopy)
			})
			t.Run("copy_prepared", func(t *testing.T) {
				stmt, err := copyConn.PrepareContext(p.Ctx, copySQL)
				if b.PG {
					if err != nil {
						t.Fatal("oracle COPY prepare admission", err)
					}
					defer stmt.Close()
					_, err = stmt.ExecContext(p.Ctx)
					fixture.AssertDiagnostic(t, err, &wantCopy)
				} else {
					if stmt != nil {
						stmt.Close()
						t.Error("native COPY prepared without stream capability")
					}
					boundary := fixture.ControlsReferenceDiagnostic("0A000", "COPY FROM STDIN requires stuntgres.CopyFrom")
					fixture.AssertDiagnostic(t, err, &boundary)
				}
			})
			if !b.PG {
				t.Run("copy_direct_boundary", func(t *testing.T) {
					_, err := copyConn.ExecContext(p.Ctx, copySQL)
					boundary := fixture.ControlsReferenceDiagnostic("0A000", "COPY FROM STDIN requires stuntgres.CopyFrom")
					fixture.AssertDiagnostic(t, err, &boundary)
				})
			}
			statioUserSequenceCheck(t, p, b, a, oid, "public", "statio_user_diagnostic", 0, 0)
			statioSequenceCheck(t, p, b, a, oid, "public", "statio_user_diagnostic", 0, 0)
			fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT last_value,is_called FROM statio_user_diagnostic", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
		})
	}
}
