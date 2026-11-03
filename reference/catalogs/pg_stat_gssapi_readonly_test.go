package reference_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func gssapiWriteDiagnostic(verb string) fixture.Diagnostic {
	if verb == "TRUNCATE" {
		return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: `"pg_stat_gssapi" is not a table`}
	}
	if verb == "COPY" {
		return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: `cannot copy to view "pg_stat_gssapi"`, Hint: "To enable copying to a view, provide an INSTEAD OF INSERT trigger."}
	}
	action := map[string]string{"INSERT": "insert into", "UPDATE": "update", "DELETE": "delete from"}[verb]
	hintAction := map[string]string{"INSERT": "inserting into", "UPDATE": "updating", "DELETE": "deleting from"}[verb]
	return fixture.Diagnostic{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000",
		Message: "cannot " + action + ` view "pg_stat_gssapi"`,
		Detail:  "Views that do not select from a single table or view are not automatically updatable.",
		Hint:    "To enable " + hintAction + " the view, provide an INSTEAD OF " + verb + " trigger or an unconditional ON " + verb + " DO INSTEAD rule.",
	}
}

func TestReferencePGStatGSSAPIReadOnly(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			gssapiSetupSentinel(t, p, b)
			a, peer := gssapiConn(t, p, b.DB), gssapiConn(t, p, b.DB)
			aPID, peerPID := gssapiPID(t, p, a), gssapiPID(t, p, peer)
			query := gssapiQuery + " WHERE pid IN ($1,$2)"
			for _, tc := range []struct{ name, query, verb string }{
				{"insert_default", "INSERT INTO pg_catalog.pg_stat_gssapi DEFAULT VALUES", "INSERT"},
				{"insert_typed", "INSERT INTO pg_catalog.pg_stat_gssapi(pid,gss_authenticated,principal,encrypted,credentials_delegated) VALUES(1,false,NULL::text,false,false)", "INSERT"},
				{"update", "UPDATE pg_catalog.pg_stat_gssapi SET encrypted=true", "UPDATE"},
				{"update_empty", "UPDATE pg_catalog.pg_stat_gssapi SET encrypted=true WHERE pid=-1", "UPDATE"},
				{"delete", "DELETE FROM pg_catalog.pg_stat_gssapi", "DELETE"},
				{"delete_empty", "DELETE FROM pg_catalog.pg_stat_gssapi WHERE pid=-1", "DELETE"},
				{"truncate", "TRUNCATE pg_catalog.pg_stat_gssapi", "TRUNCATE"},
			} {
				for _, prepared := range []bool{false, true} {
					for _, transaction := range []bool{false, true} {
						t.Run(fmt.Sprintf("%s/prepared=%t/transaction=%t", tc.name, prepared, transaction), func(t *testing.T) {
							if transaction {
								gssapiExec(t, p, a, "BEGIN")
								defer a.ExecContext(context.Background(), "ROLLBACK")
								gssapiExec(t, p, a, "SAVEPOINT gss_write")
							}
							step := fixture.ErrorDiagnostic(tc.query, gssapiWriteDiagnostic(tc.verb))
							step.Prepared, step.ErrorStage = prepared, "execute"
							if prepared && tc.verb != "TRUNCATE" {
								step.ErrorStage = "prepare"
							}
							fixture.RunStep(t, p.Ctx, a, b.Recorder, step)
							if transaction {
								gssapiExec(t, p, a, "ROLLBACK TO gss_write")
							}
							gssapiSentinel(t, p, b, a)
							gssapiBoth(t, p, b, a, query, gssapiTuples(aPID, peerPID), aPID, peerPID)
							if transaction {
								gssapiExec(t, p, a, "COMMIT")
							}
						})
					}
				}
			}
			for _, transaction := range []bool{false, true} {
				t.Run(fmt.Sprintf("copy/transaction=%t", transaction), func(t *testing.T) {
					pool := fixture.CopyScenarioPool(t, p, b.PG, "", "")
					conn := gssapiConn(t, p, pool)
					pid := gssapiPID(t, p, conn)
					if transaction {
						gssapiExec(t, p, conn, "BEGIN")
						defer conn.ExecContext(context.Background(), "ROLLBACK")
						gssapiExec(t, p, conn, "SAVEPOINT gss_copy")
					}
					result, err := fixture.CopyFrom(p.Ctx, conn, b.PG, "COPY pg_catalog.pg_stat_gssapi FROM STDIN", strings.NewReader("1\tf\t\\N\tf\tf\n"))
					want := gssapiWriteDiagnostic("COPY")
					fixture.AssertDiagnostic(t, err, &want)
					if result.Count != 0 || result.CommandTag != "" {
						t.Fatal("failed COPY published result", result)
					}
					if transaction {
						gssapiExec(t, p, conn, "ROLLBACK TO gss_copy")
					}
					gssapiBoth(t, p, b, conn, gssapiPIDQuery, gssapiTuples(pid), pid)
					if transaction {
						gssapiExec(t, p, conn, "COMMIT")
					}
					gssapiSentinel(t, p, b, a)
					gssapiBoth(t, p, b, a, query, gssapiTuples(aPID, peerPID), aPID, peerPID)
				})
			}
		})
	}
}
