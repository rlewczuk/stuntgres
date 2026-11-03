package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatioUserTablesDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, observer := fixture.LockConnections(t, p.Ctx, b.DB)
			statioSequenceExec(t, p, b, a, "CREATE TABLE statio_user_diagnostic(k integer)")
			fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Exec("INSERT INTO statio_user_diagnostic VALUES(1),(2)", 2))
			oid := statioSequenceOID(t, p, a, "public.statio_user_diagnostic")
			catalog := fixture.Select("SELECT oid,relname,relkind,reltoastrelid FROM pg_class WHERE oid=$1::oid", []string{"oid", "relname", "relkind", "reltoastrelid"}, []string{"OID", "NAME", "CHAR", "OID"}, [][]any{{oid, "statio_user_diagnostic", "r", int64(0)}}, oid)
			fixture.PgDescriptionExecutorRows(t, p, b, a, catalog)

			// Rejections run on another physical owner so a failed statement
			// cannot abort the observer's stable statistics snapshot.
			statioSequenceExec(t, p, b, observer, "BEGIN")
			statioSequenceExec(t, p, b, observer, "SET LOCAL stats_fetch_consistency=snapshot")
			userRow := func(e fixture.Executor) statioTableRow {
				rows := statioTableRows(t, p, b, e, statioUserTableProbe, oid)
				if len(rows) != 1 || rows[0].oid != oid || rows[0].schema != "public" || rows[0].name != "statio_user_diagnostic" {
					t.Fatal("positive represented user identity", rows)
				}
				statioTableAbsentToast(t, rows[0])
				if rows[0].blocks[2].Valid || rows[0].blocks[3].Valid {
					t.Fatal("empty index aggregate must be NULL", rows)
				}
				return rows[0]
			}
			beforeUser := userRow(observer)
			statioTableHeap(t, b, beforeUser, 0)
			beforeAll := statioTableOne(t, p, b, observer, oid)
			if beforeAll != beforeUser {
				t.Fatal("shared snapshot ALL/USER cells", beforeAll, beforeUser)
			}
			stmt, err := observer.PrepareContext(p.Ctx, statioUserTableProbe)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: observer, Statement: stmt, SQL: statioUserTableProbe}

			// The simple USER wrapper rewrites DML to the non-updatable ALL
			// join view; utility errors retain the selected USER target name.
			// Diagnostic equality covers every semantic field, including empty
			// fields, while the fixture excludes PostgreSQL C-source locations.
			for _, c := range []struct {
				q              string
				want           fixture.Diagnostic
				query, utility bool
			}{
				{"INSERT INTO pg_catalog.pg_statio_user_tables DEFAULT VALUES", statioTableWriteDiagnostic("insert into"), false, false},
				{"UPDATE pg_catalog.pg_statio_user_tables SET heap_blks_hit=0 WHERE false", statioTableWriteDiagnostic("update"), false, false},
				{"DELETE FROM pg_catalog.pg_statio_user_tables WHERE false", statioTableWriteDiagnostic("delete from"), false, false},
				{"TRUNCATE pg_catalog.pg_statio_user_tables", fixture.ControlsReferenceDiagnostic("42809", `"pg_statio_user_tables" is not a table`), false, true},
				{"SELECT stats_reset FROM pg_catalog.pg_statio_user_tables", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: `column "stats_reset" does not exist`, Position: "8"}, true, false},
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
											t.Error("rejected query returned rows")
										}
									} else {
										_, err = preparedStmt.ExecContext(p.Ctx)
									}
									preparedStmt.Close()
								} else if preparedStmt != nil {
									preparedStmt.Close()
									t.Error("rejected prepare returned a statement")
								}
							} else if c.query {
								rows, queryErr := a.QueryContext(p.Ctx, c.q)
								err = queryErr
								if rows != nil {
									rows.Close()
									t.Error("rejected query returned rows")
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
			const copySQL = "COPY pg_catalog.pg_statio_user_tables FROM STDIN"
			wantCopy := fixture.ControlsReferenceDiagnostic("42809", `cannot copy to view "pg_statio_user_tables"`)
			wantCopy.Hint = "To enable copying to a view, provide an INSTEAD OF INSERT trigger."
			t.Run("copy_stream", func(t *testing.T) {
				result, err := fixture.CopyFrom(p.Ctx, copyConn, b.PG, copySQL, strings.NewReader(""))
				fixture.AssertDiagnostic(t, err, &wantCopy)
				if result.Count != 0 || result.CommandTag != "" {
					t.Fatal("rejected COPY returned affected rows or completion tag", result)
				}
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
				if after := userRow(e); after != beforeUser {
					t.Fatal("rejected writes changed selected identity/snapshot", beforeUser, after)
				}
			}
			if after := statioTableOne(t, p, b, observer, oid); after != beforeAll {
				t.Fatal("rejected writes changed ALL identity/snapshot", beforeAll, after)
			}
			statioSequenceExec(t, p, b, observer, "ROLLBACK")
			// Check a current native Reader as well as the retained snapshot.
			// No SELECT from the user table has generated source work yet.
			if !b.PG {
				if after := userRow(a); after != beforeUser {
					t.Fatal("rejected writes published user statistics", beforeUser, after)
				}
				if after := statioTableOne(t, p, b, a, oid); after != beforeAll {
					t.Fatal("rejected writes published ALL statistics", beforeAll, after)
				}
			}
			fixture.PgDescriptionExecutorRows(t, p, b, a, catalog)
			fixture.PgDescriptionExecutorRows(t, p, b, a, fixture.Prepared(catalog))
			fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT k FROM statio_user_diagnostic ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		})
	}
}
