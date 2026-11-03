package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatioSysTablesDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, observer := fixture.LockConnections(t, p.Ctx, b.DB)
			statioSequenceExec(t, p, b, a, "CREATE TABLE statio_sys_diagnostic(k integer)")
			fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Exec("INSERT INTO statio_sys_diagnostic VALUES(1),(2)", 2))
			userOID := statioSequenceOID(t, p, a, "public.statio_sys_diagnostic")
			systemOID := statioSequenceOID(t, p, a, "pg_catalog.pg_class")

			// Errors run on a different physical owner so a rejected statement
			// cannot abort the observer's stable statistics snapshot.
			statioSequenceExec(t, p, b, observer, "BEGIN")
			statioSequenceExec(t, p, b, observer, "SET LOCAL stats_fetch_consistency=snapshot")
			systemRow := func(e fixture.Executor) statioTableRow {
				rows := statioTableRows(t, p, b, e, statioSysTableProbe, systemOID)
				if len(rows) != 1 || rows[0].oid != systemOID || rows[0].schema != "pg_catalog" || rows[0].name != "pg_class" {
					t.Fatal("positive represented system identity", rows)
				}
				return rows[0]
			}
			beforeSystem := systemRow(observer)
			beforeUser := statioTableOne(t, p, b, observer, userOID)
			statioTableAbsentToast(t, beforeUser)
			if got := statioTableRows(t, p, b, observer, statioSysTableProbe, userOID); len(got) != 0 {
				t.Fatal("user table is not a system identity", got)
			}
			stmt, err := observer.PrepareContext(p.Ctx, statioSysTableProbe)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: observer, Statement: stmt, SQL: statioSysTableProbe}

			// The released simple SYS view rewrites DML to the non-updatable
			// ALL view. COPY and TRUNCATE reject the original SYS relation.
			// Each diagnostic asserts every semantic field, not only SQLSTATE.
			for _, c := range []struct {
				q              string
				want           fixture.Diagnostic
				query, utility bool
			}{
				{"INSERT INTO pg_catalog.pg_statio_sys_tables DEFAULT VALUES", statioTableWriteDiagnostic("insert into"), false, false},
				{"UPDATE pg_catalog.pg_statio_sys_tables SET heap_blks_hit=0 WHERE false", statioTableWriteDiagnostic("update"), false, false},
				{"DELETE FROM pg_catalog.pg_statio_sys_tables WHERE false", statioTableWriteDiagnostic("delete from"), false, false},
				{"TRUNCATE pg_catalog.pg_statio_sys_tables", fixture.ControlsReferenceDiagnostic("42809", `"pg_statio_sys_tables" is not a table`), false, true},
				{"SELECT stats_reset FROM pg_catalog.pg_statio_sys_tables", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: `column "stats_reset" does not exist`, Position: "8"}, true, false},
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
								preparedStmt, prepareErr := a.PrepareContext(p.Ctx, c.q)
								err, stage = prepareErr, "prepare"
								if err == nil {
									stage = "execute"
									if c.query {
										rows, queryErr := preparedStmt.QueryContext(p.Ctx)
										err = queryErr
										if rows != nil {
											rows.Close()
										}
									} else {
										_, err = preparedStmt.ExecContext(p.Ctx)
									}
									preparedStmt.Close()
								}
							} else if c.query {
								rows, queryErr := a.QueryContext(p.Ctx, c.q)
								err = queryErr
								if rows != nil {
									rows.Close()
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
			const copySQL = "COPY pg_catalog.pg_statio_sys_tables FROM STDIN"
			wantCopy := fixture.ControlsReferenceDiagnostic("42809", `cannot copy to view "pg_statio_sys_tables"`)
			wantCopy.Hint = "To enable copying to a view, provide an INSTEAD OF INSERT trigger."
			t.Run("copy_stream", func(t *testing.T) {
				_, err := fixture.CopyFrom(p.Ctx, copyConn, b.PG, copySQL, strings.NewReader(""))
				fixture.AssertDiagnostic(t, err, &wantCopy)
			})
			t.Run("copy_prepared", func(t *testing.T) {
				preparedCopy, prepareErr := copyConn.PrepareContext(p.Ctx, copySQL)
				if b.PG {
					if prepareErr != nil {
						t.Fatal("oracle COPY prepare admission", prepareErr)
					}
					defer preparedCopy.Close()
					_, executeErr := preparedCopy.ExecContext(p.Ctx)
					fixture.AssertDiagnostic(t, executeErr, &wantCopy)
				} else {
					if preparedCopy != nil {
						preparedCopy.Close()
						t.Error("native COPY prepared without stream capability")
					}
					boundary := fixture.ControlsReferenceDiagnostic("0A000", "COPY FROM STDIN requires stuntgres.CopyFrom")
					fixture.AssertDiagnostic(t, prepareErr, &boundary)
				}
			})
			if !b.PG {
				t.Run("copy_direct_boundary", func(t *testing.T) {
					_, err := copyConn.ExecContext(p.Ctx, copySQL)
					boundary := fixture.ControlsReferenceDiagnostic("0A000", "COPY FROM STDIN requires stuntgres.CopyFrom")
					fixture.AssertDiagnostic(t, err, &boundary)
				})
			}

			for _, e := range []fixture.Executor{observer, held} {
				if after := systemRow(e); after != beforeSystem {
					t.Fatal("rejected writes changed selected identity/snapshot", beforeSystem, after)
				}
				if got := statioTableRows(t, p, b, e, statioSysTableProbe, userOID); len(got) != 0 {
					t.Fatal("rejected writes admitted user identity", got)
				}
			}
			if after := statioTableOne(t, p, b, observer, userOID); after != beforeUser {
				t.Fatal("rejected writes changed user identity/snapshot", beforeUser, after)
			}
			statioSequenceExec(t, p, b, observer, "ROLLBACK")
			// The native current reader must also be unchanged, not merely the
			// retained snapshot. No target source SELECT has generated work yet.
			if !b.PG {
				if after := systemRow(a); after != beforeSystem {
					t.Fatal("rejected writes published system statistics", beforeSystem, after)
				}
				if after := statioTableOne(t, p, b, a, userOID); after != beforeUser {
					t.Fatal("rejected writes published user statistics", beforeUser, after)
				}
			}
			fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT k FROM statio_sys_diagnostic ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		})
	}
}
