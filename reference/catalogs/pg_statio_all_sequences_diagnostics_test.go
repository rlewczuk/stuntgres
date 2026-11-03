package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func statioSequenceWriteDiagnostic(verb string) fixture.Diagnostic {
	gerund := map[string]string{"insert into": "inserting into", "update": "updating", "delete from": "deleting from"}[verb]
	action := map[string]string{"insert into": "INSERT", "update": "UPDATE", "delete from": "DELETE"}[verb]
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: "cannot " + verb + " view \"pg_statio_all_sequences\"", Detail: "Views that do not select from a single table or view are not automatically updatable.", Hint: "To enable " + gerund + " the view, provide an INSTEAD OF " + action + " trigger or an unconditional ON " + action + " DO INSTEAD rule."}
}

func TestReferencePGStatioAllSequencesDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
			statioSequenceExec(t, p, b, a, "CREATE SEQUENCE statio_diagnostic")
			oid := statioSequenceOID(t, p, a, "public.statio_diagnostic")
			for _, c := range []struct {
				q              string
				want           fixture.Diagnostic
				query, utility bool
			}{
				{"INSERT INTO pg_catalog.pg_statio_all_sequences DEFAULT VALUES", statioSequenceWriteDiagnostic("insert into"), false, false},
				{"UPDATE pg_catalog.pg_statio_all_sequences SET blks_hit=0 WHERE false", statioSequenceWriteDiagnostic("update"), false, false},
				{"DELETE FROM pg_catalog.pg_statio_all_sequences WHERE false", statioSequenceWriteDiagnostic("delete from"), false, false},
				{"TRUNCATE pg_catalog.pg_statio_all_sequences", fixture.ControlsReferenceDiagnostic("42809", `"pg_statio_all_sequences" is not a table`), false, true},
				{"SELECT stats_reset FROM pg_catalog.pg_statio_all_sequences", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: `column "stats_reset" does not exist`, Position: "8"}, true, false},
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
			_, err = fixture.CopyFrom(p.Ctx, copyConn, b.PG, "COPY pg_catalog.pg_statio_all_sequences FROM STDIN", strings.NewReader(""))
			want := fixture.ControlsReferenceDiagnostic("42809", `cannot copy to view "pg_statio_all_sequences"`)
			want.Hint = "To enable copying to a view, provide an INSTEAD OF INSERT trigger."
			fixture.AssertDiagnostic(t, err, &want)
			// database/sql has no native prepared stream capability. Preserve its
			// existing admission error rather than adding a COPY implementation.
			preparedCopy, prepareErr := copyConn.PrepareContext(p.Ctx, "COPY pg_catalog.pg_statio_all_sequences FROM STDIN")
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
				_, directErr := copyConn.ExecContext(p.Ctx, "COPY pg_catalog.pg_statio_all_sequences FROM STDIN")
				fixture.AssertDiagnostic(t, directErr, &boundary)
			}
			statioSequenceCheck(t, p, b, a, oid, "public", "statio_diagnostic", 0, 0)
			fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT last_value,is_called FROM statio_diagnostic", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
		})
	}
}
