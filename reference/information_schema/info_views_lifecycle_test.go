package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoViewsDefinitionsLifecycle(t *testing.T) {
	ivScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{"CREATE SCHEMA iv_moved", "CREATE TABLE iv_life_base(id integer,note text)", "CREATE VIEW iv_life AS SELECT id,note FROM iv_life_base"} {
			run(fixture.Exec(q, 0))
		}
		held := ivHeld(t, p, b, c, ivSelect)
		check := func(definition, option, update, insert string) {
			s := ivStep(b.PG, ivSelect, [][]any{ivRow(database, "public", "iv_life", definition, option, update, insert)}, "public", "iv_life")
			run(s)
			run(fixture.Prepared(s))
			held(s)
		}
		definition := " SELECT id,\n    note\n   FROM iv_life_base;"
		check(definition, "NONE", "YES", "YES")
		for _, option := range []string{"local", "cascaded"} {
			run(fixture.Exec("ALTER VIEW iv_life SET (check_option="+option+")", 0))
			want := "LOCAL"
			if option == "cascaded" {
				want = "CASCADED"
			}
			check(definition, want, "YES", "YES")
		}
		run(fixture.Exec("ALTER VIEW iv_life RESET (check_option)", 0))
		check(definition, "NONE", "YES", "YES")
		run(fixture.Exec("ALTER TABLE iv_life_base RENAME COLUMN id TO renamed_id", 0))
		definition = " SELECT renamed_id AS id,\n    note\n   FROM iv_life_base;"
		check(definition, "NONE", "YES", "YES")
		run(fixture.Exec("ALTER TABLE iv_life_base RENAME TO iv_life_renamed", 0))
		definition = " SELECT renamed_id AS id,\n    note\n   FROM iv_life_renamed;"
		check(definition, "NONE", "YES", "YES")
		run(fixture.Exec("ALTER TABLE iv_life_renamed SET SCHEMA iv_moved", 0))
		definition = " SELECT renamed_id AS id,\n    note\n   FROM iv_moved.iv_life_renamed;"
		check(definition, "NONE", "YES", "YES")
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError("CREATE OR REPLACE VIEW iv_life AS SELECT renamed_id::bigint AS id,note FROM iv_moved.iv_life_renamed", fixture.Diagnostic{Code: "42P16", Message: `cannot change data type of view column "id" from integer to bigint`})
			s.Prepared, s.ErrorStage = prepared, "execute"
			run(s)
			check(definition, "NONE", "YES", "YES")
		}
		run(fixture.Exec("CREATE OR REPLACE VIEW iv_life AS SELECT renamed_id+1 AS id,note FROM iv_moved.iv_life_renamed", 0))
		check(" SELECT (renamed_id + 1) AS id,\n    note\n   FROM iv_moved.iv_life_renamed;", "NONE", "YES", "YES")
		run(fixture.Exec("DROP VIEW iv_life", 0))
		held(ivStep(b.PG, ivSelect, nil, "public", "iv_life"))
		run(fixture.Exec("CREATE VIEW iv_life AS SELECT 1 AS id,'fixed'::text AS note", 0))
		check(" SELECT 1 AS id,\n    'fixed'::text AS note;", "NONE", "NO", "NO")
		run(fixture.Exec("CREATE OR REPLACE VIEW iv_life AS SELECT renamed_id AS id,note FROM iv_moved.iv_life_renamed", 0))
		run(fixture.Exec("DROP TABLE iv_moved.iv_life_renamed CASCADE", 0))
		held(ivStep(b.PG, ivSelect, nil, "public", "iv_life"))
		// Routine identities and repeated references belong in the definition,
		// even though dependency usage relations deduplicate object addresses.
		for _, q := range []string{
			"CREATE TABLE iv_refs_base(id integer)",
			"CREATE FUNCTION iv_refs_f() RETURNS integer BEGIN ATOMIC SELECT 7; END",
			"CREATE VIEW iv_refs AS SELECT a.id,iv_refs_f() AS n,b.id AS again FROM iv_refs_base a JOIN iv_refs_base b ON a.id=b.id",
		} {
			run(fixture.Exec(q, 0))
		}
		refs := func(def string) {
			s := ivStep(b.PG, ivSelect, [][]any{ivRow(database, "public", "iv_refs", def, "NONE", "NO", "NO")}, "public", "iv_refs")
			run(s)
			run(fixture.Prepared(s))
		}
		refs(" SELECT a.id,\n    iv_refs_f() AS n,\n    b.id AS again\n   FROM (iv_refs_base a\n     JOIN iv_refs_base b ON ((a.id = b.id)));")
		run(fixture.Exec("CREATE FUNCTION iv_refs_g() RETURNS integer BEGIN ATOMIC SELECT 7; END", 0))
		run(fixture.Exec("CREATE OR REPLACE VIEW iv_refs AS SELECT a.id,iv_refs_g() AS n,b.id AS again FROM iv_refs_base a JOIN iv_refs_base b ON a.id=b.id", 0))
		refs(" SELECT a.id,\n    iv_refs_g() AS n,\n    b.id AS again\n   FROM (iv_refs_base a\n     JOIN iv_refs_base b ON ((a.id = b.id)));")
	})
}

func TestReferenceInfoViewsNoExecution(t *testing.T) {
	ivScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{"CREATE TABLE iv_noexec_base(id integer)", "INSERT INTO iv_noexec_base VALUES(0)", "CREATE SEQUENCE iv_noexec_seq", "CREATE VIEW iv_noexec AS SELECT (10/id) AS quotient,nextval('iv_noexec_seq') AS tick FROM iv_noexec_base"} {
			count := int64(0)
			if q == "INSERT INTO iv_noexec_base VALUES(0)" {
				count = 1
			}
			run(fixture.Exec(q, count))
		}
		s := ivStep(b.PG, ivSelect, [][]any{ivRow(database, "public", "iv_noexec", " SELECT (10 / id) AS quotient,\n    nextval('iv_noexec_seq'::regclass) AS tick\n   FROM iv_noexec_base;", "NONE", "NO", "NO")}, "public", "iv_noexec")
		run(s)
		run(fixture.Prepared(s))
		ivHeld(t, p, b, c, ivSelect)(s)
		run(fixture.Select("SELECT last_value,is_called FROM iv_noexec_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError("SELECT quotient FROM iv_noexec", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			run(s)
		}
		run(fixture.Select("SELECT last_value,is_called FROM iv_noexec_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
	})
}
