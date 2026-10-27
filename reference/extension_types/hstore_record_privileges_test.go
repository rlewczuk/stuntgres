package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceHstoreRecordPrivileges(t *testing.T) {
	for _, tc := range []struct{ label, signature, name, expression, nullExpression, value string }{
		{"constructor", "hstore(record)", "hstore", "hstore(r)::text", "hstore(NULL::hs_record_pair)::text", `"i"=>"7", "n"=>"keep"`},
		{"function", "populate_record(anyelement,hstore)", "populate_record", "populate_record(r,h)::text", "populate_record(NULL::hs_record_pair,NULL::hstore)::text", "(42,keep)"},
		{"operator", "populate_record(anyelement,hstore)", "populate_record", "(r #= h)::text", "(NULL::hs_record_pair #= NULL::hstore)::text", "(42,keep)"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"hs_record_actor"})
			hstoreRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				hstoreRecordSetup(t, p, b, c)
				for _, q := range []string{
					"CREATE ROLE hs_record_actor",
					"GRANT USAGE ON SCHEMA public TO hs_record_actor",
					"CREATE TABLE hs_record_acl(id integer,r hs_record_pair,h hstore)",
					"GRANT SELECT ON hs_record_acl TO hs_record_actor",
				} {
					hstoreRun(t, p, b, c, fixture.Exec(q, 0))
				}
				hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_record_acl VALUES (1,'(7,keep)','i=>42'),(2,NULL,NULL)", 2))
				q := "SELECT " + tc.expression + " AS v FROM hs_record_acl WHERE id=1"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				hstoreRun(t, p, b, held, hstoreRecordText(q, tc.value))
				hstoreRun(t, p, b, c, fixture.Exec("REVOKE EXECUTE ON FUNCTION "+tc.signature+" FROM PUBLIC,hs_record_actor", 0))
				hstoreRun(t, p, b, c, fixture.Exec("SET ROLE hs_record_actor", 0))
				defer c.ExecContext(p.Ctx, "RESET ROLE")
				want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for function "+tc.name)
				for _, id := range []string{"1", "2"} {
					fixture.CompositeReferenceError(t, p, c, "SELECT "+tc.expression+" AS v FROM hs_record_acl WHERE id="+id, "execute", want)
				}
				hstoreError(t, p, b, held, q, false, true, false, want)
				fixture.CompositeReferenceError(t, p, c, "SELECT "+tc.nullExpression+" AS v", "execute", want)
				hstoreRun(t, p, b, c, fixture.Exec("RESET ROLE", 0))
				hstoreRun(t, p, b, c, fixture.Exec("GRANT EXECUTE ON FUNCTION "+tc.signature+" TO hs_record_actor", 0))
				hstoreRun(t, p, b, c, fixture.Exec("SET ROLE hs_record_actor", 0))
				hstoreRecordBoth(t, p, b, c, hstoreRecordText(q, tc.value))
				hstoreRun(t, p, b, held, hstoreRecordText(q, tc.value))
				hstoreRun(t, p, b, c, fixture.Exec("RESET ROLE", 0))
			})
		})
	}
}
