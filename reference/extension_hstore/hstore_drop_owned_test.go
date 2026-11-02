package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// DROP OWNED deletes a role's extensions newest first, so the older
// extension's dependents are reported first.
func TestReferenceHstoreDropOwnedExtensions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"hs_owned_ext"})
	hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		var notices []fixture.Diagnostic
		_, owner, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		run := func(s fixture.Step) { t.Helper(); fixture.RunStep(t, p.Ctx, owner, recorder, s) }
		var database string
		if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
			t.Fatal(err)
		}
		for _, q := range []string{
			"CREATE ROLE hs_owned_ext",
			"GRANT CREATE ON DATABASE " + `"` + strings.ReplaceAll(database, `"`, `""`) + `"` + " TO hs_owned_ext",
			"GRANT USAGE, CREATE ON SCHEMA public TO hs_owned_ext",
			"SET ROLE hs_owned_ext",
			"CREATE EXTENSION hstore",
			"CREATE EXTENSION citext",
			"RESET ROLE",
			"CREATE TABLE hs_owned_t(v hstore, c citext)",
			"CREATE VIEW hs_owned_vh AS SELECT v FROM hs_owned_t",
			"CREATE VIEW hs_owned_vc AS SELECT c FROM hs_owned_t",
		} {
			run(fixture.Exec(q, 0))
		}
		want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop desired object(s) because other objects depend on them")
		want.Detail = "column v of table hs_owned_t depends on type hstore\nview hs_owned_vh depends on column v of table hs_owned_t\ncolumn c of table hs_owned_t depends on type citext\nview hs_owned_vc depends on column c of table hs_owned_t"
		want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("DROP OWNED BY hs_owned_ext", want)
			s.Prepared, s.ErrorStage = prepared, "execute"
			run(s)
			hstoreDropCascaded(t, &notices)
		}
		run(fixture.Select(`SELECT count(*) AS n FROM pg_extension WHERE extname IN ('hstore','citext')`, []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
		run(fixture.Exec("DROP OWNED BY hs_owned_ext CASCADE", 0))
		hstoreDropCascaded(t, &notices, "column v of table hs_owned_t", "view hs_owned_vh", "column c of table hs_owned_t", "view hs_owned_vc")
		run(fixture.Select(`SELECT count(*) AS n FROM pg_extension WHERE extname IN ('hstore','citext')`, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Exec("DROP TABLE hs_owned_t", 0))
		run(fixture.Exec("DROP ROLE hs_owned_ext", 0))
	})
}
