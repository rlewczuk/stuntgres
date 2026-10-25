package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// One session repeats identical pg_type and pg_proc probes across ownership,
// ACL, rename, transactional and routine rename and drop. Every probe must see the
// catalog as of its own statement: direct, prepared and a statement prepared
// before any change.
func TestReferencePGTypeProcRepeatedProbes(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"rp_owner", "rp_reader"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, q := range []string{"CREATE ROLE rp_owner", "CREATE ROLE rp_reader", "CREATE DOMAIN rp_dom AS integer", "CREATE FUNCTION rp_f() RETURNS integer LANGUAGE sql AS 'SELECT 1'"} {
				run(fixture.Exec(q, 0))
			}
			var typ, proc int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'rp_dom'::regtype::oid,'rp_f()'::regprocedure::oid").Scan(&typ, &proc); err != nil {
				t.Fatal(err)
			}
			typeQuery := "SELECT t.typname,t.typowner='rp_owner'::regrole::oid AS owned,t.typacl IS NULL AS default_acl FROM pg_type t WHERE t.oid=$1::oid"
			grantQuery := "SELECT pg_get_userbyid(a.grantee) AS grantee FROM pg_type t CROSS JOIN LATERAL aclexplode(t.typacl) a WHERE t.oid=$1::oid AND a.grantee<>0 ORDER BY 1"
			procQuery := "SELECT proname,prosrc FROM pg_proc WHERE oid=$1::oid"
			held := map[string]fixture.HeldStatement{}
			for _, q := range []string{typeQuery, grantQuery, procQuery} {
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held[q] = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			}
			probe := func(q string, columns, types []string, want [][]any, arg int64) {
				t.Helper()
				s := fixture.Select(q, columns, types, want, arg)
				run(s)
				run(fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, held[q], b.Recorder, s)
			}
			typeRow := func(want ...any) {
				t.Helper()
				rows := [][]any{}
				if len(want) != 0 {
					rows = [][]any{want}
				}
				probe(typeQuery, []string{"typname", "owned", "default_acl"}, []string{"NAME", "BOOL", "BOOL"}, rows, typ)
			}
			grantees := func(names ...string) {
				t.Helper()
				rows := [][]any{}
				for _, name := range names {
					rows = append(rows, []any{name})
				}
				probe(grantQuery, []string{"grantee"}, []string{"NAME"}, rows, typ)
			}
			procRow := func(want ...any) {
				t.Helper()
				rows := [][]any{}
				if len(want) != 0 {
					rows = [][]any{want}
				}
				probe(procQuery, []string{"proname", "prosrc"}, []string{"NAME", "TEXT"}, rows, proc)
			}

			typeRow("rp_dom", false, true)
			grantees()
			run(fixture.Exec("ALTER DOMAIN rp_dom OWNER TO rp_owner", 0))
			typeRow("rp_dom", true, true)
			run(fixture.Exec("GRANT USAGE ON DOMAIN rp_dom TO rp_reader", 0))
			typeRow("rp_dom", true, false)
			grantees("rp_owner", "rp_reader")
			run(fixture.Exec("REVOKE USAGE ON DOMAIN rp_dom FROM rp_reader", 0))
			typeRow("rp_dom", true, false)
			grantees("rp_owner")
			run(fixture.Exec("ALTER DOMAIN rp_dom RENAME TO rp_renamed", 0))
			typeRow("rp_renamed", true, false)

			procRow("rp_f", "SELECT 1")
			run(fixture.Exec("ALTER FUNCTION rp_f() RENAME TO rp_g", 0))
			procRow("rp_g", "SELECT 1")

			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("DROP DOMAIN rp_renamed", 0))
			run(fixture.Exec("DROP FUNCTION rp_g()", 0))
			typeRow()
			procRow()
			run(fixture.Exec("ROLLBACK", 0))
			typeRow("rp_renamed", true, false)
			procRow("rp_g", "SELECT 1")

			run(fixture.Exec("DROP FUNCTION rp_g()", 0))
			procRow()
			run(fixture.Exec("DROP DOMAIN rp_renamed", 0))
			typeRow()
		})
	}
}
