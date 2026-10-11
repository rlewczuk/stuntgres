package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func batchIdentityPromotion(t *testing.T) {
	for _, sessionAuthorization := range []bool{false, true} {
		command := "SET ROLE batch_identity"
		if sessionAuthorization {
			command = "SET SESSION AUTHORIZATION batch_identity"
		}
		t.Run("identity_promotion/"+command, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"batch_identity"})
			batchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
				batchSeed(t, p, b, a)
				batchExec(t, p, b, a, "CREATE ROLE batch_identity LOGIN", 0)
				batchExec(t, p, b, a, "GRANT SELECT ON batch_t TO batch_identity", 0)
				if b.PG {
					batchExec(t, p, b, a, "GRANT USAGE ON SCHEMA public TO batch_identity", 0)
				}
				var current, session string
				if err := a.QueryRowContext(p.Ctx, "SELECT current_user,session_user").Scan(&current, &session); err != nil {
					t.Fatal(err)
				}
				reachedSession := session
				if sessionAuthorization {
					reachedSession = "batch_identity"
				}
				identitySQL := "SELECT current_user AS cu,session_user AS su"
				reached := batchSet{[]string{"cu", "su"}, []string{"NAME", "NAME"}, [][]any{{"batch_identity", reachedSession}}, false}
				original := batchSet{[]string{"cu", "su"}, []string{"NAME", "NAME"}, [][]any{{current, session}}, false}
				identity := func(w batchSet) {
					fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select(identitySQL, w.columns, w.types, w.rows))
				}
				q := command + "; " + identitySQL + "; SELECT i FROM batch_t; BEGIN; SAVEPOINT sp; SELECT missing"
				want := batchDiag("42703", `column "missing" does not exist`)
				want.Position = batchPosition(q, "missing")
				r := batchQuery(t, p, a, q)
				batchReadSet(t, r, reached)
				if !r.NextResultSet() {
					t.Fatal("authorization set", r.Err())
				}
				batchReadSet(t, r, batchSet{[]string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}, false})
				fixture.AssertDiagnostic(t, r.Err(), &want)
				r.Close()
				batchExec(t, p, b, a, "ROLLBACK TO sp", 0)
				identity(reached)
				batchValues(t, p, b, a, "SELECT i FROM batch_t", 7)
				batchExec(t, p, b, a, "ROLLBACK", 0)
				identity(original)
				batchExec(t, p, b, a, command+"; BEGIN", 0)
				identity(reached)
				batchExec(t, p, b, a, "COMMIT", 0)
				identity(reached)
				batchExec(t, p, b, a, "RESET SESSION AUTHORIZATION; RESET ROLE", 0)
				identity(original)
				if sessionAuthorization {
					// A later session authorization supersedes the earlier LOCAL role at commit.
					q := "SET LOCAL ROLE batch_identity; " + command + "; " + identitySQL
					r := batchQuery(t, p, a, q)
					batchReadSet(t, r, reached)
					if r.NextResultSet() || r.Err() != nil {
						t.Fatal("LOCAL/session authorization completion", r.Err())
					}
					if err := r.Close(); err != nil {
						t.Fatal(err)
					}
					identity(reached)
					batchExec(t, p, b, a, "RESET SESSION AUTHORIZATION; RESET ROLE", 0)
					identity(original)
				}
				// Explicit SET ROLE follows its settings journal; SESSION AUTHORIZATION
				// retains the independent native exclusion.
				batchExec(t, p, b, a, "BEGIN", 0)
				if b.PG || !sessionAuthorization {
					batchExec(t, p, b, a, command, 0)
					identity(reached)
				} else {
					batchError(t, p, a, command, batchDiag("0A000", "statement is unsupported in an explicit transaction"))
				}
				batchExec(t, p, b, a, "ROLLBACK", 0)
				identity(original)
			})
		})
	}
}
