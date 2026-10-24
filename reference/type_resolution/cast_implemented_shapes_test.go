package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCastImplementedGenericShapes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			for _, q := range []string{
				"CREATE FUNCTION cast_shape_gate(integer) RETURNS boolean LANGUAGE SQL AS $$SELECT $1>0$$",
				"CREATE DOMAIN cast_shape_positive AS integer CONSTRAINT cast_shape_gate CHECK(cast_shape_gate(VALUE))",
				"CREATE DOMAIN cast_shape_nested AS cast_shape_positive",
				"CREATE DOMAIN cast_shape_required AS integer NOT NULL",
				"CREATE DOMAIN cast_shape_array AS integer[] CONSTRAINT cast_shape_nonempty CHECK(cardinality(VALUE)>0)",
				`CREATE DOMAIN cast_shape_short AS varchar(3) COLLATE "C"`,
				"CREATE TABLE cast_shape_saved(v cast_shape_positive)",
				"CREATE TYPE cast_shape_pair AS(n integer,s text,g integer)",
				"ALTER TYPE cast_shape_pair DROP ATTRIBUTE g",
				"CREATE TYPE cast_shape_typed AS(n cast_shape_required,s varchar(3))",
			} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO cast_shape_saved VALUES(1)", 1))
			for _, tc := range []struct{ source, target, input, output string }{
				{"int4[]", "int8[]", "[0:2]={1,NULL,2}", "[0:2]={1,NULL,2}"},
				{"int4[]", "bool[]", "[-1:1]={-2,NULL,0}", "[-1:1]={t,NULL,f}"},
				{"varchar(6)[]", "varchar(3)[]", "[0:1]={abcdef,é界abcd}", "[0:1]={abc,é界a}"},
				{"text[]", "uuid[]", "[0:2]={00000000000000000000000000000001,NULL,00000000000000000000000000000002}", "[0:2]={00000000-0000-0000-0000-000000000001,NULL,00000000-0000-0000-0000-000000000002}"},
			} {
				factName := tc.target
				if strings.HasPrefix(factName, "varchar(") {
					factName = "varchar[]"
				}
				target := castInventoryType(factName)
				q := "SELECT CAST(CAST($1 AS " + tc.source + ") AS " + tc.target + ") AS v WHERE $2"
				full := castResultStep(t, q, target, tc.output, b.PG, tc.input, true)
				empty := full
				empty.Args = []any{tc.input, false}
				empty.Rows = nil
				castWitnessRoutes(t, p, b, full, castResultStep(t, q, target, nil, b.PG, nil, true), empty)
			}
			q := "SELECT CAST(CAST($1 AS int4[]) AS int8[]) AS v"
			castWitnessRoutes(t, p, b, castResultStep(t, q, castInventoryType("int8[]"), "{}", b.PG, "{}"), castResultStep(t, q, castInventoryType("int8[]"), nil, b.PG, nil))
			for _, tc := range []struct{ q, input, code, message, datatype, constraint string }{
				{"SELECT CAST(CAST($1 AS integer) AS cast_shape_nested) AS v WHERE $2", "0", "23514", `value for domain cast_shape_nested violates check constraint "cast_shape_gate"`, "cast_shape_nested", "cast_shape_gate"},
				{"SELECT CAST(CAST($1 AS integer[]) AS cast_shape_required[]) AS v WHERE $2", "{1,NULL}", "23502", "domain cast_shape_required does not allow null values", "cast_shape_required", ""},
				{"SELECT CAST(CAST($1 AS integer[]) AS cast_shape_array) AS v WHERE $2", "{}", "23514", `value for domain cast_shape_array violates check constraint "cast_shape_nonempty"`, "cast_shape_array", "cast_shape_nonempty"},
			} {
				bad := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Schema: "public", DataType: tc.datatype, Constraint: tc.constraint}, tc.input, true)
				bad.SelectRows = true
				bad.ErrorStage = "execute"
				fact := castInventoryType("int4")
				if strings.Contains(tc.q, "integer[]") {
					fact = castInventoryType("int4[]")
				}
				if strings.Contains(tc.q, "cast_shape_required[]") {
					fact = castImplementedType{name: "cast_shape_required[]", element: "cast_shape_required"}
				}
				empty := castResultStep(t, tc.q, fact, nil, b.PG, tc.input, false)
				empty.Rows = nil
				castWitnessRoutes(t, p, b, bad, empty)
			}
			for _, target := range []string{"cast_shape_pair", "cast_shape_typed"} {
				fact := castImplementedType{name: target, kind: "c", category: "C"}
				expected := "(1,red)"
				source := "ROW(1,'red'::text)"
				if target == "cast_shape_typed" {
					source = "ROW('1'::text,'abcdef'::text)"
					expected = "(1,abc)"
				}
				q = "SELECT CAST(" + source + " AS " + target + ") AS v"
				castWitnessRoutes(t, p, b, castResultStep(t, q, fact, expected, b.PG))
			}
			q = "SELECT CAST(ROW(NULL::integer,'red'::text) AS cast_shape_typed) AS v"
			badMember := fixture.ImplicitError(q, fixture.Diagnostic{Code: "23502", Message: "domain cast_shape_required does not allow null values", Schema: "public", DataType: "cast_shape_required"})
			badMember.SelectRows = true
			badMember.ErrorStage = "execute"
			castWitnessRoutes(t, p, b, badMember)
			q = "SELECT CAST(CAST($1 AS integer) AS information_schema.cardinal_number) AS v WHERE $2"
			cardinal := fixture.ImplicitError(q, fixture.Diagnostic{Code: "23514", Message: `value for domain information_schema.cardinal_number violates check constraint "cardinal_number_domain_check"`, Schema: "information_schema", DataType: "cardinal_number", Constraint: "cardinal_number_domain_check"}, int64(-1), true)
			cardinal.SelectRows = true
			cardinal.ErrorStage = "execute"
			emptyCardinal := fixture.Select(q, []string{"v"}, []string{"INT4"}, nil, int64(-1), false)
			castWitnessRoutes(t, p, b, cardinal, emptyCardinal)
			for _, fields := range []string{"1", "1,'red'::text,3"} {
				q = "SELECT CAST(ROW(" + fields + ") AS cast_shape_pair) AS v"
				detail := "Input has too few columns."
				if strings.Contains(fields, ",") {
					detail = "Input has too many columns."
				}
				castWitnessDenied(t, p, b, q, fixture.Diagnostic{Code: "42846", Message: "cannot cast type record to cast_shape_pair", Detail: detail, Position: "8"})
			}
			// Explicit collation survives the cast; conflicting explicit derivations
			// must still fail analysis even when their string representations match.
			q = `SELECT CAST((CAST('a' AS cast_shape_short) COLLATE "C") AS text) = 'a' COLLATE "POSIX" AS v`
			castWitnessDenied(t, p, b, q, fixture.Diagnostic{Code: "42P21", Message: `collation mismatch between explicit collations "C" and "POSIX"`, Position: fixture.Position(strings.LastIndex(q, "COLLATE") + 1)})
			q = "SELECT CAST(CAST($1 AS text) AS name) AS v"
			castWitnessRoutes(t, p, b, castResultStep(t, q, castInventoryType("name"), strings.Repeat("界", 21), b.PG, strings.Repeat("界", 22)), castResultStep(t, q, castInventoryType("name"), nil, b.PG, nil))
			q = "SELECT CAST(r.v AS cast_shape_required) AS v,CAST(r.v AS integer) AS b FROM (VALUES(1)) l(n) LEFT JOIN (SELECT CAST(1 AS cast_shape_required) AS v) r ON false"
			run(fixture.Select(q, []string{"v", "b"}, []string{"INT4", "INT4"}, [][]any{{nil, nil}}))
			run(fixture.Exec("CREATE TABLE cast_shape_formats(b boolean,i inet)", 0))
			run(fixture.Exec("INSERT INTO cast_shape_formats VALUES(false,'192.0.2.1')", 1))
			run(fixture.Select("SELECT CAST(b AS text) AS bt,CAST(b AS name) AS bn,CAST(i AS text) AS it,CAST(i AS name) AS inn FROM cast_shape_formats", []string{"bt", "bn", "it", "inn"}, []string{"TEXT", "NAME", "TEXT", "NAME"}, [][]any{{"false", "f", "192.0.2.1/32", "192.0.2.1"}}))
		})
	}
}
