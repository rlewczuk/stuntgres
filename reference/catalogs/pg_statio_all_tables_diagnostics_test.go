package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func statioTableWriteDiagnostic(verb string) fixture.Diagnostic {
	gerund := map[string]string{"insert into": "inserting into", "update": "updating", "delete from": "deleting from"}[verb]
	action := map[string]string{"insert into": "INSERT", "update": "UPDATE", "delete from": "DELETE"}[verb]
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: "cannot " + verb + " view \"pg_statio_all_tables\"", Detail: "Views that do not select from a single table or view are not automatically updatable.", Hint: "To enable " + gerund + " the view, provide an INSTEAD OF " + action + " trigger or an unconditional ON " + action + " DO INSTEAD rule."}
}

func TestReferencePGStatioAllTablesDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
			statioSequenceExec(t, p, b, a, "CREATE TABLE statio_diagnostic(k integer)")
			fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Exec("INSERT INTO statio_diagnostic VALUES(1),(2)",2))
			oid := statioSequenceOID(t, p, a, "public.statio_diagnostic")
			before:=statioTableOne(t,p,b,a,oid)
			statioTableAbsentToast(t,before)
			for _, c := range []struct {
				q              string
				want           fixture.Diagnostic
				query, utility bool
			}{
				{"INSERT INTO pg_catalog.pg_statio_all_tables DEFAULT VALUES", statioTableWriteDiagnostic("insert into"), false, false},
				{"UPDATE pg_catalog.pg_statio_all_tables SET heap_blks_hit=0 WHERE false", statioTableWriteDiagnostic("update"), false, false},
				{"DELETE FROM pg_catalog.pg_statio_all_tables WHERE false", statioTableWriteDiagnostic("delete from"), false, false},
				{"TRUNCATE pg_catalog.pg_statio_all_tables", fixture.ControlsReferenceDiagnostic("42809", `"pg_statio_all_tables" is not a table`), false, true},
				{"SELECT stats_reset FROM pg_catalog.pg_statio_all_tables", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: `column "stats_reset" does not exist`, Position: "8"}, true, false},
			} {
				t.Run(c.q, func(t *testing.T) {
					for _, prepared := range []bool{false, true} {
						var err error
						stage := "execute"
						if prepared {
							stmt, e := a.PrepareContext(p.Ctx, c.q)
							err, stage = e, "prepare"
							if err == nil {
								stage = "execute"
								if c.query {
									rows, e := stmt.QueryContext(p.Ctx)
									err = e
									if rows != nil {
										rows.Close()
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
							}
						} else {
							_, err = a.ExecContext(p.Ctx, c.q)
						}
						fixture.AssertDiagnostic(t, err, &c.want)
						wantStage := "execute"
						if prepared && !c.utility {
							wantStage = "prepare"
						}
						if stage != wantStage {
							t.Errorf("prepared=%v stage=%s want=%s", prepared, stage, wantStage)
						}
					}
				})
			}
			// COPY uses the genuine supported stream API, not ordinary Exec's
			// transport-boundary error. No input row can reach a read-only view.
			pool := fixture.CopyScenarioPool(t, p, b.PG, "", "")
			copyConn, err := pool.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer copyConn.Close()
			_, err = fixture.CopyFrom(p.Ctx, copyConn, b.PG, "COPY pg_catalog.pg_statio_all_tables FROM STDIN", strings.NewReader(""))
			want := fixture.ControlsReferenceDiagnostic("42809", `cannot copy to view "pg_statio_all_tables"`)
			want.Hint = "To enable copying to a view, provide an INSTEAD OF INSERT trigger."
			fixture.AssertDiagnostic(t, err, &want)
			// database/sql has no native prepared stream capability. Preserve its
			// existing admission error rather than adding a COPY implementation.
			preparedCopy, prepareErr := copyConn.PrepareContext(p.Ctx, "COPY pg_catalog.pg_statio_all_tables FROM STDIN")
			if b.PG {
				if prepareErr != nil {
					t.Fatal("oracle COPY prepare admission", prepareErr)
				}
				_, executeErr := preparedCopy.ExecContext(p.Ctx)
				preparedCopy.Close()
				fixture.AssertDiagnostic(t, executeErr, &want)
			} else {
				if preparedCopy != nil {
					preparedCopy.Close()
					t.Fatal("native COPY prepared without stream capability")
				}
				boundary := fixture.ControlsReferenceDiagnostic("0A000", "COPY FROM STDIN requires stuntgres.CopyFrom")
				fixture.AssertDiagnostic(t, prepareErr, &boundary)
				_, directErr := copyConn.ExecContext(p.Ctx, "COPY pg_catalog.pg_statio_all_tables FROM STDIN")
				fixture.AssertDiagnostic(t, directErr, &boundary)
			}
			after:=statioTableOne(t,p,b,a,oid)
			statioTableAbsentToast(t,after)
			statioTableHeap(t,b,after,0)
			if !b.PG && after!=before {t.Fatal("rejected writes changed identity/provider values",before,after)}
			fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT k FROM statio_diagnostic ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)},{int64(2)}}))
		})
	}
}