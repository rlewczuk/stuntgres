package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceHstoreArraysPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"hs_arrays_actor"})
	hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreInstall(t, p, b, c)
		for _, q := range []string{"CREATE ROLE hs_arrays_actor", "GRANT USAGE ON SCHEMA public TO hs_arrays_actor", "CREATE TABLE hs_arrays_acl(id integer,v hstore)", "GRANT SELECT ON hs_arrays_acl TO hs_arrays_actor"} {
			hstoreRun(t, p, b, c, fixture.Exec(q, 0))
		}
		hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_arrays_acl VALUES (1,'a=>1'),(2,NULL)", 2))
		query := hstoreArraysQuery("v") + " FROM hs_arrays_acl WHERE id=1"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
		hstoreRun(t, p, b, c, fixture.Exec("SET ROLE hs_arrays_actor", 0))
		hstoreRun(t, p, b, held, hstoreArraysSelect(b, query, [][]any{{"{a,1}", "{{a,1}}", "{a,1}", "{{a,1}}"}}))
		hstoreRun(t, p, b, c, fixture.Exec("RESET ROLE", 0))
		for _, name := range []string{"hstore_to_array", "hstore_to_matrix"} {
			hstoreRun(t, p, b, c, fixture.Exec("REVOKE EXECUTE ON FUNCTION "+name+"(hstore) FROM PUBLIC,hs_arrays_actor", 0))
		}
		hstoreRun(t, p, b, c, fixture.Exec("SET ROLE hs_arrays_actor", 0))
		for _, tc := range []struct{ form, name string }{{"hstore_to_array(v)", "hstore_to_array"}, {"%% v", "hstore_to_array"}, {"hstore_to_matrix(v)", "hstore_to_matrix"}, {"%# v", "hstore_to_matrix"}} {
			want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for function "+tc.name)
			for _, id := range []string{"1", "2"} {
				hstoreError(t, p, b, c, "SELECT "+tc.form+" FROM hs_arrays_acl WHERE id="+id, false, true, false, want)
			}
		}
		hstoreError(t, p, b, held, query, false, true, false, fixture.ControlsReferenceDiagnostic("42501", "permission denied for function hstore_to_array"))
		hstoreRun(t, p, b, c, hstoreArraysSelect(b, hstoreArraysQuery("NULL::hstore"), [][]any{{nil, nil, nil, nil}}))
		hstoreRun(t, p, b, c, fixture.Exec("RESET ROLE", 0))
		for _, name := range []string{"hstore_to_array", "hstore_to_matrix"} {
			hstoreRun(t, p, b, c, fixture.Exec("GRANT EXECUTE ON FUNCTION "+name+"(hstore) TO hs_arrays_actor", 0))
		}
		hstoreRun(t, p, b, c, fixture.Exec("SET ROLE hs_arrays_actor", 0))
		hstoreRun(t, p, b, held, hstoreArraysSelect(b, query, [][]any{{"{a,1}", "{{a,1}}", "{a,1}", "{{a,1}}"}}))
		hstoreRun(t, p, b, c, fixture.Exec("RESET ROLE", 0))
	})
}
