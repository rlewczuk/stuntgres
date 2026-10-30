package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceHstoreJSONPrivileges(t *testing.T) {
	for _, name := range []string{"hstore_to_json", "hstore_to_json_loose"} {
		t.Run(name, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"hs_json_actor"})
			hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				hstoreInstall(t, p, b, c)
				for _, q := range []string{"CREATE ROLE hs_json_actor", "GRANT USAGE ON SCHEMA public TO hs_json_actor", "CREATE TABLE hs_json_acl(id integer,v hstore)", "GRANT SELECT ON hs_json_acl TO hs_json_actor"} {
					hstoreRun(t, p, b, c, fixture.Exec(q, 0))
				}
				hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_json_acl VALUES (1,'a=>1'),(2,NULL)", 2))
				hstoreRun(t, p, b, c, fixture.Exec("SET ROLE hs_json_actor", 0))
				defer func() {
					if _, err := c.ExecContext(p.Ctx, "RESET ROLE"); err != nil {
						t.Error(err)
					}
				}()
				var held []fixture.HeldStatement
				for _, id := range []string{"1", "2"} {
					q := "SELECT " + name + "(v) AS j FROM hs_json_acl WHERE id=" + id
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held = append(held, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q})
				}
				value := `{"a": "1"}`
				if name == "hstore_to_json_loose" {
					value = `{"a": 1}`
				}
				for i, h := range held {
					var want any = value
					if i == 1 {
						want = nil
					}
					hstoreRun(t, p, b, h, hstoreJSONSelect(b, h.SQL, []string{"j"}, [][]any{{want}}, false))
				}
				hstoreRun(t, p, b, c, fixture.Exec("RESET ROLE", 0))
				hstoreRun(t, p, b, c, fixture.Exec("REVOKE EXECUTE ON FUNCTION "+name+"(hstore) FROM PUBLIC,hs_json_actor", 0))
				hstoreRun(t, p, b, c, fixture.Exec("SET ROLE hs_json_actor", 0))
				denied := fixture.ControlsReferenceDiagnostic("42501", "permission denied for function "+name)
				for _, h := range held {
					hstoreError(t, p, b, c, h.SQL, false, true, false, denied)
					hstoreError(t, p, b, c, h.SQL, true, true, false, denied)
					hstoreError(t, p, b, h, h.SQL, false, true, false, denied)
				}
				q := "SELECT " + name + "(NULL::hstore) AS j"
				s := hstoreJSONSelect(b, q, []string{"j"}, [][]any{{nil}}, false)
				hstoreRun(t, p, b, c, s)
				hstoreRun(t, p, b, c, fixture.Prepared(s))
				hstoreRun(t, p, b, c, fixture.Exec("RESET ROLE", 0))
				hstoreRun(t, p, b, c, fixture.Exec("GRANT EXECUTE ON FUNCTION "+name+"(hstore) TO hs_json_actor", 0))
				hstoreRun(t, p, b, c, fixture.Exec("SET ROLE hs_json_actor", 0))
				for i, h := range held {
					var want any = value
					if i == 1 {
						want = nil
					}
					hstoreRun(t, p, b, h, hstoreJSONSelect(b, h.SQL, []string{"j"}, [][]any{{want}}, false))
				}
				hstoreRun(t, p, b, c, fixture.Exec("RESET ROLE", 0))
			})
		})
	}
}
