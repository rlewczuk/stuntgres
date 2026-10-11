package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGTableViewsDefinitions(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "pgtv_definition")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			for _, q := range []string{"CREATE SCHEMA pgtv_definition", "CREATE TABLE pgtv_base(n INT, label TEXT)", "CREATE TABLE pgtv_other(n INT)", "CREATE VIEW pgtv_star AS SELECT * FROM pgtv_base", "CREATE VIEW pgtv_join(k,txt) AS SELECT a.n,a.label FROM pgtv_star a JOIN pgtv_other b ON a.n=b.n", "CREATE VIEW pgtv_nested AS SELECT k,txt FROM pgtv_join", "CREATE VIEW pgtv_zero AS SELECT FROM pgtv_base", "CREATE SEQUENCE pgtv_sequence", "CREATE VIEW pgtv_volatile AS SELECT nextval('pgtv_sequence') AS n FROM pgtv_base", `CREATE TABLE "pgtv Quoted"("Odd Column" INT)`, `CREATE VIEW "pgtv Quoted View" AS SELECT "Odd Column" FROM "pgtv Quoted"`} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO pgtv_base VALUES(1,'a'),(2,'b'),(2,'b')", 3))
			run(fixture.Exec("INSERT INTO pgtv_other VALUES(2)", 1))
			run(fixture.Exec(`INSERT INTO "pgtv Quoted" VALUES(7)`, 1))
			for _, q := range []string{"ALTER TABLE pgtv_base ADD COLUMN extra BOOL", "ALTER TABLE pgtv_base RENAME COLUMN n TO renamed_n", "ALTER TABLE pgtv_base RENAME TO pgtv_base_renamed", "ALTER TABLE pgtv_base_renamed SET SCHEMA pgtv_definition", "ALTER VIEW pgtv_join RENAME COLUMN txt TO renamed_txt"} {
				run(fixture.Exec(q, 0))
			}
			// Frozen source-column alias lists may retain creation-time labels.
			// Current relation/schema identity and actual replay below, not a
			// demanded source-column spelling, establish rename correctness.
			pgViewSummary(t, p, b, b.DB, "public", "pgtv_star", pgTableViewsOwner(b), "pgtv_definition", "pgtv_base_renamed")
			pgTableCheck(t, p, b, b.DB, "pgtv_sequence", nil)
			pgViewSummary(t, p, b, b.DB, "public", "pgtv_join", pgTableViewsOwner(b), "pgtv_star", "pgtv_other", "renamed_txt")
			for _, c := range []struct {
				name, copy, query string
				columns, types    []string
				rows              [][]any
			}{
				{"pgtv_star", "pgtv_star_copy", "SELECT * FROM pgtv_star_copy ORDER BY n", []string{"n", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), "b"}, {int64(2), "b"}}},
				{"pgtv_nested", "pgtv_nested_copy", "SELECT * FROM pgtv_nested_copy ORDER BY k", []string{"k", "txt"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "b"}, {int64(2), "b"}}},
				{"pgtv_zero", "pgtv_zero_copy", "SELECT * FROM pgtv_zero_copy", []string{}, []string{}, [][]any{{}, {}, {}}},
				{"pgtv Quoted View", "pgtv_quoted_copy", "SELECT * FROM pgtv_quoted_copy", []string{"Odd Column"}, []string{"INT4"}, [][]any{{int64(7)}}},
			} {
				fixture.ViewCopySQL(t, p, b, c.copy, "SELECT definition FROM pg_catalog.pg_views WHERE schemaname='public' AND viewname=$1", c.name)
				s := fixture.Select(c.query, c.columns, c.types, c.rows)
				run(s)
				run(fixture.Prepared(s))
			}
			run(fixture.Exec("INSERT INTO pgtv_definition.pgtv_base_renamed(renamed_n,label) VALUES(2,'new')", 1))
			run(fixture.Select("SELECT * FROM pgtv_nested_copy ORDER BY txt", []string{"k", "txt"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "b"}, {int64(2), "b"}, {int64(2), "new"}}))
			pgViewSummary(t, p, b, b.DB, "public", "pgtv_volatile", pgTableViewsOwner(b), "nextval", "pgtv_sequence")
			run(fixture.Select("SELECT last_value,is_called FROM pgtv_sequence", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
			run(fixture.Exec("CREATE OR REPLACE VIEW pgtv_star AS SELECT renamed_n+10 AS n,label FROM pgtv_definition.pgtv_base_renamed", 0))
			pgViewSummary(t, p, b, b.DB, "public", "pgtv_star", pgTableViewsOwner(b), "renamed_n", "+ 10")
			fixture.ViewCopy(t, p, b, "pgtv_star", "pgtv_replaced_copy")
			run(fixture.Select("SELECT n FROM pgtv_replaced_copy ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(11)}, {int64(12)}, {int64(12)}, {int64(12)}}))
			run(fixture.Exec("CREATE VIEW pgtv_cycle_a AS SELECT n FROM pgtv_other", 0))
			run(fixture.Exec("CREATE VIEW pgtv_cycle_b AS SELECT n FROM pgtv_cycle_a", 0))
			run(fixture.Exec("CREATE OR REPLACE VIEW pgtv_cycle_a AS SELECT n FROM pgtv_cycle_b", 0))
			pgViewSummary(t, p, b, b.DB, "public", "pgtv_cycle_a", pgTableViewsOwner(b), "pgtv_cycle_b")
		})
	}
}
