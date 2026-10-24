package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleTransactionalAtomicity(t *testing.T) {
	for _, kind := range []string{"grant", "revoke", "drop_missing", "drop_dependent", "drop_current", "drop_session", "initial_missing", "initial_cycle", "attribute", "date"} {
		for _, held := range []bool{false, true} {
			for _, mode := range []string{"autocommit", "sql", "go"} {
				t.Run(fmt.Sprintf("%s/held=%v/%s", kind, held, mode), func(t *testing.T) {
					roleTransactionT2Cases(t, []string{"ra_parent", "ra_member", "ra_empty", "ra_owner", "ra_super", "ra_new", "ra_kept"}, []string{"ra_dep"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
						c, _ := roleTransactionT2Conn(t, p, b)
						run := func(e fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
						bootstrap := "postgres"
						if b.PG {
							bootstrap = "stuntgres"
						}
						for _, q := range []string{"CREATE ROLE ra_parent", "CREATE ROLE ra_member", "CREATE ROLE ra_empty", "CREATE ROLE ra_owner", "CREATE ROLE ra_super SUPERUSER", "CREATE SCHEMA ra_dep AUTHORIZATION ra_owner", "CREATE TABLE ra_sentinel(i INT)"} {
							run(c, fixture.Exec(q, 0))
						}
						run(c, fixture.Exec("INSERT INTO ra_sentinel VALUES(7)", 1))
						if kind == "revoke" {
							run(c, fixture.Exec("GRANT ra_parent TO ra_member", 0))
						}
						e, finish := roleTransactionBegin(t, p, b, c, mode)
						run(e, fixture.Exec("CREATE ROLE ra_kept", 0))
						run(e, fixture.Exec("INSERT INTO ra_sentinel VALUES(8)", 1))
						if kind == "drop_session" {
							run(e, fixture.Exec("SET ROLE ra_super", 0))
						}
						query, code, message, detail := "", "", "", ""
						switch kind {
						case "grant":
							query, code, message = "GRANT ra_parent TO ra_member,ra_missing", "42704", `role "ra_missing" does not exist`
						case "revoke":
							query, code, message = "REVOKE ra_parent FROM ra_member,ra_missing", "42704", `role "ra_missing" does not exist`
						case "drop_missing":
							query, code, message = "DROP ROLE ra_empty,ra_missing", "42704", `role "ra_missing" does not exist`
						case "drop_dependent":
							query, code, message, detail = "DROP ROLE ra_empty,ra_owner", "2BP01", `role "ra_owner" cannot be dropped because some objects depend on it`, "owner of schema ra_dep"
						case "drop_current":
							query, code, message = "DROP ROLE ra_empty,"+bootstrap, "55006", "current user cannot be dropped"
						case "drop_session":
							query, code, message = "DROP ROLE ra_empty,"+bootstrap, "55006", "session user cannot be dropped"
						case "initial_missing":
							query, code, message = "CREATE ROLE ra_new IN ROLE ra_parent,ra_missing", "42704", `role "ra_missing" does not exist`
						case "initial_cycle":
							query, code, message = "CREATE ROLE ra_new IN ROLE ra_parent ROLE ra_parent", "0LP01", `role "ra_new" is a member of role "ra_parent"`
						case "attribute":
							query, code, message = "ALTER ROLE ra_parent LOGIN CONNECTION LIMIT -2", "22023", "invalid connection limit: -2"
						case "date":
							query, code, message = "ALTER ROLE ra_parent LOGIN VALID UNTIL 'bad'", "22007", `invalid input syntax for type timestamp with time zone: "bad"`
						}
						if mode != "autocommit" {
							run(e, fixture.Exec("SAVEPOINT atomic_mark", 0))
						}
						roleTransactionStep(t, p, b, e, held, fixture.RoleReferenceError(query, code, message, detail, false, "execute"))
						if mode != "autocommit" {
							aborted := fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")
							run(e, fixture.ErrorDiagnostic("SELECT 1", aborted))
							stmt, err := e.PrepareContext(p.Ctx, "ALTER ROLE ra_empty LOGIN")
							if stmt != nil {
								stmt.Close()
								t.Fatal("aborted Prepare returned a statement")
							}
							fixture.AssertDiagnostic(t, err, &aborted)
							run(e, fixture.Exec("ROLLBACK TO atomic_mark", 0))
							run(e, fixture.Exec("RELEASE atomic_mark", 0))
						}
						run(e, roleTransactionExists("ra_empty", true))
						run(e, roleTransactionExists("ra_new", false))
						run(e, fixture.Select("SELECT rolcanlogin,rolconnlimit FROM pg_roles WHERE rolname='ra_parent'", []string{"rolcanlogin", "rolconnlimit"}, []string{"BOOL", "INT4"}, [][]any{{false, int64(-1)}}))
						run(e, fixture.Select("SELECT pg_has_role('ra_member','ra_parent','MEMBER') AS member", []string{"member"}, []string{"BOOL"}, [][]any{{kind == "revoke"}}))
						if kind == "drop_session" {
							run(e, fixture.Exec("RESET ROLE", 0))
						}
						finish(true)
						run(c, roleTransactionExists("ra_kept", true))
						run(c, fixture.Select("SELECT i FROM ra_sentinel ORDER BY i", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}))
					})
				})
			}
		}
	}
	roleTransactionAtomicReadOnly(t)
	roleTransactionAtomicPassword(t)
}

func roleTransactionAtomicReadOnly(t *testing.T) {
	t.Helper()
	for _, query := range []string{"CREATE ROLE ra_ro_new", "ALTER ROLE ra_ro LOGIN", "DROP ROLE ra_ro", "GRANT ra_ro TO ra_ro_member", "REVOKE ra_ro FROM ra_ro_member"} {
		t.Run("readonly/"+query, func(t *testing.T) {
			roleTransactionT2Cases(t, []string{"ra_ro", "ra_ro_member", "ra_ro_new"}, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				c, _ := roleTransactionT2Conn(t, p, b)
				for _, q := range []string{"CREATE ROLE ra_ro", "CREATE ROLE ra_ro_member"} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
				}
				for _, held := range []bool{false, true} {
					tx, err := c.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { tx.Rollback() })
					tag := map[string]string{"CREATE": "CREATE ROLE", "ALTER": "ALTER ROLE", "DROP": "DROP ROLE", "GRANT": "GRANT ROLE", "REVOKE": "REVOKE ROLE"}
					verb := query[:len("DROP")]
					for candidate := range tag {
						if len(query) >= len(candidate) && query[:len(candidate)] == candidate {
							verb = candidate
						}
					}
					roleTransactionStep(t, p, b, tx, held, fixture.ErrorDiagnostic(query, fixture.ControlsReferenceDiagnostic("25006", "cannot execute "+tag[verb]+" in a read-only transaction")))
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
				}
			})
		})
	}
}
