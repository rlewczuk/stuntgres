package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceHstoreConstructPrivileges(t *testing.T) {
	for _, tc := range []struct {
		label, signature, name, expression, nullExpression, value string
		strict                                                    bool
	}{
		{"text", "hstore(text,text)", "hstore", "hstore(k,w)::text", "hstore('a',NULL::text)::text", `"a"=>"1"`, false},
		{"paired", "hstore(text[],text[])", "hstore", "hstore(ARRAY[k],ARRAY[w])::text", "hstore(ARRAY['a'],NULL::text[])::text", `"a"=>"1"`, false},
		{"single", "hstore(text[])", "hstore", "hstore(a)::text", "hstore(NULL::text[])::text", `"a"=>"1"`, true},
		{"fetch", "fetchval(hstore,text)", "fetchval", "fetchval(v,k)", "fetchval(NULL::hstore,'a')", "1", true},
		{"arrow", "fetchval(hstore,text)", "fetchval", "v->k", "NULL::hstore->'a'", "1", true},
		{"slice", "slice_array(hstore,text[])", "slice_array", "slice_array(v,ARRAY[k])::text", "slice_array(NULL::hstore,ARRAY['a'])::text", "{1}", true},
		{"array_arrow", "slice_array(hstore,text[])", "slice_array", "(v->ARRAY[k])::text", "(NULL::hstore->ARRAY['a'])::text", "{1}", true},
		{"exist", "exist(hstore,text)", "exist", "exist(v,k)::text", "exist(NULL::hstore,'a')::text", "true", true},
		{"defined", "defined(hstore,text)", "defined", "defined(v,k)::text", "defined(NULL::hstore,'a')::text", "true", true},
	} {
		t.Run(tc.label, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"hs_construct_actor"})
			hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				hstoreInstall(t, p, b, c)
				for _, q := range []string{"CREATE ROLE hs_construct_actor", "GRANT USAGE ON SCHEMA public TO hs_construct_actor", "CREATE TABLE hs_construct_acl(id integer,k text,w text,a text[],v hstore)", "GRANT SELECT ON hs_construct_acl TO hs_construct_actor"} {
					hstoreRun(t, p, b, c, fixture.Exec(q, 0))
				}
				hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_construct_acl VALUES (1,'a','1',ARRAY['a','1'],'a=>1'),(2,NULL,NULL,NULL,NULL)", 2))
				q := "SELECT " + tc.expression + " AS v FROM hs_construct_acl WHERE id=1"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				s := fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.value}})
				hstoreRun(t, p, b, held, s)
				hstoreRun(t, p, b, c, fixture.Exec("REVOKE EXECUTE ON FUNCTION "+tc.signature+" FROM PUBLIC,hs_construct_actor", 0))
				hstoreRun(t, p, b, c, fixture.Exec("SET ROLE hs_construct_actor", 0))
				want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for function "+tc.name)
				for _, id := range []string{"1", "2"} {
					query := "SELECT " + tc.expression + " AS v FROM hs_construct_acl WHERE id=" + id
					for _, prepared := range []bool{false, true} {
						hstoreError(t, p, b, c, query, prepared, true, false, want)
					}
				}
				hstoreError(t, p, b, held, q, false, true, false, want)
				if tc.strict {
					hstoreRun(t, p, b, c, fixture.Select("SELECT "+tc.nullExpression+" AS v", []string{"v"}, []string{"TEXT"}, [][]any{{nil}}))
				} else {
					hstoreError(t, p, b, c, "SELECT "+tc.nullExpression+" AS v", false, true, false, want)
				}
				hstoreRun(t, p, b, c, fixture.Exec("RESET ROLE", 0))
				hstoreRun(t, p, b, c, fixture.Exec("GRANT EXECUTE ON FUNCTION "+tc.signature+" TO hs_construct_actor", 0))
				hstoreRun(t, p, b, c, fixture.Exec("SET ROLE hs_construct_actor", 0))
				hstoreRun(t, p, b, c, s)
				hstoreRun(t, p, b, c, fixture.Prepared(s))
				hstoreRun(t, p, b, held, s)
				hstoreRun(t, p, b, c, fixture.Exec("RESET ROLE", 0))
			})
		})
	}
}
