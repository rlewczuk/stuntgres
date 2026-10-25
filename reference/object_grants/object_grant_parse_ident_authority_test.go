package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantParseIdentAuthority(t *testing.T) {
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
		run(fixture.Exec("CREATE TABLE og_parse_ident_source(id INT,s TEXT,strict_mode BOOLEAN)", 0))
		run(fixture.Exec(`INSERT INTO og_parse_ident_source VALUES(1,'"SomeSchema".someTable',true),(2,NULL,true),(3,'"',NULL),(4,'Foo.Bar[]',false)`, 4))
		run(fixture.Exec("GRANT SELECT ON og_parse_ident_source TO og_object_reader", 0))
		run(fixture.Exec("CREATE VIEW og_parse_ident_view AS SELECT parse_ident(strict=>strict_mode,str=>s) AS v FROM og_parse_ident_source WHERE id=1", 0))
		run(fixture.Exec("GRANT SELECT ON og_parse_ident_view TO og_object_reader", 0))

		// Quoted case is retained; unquoted ASCII uppercase folds. These
		// independently specified arrays exercise the existing identifier parser.
		const identifiers = `{SomeSchema,sometable}`
		selectValue := func(expr, tail string, want any, args ...any) fixture.Step {
			return fixture.ArrayReferenceSelect("SELECT "+expr+" AS v FROM og_parse_ident_source"+tail, []string{"v"}, []string{"_TEXT"}, []string{"_TEXT"}, [][]any{{want}}, b.PG, args...)
		}
		cases := []objectGrantProviderCase{
			// There is one full (text,boolean) declaration. The sole default
			// supplies strict=true; str is required, not another overload.
			{selectValue("parse_ident(s)", " WHERE id=1", identifiers), true, true},
			{selectValue("parse_ident(s,strict_mode)", " WHERE id=1", identifiers), true, true},
			{selectValue("pg_catalog.parse_ident(s,strict_mode)", " WHERE id=1", identifiers), true, true},
			{selectValue("parse_ident(str=>s)", " WHERE id=1", identifiers), true, true},
			{selectValue("parse_ident(s,strict=>strict_mode)", " WHERE id=1", identifiers), true, true},
			{selectValue("parse_ident(str=>s,strict=>strict_mode)", " WHERE id=1", identifiers), true, true},
			{selectValue("parse_ident(strict=>strict_mode,str=>s)", " WHERE id=1", identifiers), true, true},
			{fixture.ArrayReferenceSelect("SELECT v FROM og_parse_ident_view", []string{"v"}, []string{"_TEXT"}, []string{"_TEXT"}, [][]any{{identifiers}}, b.PG), true, true},
			{selectValue("parse_ident(CASE WHEN id=1 THEN $1::TEXT ELSE s END,strict_mode)", " WHERE id=1", identifiers, `"SomeSchema".someTable`), true, true},
			{selectValue("parse_ident(strict=>strict_mode,str=>s)", " WHERE id=4", `{foo,bar}`), true, true},

			// Runtime NULL still demands initialization authority, including a
			// NULL strict flag that suppresses parsing malformed source text.
			{selectValue("parse_ident(s)", " WHERE id=2", nil), true, true},
			{selectValue("parse_ident(s,strict_mode)", " WHERE id=3", nil), true, true},
			{selectValue("parse_ident(NULL::TEXT)", " WHERE id=1", nil), false, false},
			{selectValue("parse_ident(NULL::TEXT,true)", " WHERE id=1", nil), false, false},
			{selectValue(`parse_ident('"',NULL::BOOLEAN)`, " WHERE id=1", nil), false, false},
			{selectValue("parse_ident(NULL::TEXT,NULL::BOOLEAN)", " WHERE id=1", nil), false, false},
			{selectValue("parse_ident($1::TEXT,$2::BOOLEAN)", " WHERE id=1", nil, nil, true), false, false},
			{selectValue("parse_ident($1::TEXT,$2::BOOLEAN)", " WHERE id=1", nil, `"`, nil), false, false},

			// Immutable constant-only held plans may fold. Fresh constants
			// witness current authority; row-backed SAME-held cases above do not fold.
			{selectValue(`parse_ident('"SomeSchema".someTable')`, " WHERE id=1", identifiers), true, false},
			{selectValue("parse_ident($1::TEXT,$2::BOOLEAN)", " WHERE id=1", identifiers, `"SomeSchema".someTable`, true), true, false},
			{selectValue("CASE WHEN FALSE THEN parse_ident(s,strict_mode) ELSE NULL::TEXT[] END", " WHERE id=1", nil), false, true},
			{fixture.ArrayReferenceSelect("SELECT parse_ident(s,strict_mode) AS v FROM og_parse_ident_source WHERE id>99", []string{"v"}, []string{"_TEXT"}, []string{"_TEXT"}, nil, b.PG), true, true},
			{fixture.ArrayReferenceSelect("SELECT parse_ident(s,strict_mode) AS v FROM og_parse_ident_source WHERE FALSE", []string{"v"}, []string{"_TEXT"}, []string{"_TEXT"}, nil, b.PG), true, true},
		}
		controls := []fixture.Step{
			fixture.Select("SELECT quote_ident(s) AS v FROM og_parse_ident_source WHERE id=4", []string{"v"}, []string{"TEXT"}, [][]any{{`"Foo.Bar[]"`}}),
		}
		objectGrantProviderLifecycle(t, p, b, c, run, prepared, "pg_catalog.parse_ident(text,boolean)", "function", "parse_ident", cases, controls)
	})
}
