package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceUUIDOsspNameAuthority(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		label := "direct"
		if prepared {
			label = "fresh_prepared"
		}
		t.Run(label, func(t *testing.T) {
			uuidNameCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
				uuidOsspNamespaceInstall(t, p, c, r)
				run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, c, r, s) }
				run(fixture.Exec("CREATE USER uuid_ossp_owner", 0))
				defer func() {
					for _, q := range []string{"RESET SESSION AUTHORIZATION", "DROP SCHEMA IF EXISTS uuid_ossp_part CASCADE", "DROP OWNED BY uuid_ossp_owner", "DROP ROLE uuid_ossp_owner"} {
						if _, err := c.ExecContext(p.Ctx, q); err != nil {
							t.Error(q, err)
						}
					}
				}()
				run(fixture.Exec("GRANT USAGE ON SCHEMA uuid_ossp_part TO uuid_ossp_owner", 0))
				actor := func(body func()) { fixture.RoleGrantActor(run, "uuid_ossp_owner", body) }
				for _, f := range uuidNameFunctions {
					call := "uuid_ossp_part." + f.name + "('" + uuidNameDNS + "','www.example.com')"
					sig := "uuid_ossp_part." + f.name + "(uuid,text)"
					q := "SELECT " + call + " AS u"
					var h fixture.HeldStatement
					actor(func() { h = uuidNameHeld(t, p, c, q); run(uuidNameStep(q, f.example)) })
					defer h.Statement.Close()
					table := "uuid_ossp_part." + f.name + "_acl"
					view := table + "_view"
					run(fixture.Exec("CREATE TABLE "+table+"(id integer,u uuid DEFAULT "+call+")", 0))
					run(fixture.Exec("INSERT INTO "+table+"(id) VALUES(1)", 1))
					run(fixture.Exec("CREATE VIEW "+view+" AS SELECT "+call+" AS u", 0))
					run(fixture.Exec("GRANT SELECT,INSERT ON "+table+" TO uuid_ossp_owner", 0))
					run(fixture.Exec("GRANT SELECT ON "+view+" TO uuid_ossp_owner", 0))
					run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+sig+" FROM PUBLIC", 0))
					denied := func(query string) fixture.Step {
						s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + f.name})
						s.SelectRows = true
						s.ErrorStage = "execute"
						return s
					}
					for range 2 {
						actor(func() {
							// The pinned oracle confirms execution-stage denial even
							// for these immutable constant calls and fresh preparations.
							for _, query := range []string{q, q + " WHERE FALSE", "SELECT u FROM " + view} {
								t.Run(f.name+"/denied/"+query, func(t *testing.T) {
									s := denied(query)
									s.Prepared = prepared
									fixture.RunStep(t, p.Ctx, c, r, s)
								})
							}
							t.Run(f.name+"/held_denied", func(t *testing.T) { fixture.RunStep(t, p.Ctx, h, r, denied(q)) })
							t.Run(f.name+"/atomic_default", func(t *testing.T) {
								s := denied("INSERT INTO " + table + "(id,u) VALUES(2,'" + f.widgets + "'),(3,DEFAULT) RETURNING u")
								s.Prepared = prepared
								fixture.RunStep(t, p.Ctx, c, r, s)
							})
							run(fixture.Select("SELECT id FROM "+table+" ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
							run(uuidNameStep("SELECT CASE WHEN FALSE THEN "+call+" ELSE NULL END AS u", nil))
						})
						run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+sig+" TO uuid_ossp_owner", 0))
						actor(func() { run(uuidNameStep(q, f.example)); fixture.RunStep(t, p.Ctx, h, r, uuidNameStep(q, f.example)) })
						run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+sig+" FROM uuid_ossp_owner", 0))
					}
					run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+sig+" TO uuid_ossp_owner", 0))
					run(fixture.Exec("REVOKE USAGE ON SCHEMA uuid_ossp_part FROM uuid_ossp_owner", 0))
					actor(func() {
						uuidOsspFailure(t, p, c, q, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for schema uuid_ossp_part", Position: "8"})
					})
					run(fixture.Exec("GRANT USAGE ON SCHEMA uuid_ossp_part TO uuid_ossp_owner", 0))
				}
			})
		})
	}
}
