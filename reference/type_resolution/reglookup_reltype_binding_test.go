package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRegLookupRelTypeBinding(t *testing.T) {
	regLookupScenario(t, []string{"rl_bind"}, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regObjectExec(t, p, b, c, "CREATE SCHEMA rl_bind")
		for _, tc := range []struct {
			name, kind, input string
			output            any
		}{
			{"to_regclass", "REGCLASS", "pg_catalog.pg_class", "pg_class"},
			{"to_regnamespace", "REGNAMESPACE", "pg_catalog", "pg_catalog"},
			{"to_regtype", "REGTYPE", "integer", "integer"},
			{"to_regtypemod", "INT4", "varchar(12)", int64(16)},
		} {
			t.Run(tc.name, func(t *testing.T) {
				for _, bad := range []struct{ args, signature string }{
					{"", ""}, {"'x'::text,'y'::text", "text, text"}, {"TRUE", "boolean"}, {"42", "integer"},
					{"name => 'x'::text", "name => text"}, {"text => 'x'::text", "text => text"},
					{"VARIADIC ARRAY['x'::text]", "text[]"},
				} {
					t.Run("signature/"+bad.args, func(t *testing.T) {
						regLookupAnalysisError(t, p, b, c, "SELECT "+tc.name+"("+bad.args+")", tc.name+"("+bad.signature+")")
					})
				}
				for _, schema := range []string{"rl_bind", "rl_no_schema"} {
					t.Run(schema, func(t *testing.T) {
						query := "SELECT " + schema + "." + tc.name + "('x'::text)"
						if schema == "rl_no_schema" {
							want := fixture.ControlsReferenceDiagnostic("3F000", `schema "rl_no_schema" does not exist`)
							want.Position = "8"
							callableAnalysisError(t, p, b, c, query, want)
						} else {
							regLookupAnalysisError(t, p, b, c, query, schema+"."+tc.name+"(text)")
						}
					})
				}
				quoted := "To_" + tc.name[3:]
				t.Run("wrong_case", func(t *testing.T) {
					regLookupAnalysisError(t, p, b, c, "SELECT \""+quoted+"\"('x'::text)", quoted+"(text)")
				})
				for _, decoration := range []struct{ tail, message string }{
					{"(DISTINCT 'x'::text)", "DISTINCT specified, but " + tc.name + " is not an aggregate function"},
					{"('x'::text ORDER BY 1)", "ORDER BY specified, but " + tc.name + " is not an aggregate function"},
					{"('x'::text) FILTER(WHERE TRUE)", "FILTER specified, but " + tc.name + " is not an aggregate function"},
					{"('x'::text) OVER()", "OVER specified, but " + tc.name + " is not a window function nor an aggregate function"},
				} {
					t.Run(decoration.tail, func(t *testing.T) {
						want := fixture.ControlsReferenceDiagnostic("42809", decoration.message)
						want.Position = "8"
						callableAnalysisError(t, p, b, c, "SELECT "+tc.name+decoration.tail, want)
					})
				}
				regObjectBoth(t, p, b, c, regLookupRelTypeSelect(b, "SELECT \""+tc.name+"\"("+regObjectLiteral(tc.input)+") AS quoted", []string{"quoted"}, []string{tc.kind}, [][]any{{tc.output}}))
				for _, suffix := range []string{"::text", "::varchar", ""} {
					q := "SELECT pg_catalog." + tc.name + "(" + regObjectLiteral(tc.input) + suffix + ") AS value"
					regObjectBoth(t, p, b, c, regLookupRelTypeSelect(b, q, []string{"value"}, []string{tc.kind}, [][]any{{tc.output}}))
				}
				q := "SELECT " + tc.name + "($1) AS value,pg_typeof($1)::text AS input"
				held := regLookupHeld(t, p, c, q)
				defer held.Statement.Close()
				for _, input := range []any{tc.input, []byte(tc.input), nil, tc.input} {
					output := tc.output
					if input == nil {
						output = nil
					}
					s := regLookupRelTypeSelect(b, q, []string{"value", "input"}, []string{tc.kind, "TEXT"}, [][]any{{output, "text"}}, input)
					regObjectBoth(t, p, b, c, s)
					regObjectRunStep(t, p, b, held, s)
				}
				for _, args := range [][]any{nil, {tc.input, tc.input}} {
					rows, err := held.Statement.QueryContext(p.Ctx, args...)
					if rows != nil {
						rows.Close()
						t.Error("driver arity failure exposed cursor")
					}
					if err == nil {
						t.Fatal("wrong driver argument count succeeded")
					}
					if _, ok := err.(interface{ SQLState() string }); ok {
						t.Fatal("ordinary driver arity acquired SQLSTATE", err)
					}
				}
				regObjectRunStep(t, p, b, held, regLookupRelTypeSelect(b, q, []string{"value", "input"}, []string{tc.kind, "TEXT"}, [][]any{{tc.output, "text"}}, tc.input))
			})
		}
		t.Run("unknown_name", func(t *testing.T) {
			regLookupAnalysisError(t, p, b, c, "SELECT rl_unknown_lookup('x'::text)", "rl_unknown_lookup(text)")
		})
		// A driver integer belongs to an inferred TEXT formal, not an SQL integer
		// overload. The type/modifier grammar distinction remains observable.
		regObjectBoth(t, p, b, c, regLookupRelTypeSelect(b, `SELECT to_regtype($1) AS value,pg_typeof($1)::text AS input`, []string{"value", "input"}, []string{"REGTYPE", "TEXT"}, [][]any{{"integer", "text"}}, int64(23)))
		regObjectBoth(t, p, b, c, fixture.Select(`SELECT oid::bigint AS oid,proname,proargtypes::text AS args,prorettype::bigint AS result,provolatile::text AS volatility,proisstrict,proparallel::text AS parallel,proretset,proargnames::text AS names,proallargtypes::text AS alltypes,proargmodes::text AS modes,prosrc,prolang::bigint AS language,procost::float8 AS cost,pronargdefaults::integer AS defaults,provariadic::bigint AS variadic,pronargs::integer AS nargs,prokind::text AS kind FROM pg_catalog.pg_proc WHERE oid IN (3493,3495,4086,6317) AND pronamespace=11 ORDER BY oid`,
			[]string{"oid", "proname", "args", "result", "volatility", "proisstrict", "parallel", "proretset", "names", "alltypes", "modes", "prosrc", "language", "cost", "defaults", "variadic", "nargs", "kind"},
			[]string{"INT8", "NAME", "TEXT", "INT8", "TEXT", "BOOL", "TEXT", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT", "INT8", "FLOAT8", "INT4", "INT8", "INT4", "TEXT"}, [][]any{
				{int64(3493), "to_regtype", "25", int64(2206), "s", true, "s", false, nil, nil, nil, "to_regtype", int64(12), float64(1), int64(0), int64(0), int64(1), "f"},
				{int64(3495), "to_regclass", "25", int64(2205), "s", true, "s", false, nil, nil, nil, "to_regclass", int64(12), float64(1), int64(0), int64(0), int64(1), "f"},
				{int64(4086), "to_regnamespace", "25", int64(4089), "s", true, "s", false, nil, nil, nil, "to_regnamespace", int64(12), float64(1), int64(0), int64(0), int64(1), "f"},
				{int64(6317), "to_regtypemod", "25", int64(23), "s", true, "s", false, nil, nil, nil, "to_regtypemod", int64(12), float64(1), int64(0), int64(0), int64(1), "f"},
			}))
	})
}
