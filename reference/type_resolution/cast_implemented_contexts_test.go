package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCastImplementedContexts(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			castBuiltinSetup(t, p.Ctx, b)
			castUserShapesSetup(t, p.Ctx, b)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			for i, pair := range [][2]string{{"int2", "int4"}, {"float8", "int4"}, {"int4", "bool"}, {"bool", "int4"}, {"text", "uuid"}, {"uuid", "text"}, {"int4[]", "int8[]"}, {"int4[]", "bool[]"}, {"bool[]", "int8[]"}, {"cast_positive", "int8"}, {"int4", "cast_positive"}, {"cast_enum_a", "text"}, {"int4range", "int4multirange"}, {"oid", "int4"}, {"int4", "oid"}} {
				source, target := castInventoryType(pair[0]), castInventoryType(pair[1])
				cell := castCaseCell(pair[0], pair[1])
				value := castScalarMatrixValue(cell)
				from, to, fn := fmt.Sprintf("cast_context_s%d", i), fmt.Sprintf("cast_context_t%d", i), fmt.Sprintf("cast_context_f%d", i)
				run(fixture.Exec("CREATE TABLE "+from+"(id integer,v "+source.name+")", 0))
				run(fixture.Exec("INSERT INTO "+from+" VALUES(0,NULL),(1,$1)", 2, castInputArgument(t, source, value.input)))
				run(fixture.Exec("CREATE TABLE "+to+"(v "+target.name+")", 0))
				run(fixture.Exec("CREATE FUNCTION "+fn+"("+target.name+") RETURNS "+target.name+" LANGUAGE SQL AS $$SELECT $1$$", 0))
				q := "SELECT " + fn + "(v) AS v FROM " + from + " WHERE id=$1"
				if cell.contexts[0].admitted {
					castWitnessRoutes(t, p, b, castResultStep(t, q, target, castExpectedValue(t, target, value.output, b.PG), b.PG, int64(1)), castResultStep(t, q, target, nil, b.PG, int64(0)))
				} else {
					castWitnessDenied(t, p, b, q, fixture.Diagnostic{Code: "42883", Message: "function " + fn + "(" + source.diagnostic + ") does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}, int64(1))
				}
				q = "INSERT INTO " + to + "(v) SELECT v FROM " + from + " WHERE id=$1"
				if cell.contexts[1].admitted {
					castWitnessRoutes(t, p, b, fixture.Exec(q, 1, int64(1)), fixture.Exec(q, 1, int64(0)))
					v := castExpectedValue(t, target, value.output, b.PG)
					stored := fixture.Select("SELECT v FROM "+to, []string{"v"}, []string{castResultType(target, b.PG)}, [][]any{{v}, {v}, {nil}})
					stored.Bag = true
					run(stored)
				} else {
					want := fixture.Diagnostic{Code: "42804", Message: fmt.Sprintf("column %q is of type %s but expression is of type %s", "v", target.diagnostic, source.diagnostic), Hint: "You will need to rewrite or cast the expression.", Position: fixture.Position(strings.Index(q, "SELECT v") + 8)}
					s := fixture.ImplicitError(q, want, int64(1))
					s.ErrorStage = "execute"
					run(s)
					run(fixture.Select("SELECT count(*) AS n FROM "+to, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
				}
			}
			// An explicit-only declaration blocks assignment IO for the exact pair and
			// array leaf. An unrelated VARCHAR target must not transitively use it.
			for _, q := range []string{"CREATE TYPE cast_context_enum AS ENUM ('red')", "CREATE FUNCTION cast_context_text(cast_context_enum) RETURNS text LANGUAGE SQL AS $$SELECT CASE WHEN $1 IS NULL THEN NULL::text ELSE 'override'::text END$$", "CREATE CAST (cast_context_enum AS text) WITH FUNCTION cast_context_text(cast_context_enum)", "CREATE TABLE cast_context_block(v text)", "CREATE TABLE cast_context_array_block(v text[])"} {
				run(fixture.Exec(q, 0))
			}
			castWitnessRoutes(t, p, b, fixture.Select("SELECT CAST('red'::cast_context_enum AS text) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"override"}}))
			castWitnessRoutes(t, p, b, fixture.Select("SELECT ARRAY['red'::cast_context_enum,NULL::cast_context_enum]::text[] AS v", []string{"v"}, []string{"_TEXT"}, [][]any{{"{override,NULL}"}}))
			for _, tc := range []struct{ q, typ, source string }{{"INSERT INTO cast_context_block SELECT 'red'::cast_context_enum", "text", "cast_context_enum"}, {"INSERT INTO cast_context_array_block SELECT ARRAY['red'::cast_context_enum]", "text[]", "cast_context_enum[]"}} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42804", Message: "column \"v\" is of type " + tc.typ + " but expression is of type " + tc.source, Hint: "You will need to rewrite or cast the expression.", Position: fixture.Position(strings.Index(tc.q, "SELECT ") + 8)})
				s.ErrorStage = "execute"
				run(s)
			}
			castWitnessRoutes(t, p, b, fixture.Select("SELECT 'red'::cast_context_enum::varchar AS v", []string{"v"}, []string{"VARCHAR"}, [][]any{{"red"}}))
		})
	}
}
