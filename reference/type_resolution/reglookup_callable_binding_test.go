package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func callableAnalysisError(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query string, want fixture.Diagnostic) {
	t.Helper()
	s := fixture.ErrorDiagnostic(query, want)
	s.SelectRows, s.ErrorStage = true, "execute"
	regObjectRunStep(t, p, b, c, s)
	s = fixture.Prepared(s)
	s.ErrorStage = "prepare"
	regObjectRunStep(t, p, b, c, s)
}

func TestReferenceRegLookupCallableBinding(t *testing.T) {
	regLookupScenario(t, []string{"call_bind"}, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regObjectExec(t, p, b, c, "CREATE SCHEMA call_bind")
		for _, kind := range callableKinds {
			t.Run(kind.name, func(t *testing.T) {
				for _, tc := range []struct{ args, signature string }{
					{"", ""}, {"'x'::text,'y'::text", "text, text"},
					{"42", "integer"}, {"TRUE", "boolean"},
					{"name => 'x'::text", "name => text"}, {"text => 'x'::text", "text => text"},
					{"VARIADIC ARRAY['x'::text]", "text[]"},
				} {
					t.Run(tc.args, func(t *testing.T) {
						regLookupAnalysisError(t, p, b, c, "SELECT "+kind.name+"("+tc.args+")", kind.name+"("+tc.signature+")")
					})
				}
				regLookupAnalysisError(t, p, b, c, "SELECT call_bind."+kind.name+"('x'::text)", "call_bind."+kind.name+"(text)")
				for _, spelling := range []string{kind.name, "pg_catalog." + kind.name, `"` + kind.name + `"`} {
					for _, cast := range []string{"", "::text", "::varchar"} {
						q := "SELECT " + spelling + "(" + regObjectLiteral(kind.input) + cast + ") AS value"
						regObjectBoth(t, p, b, c, callableSelect(b, q, []string{"value"}, []string{kind.family}, [][]any{{kind.output}}))
					}
				}
				regObjectBoth(t, p, b, c, callableSelect(b, "SELECT "+kind.name+"(NULL) AS value", []string{"value"}, []string{kind.family}, [][]any{{nil}}))
				q := "SELECT " + kind.name + "($1) AS value,pg_typeof($1)::text AS input"
				held := regLookupHeld(t, p, c, q)
				defer held.Statement.Close()
				for _, value := range []any{kind.input, []byte(kind.input), nil, []byte(nil), kind.input} {
					var output any = kind.output
					if value == nil {
						output = nil
					} else if bytes, ok := value.([]byte); ok && bytes == nil {
						output = nil
					}
					s := fixture.Select(q, []string{"value", "input"}, []string{kind.family, "TEXT"}, [][]any{{output, "text"}}, value)
					// The dedicated Values scenario checks complete alias descriptors;
					// this mixed projection proves the inferred formal type itself.
					s.CheckRaw = func(t *testing.T, col, row int, value any) bool {
						if col != 0 {
							return false
						}
						fixture.RegtypeRaw(t, b.PG, value)
						return true
					}
					regObjectBoth(t, p, b, c, s)
					regObjectRunStep(t, p, b, held, s)
				}
				for _, tc := range []struct{ tail, message string }{
					{"(DISTINCT 'x'::text)", "DISTINCT specified, but " + kind.name + " is not an aggregate function"},
					{"('x'::text ORDER BY 1)", "ORDER BY specified, but " + kind.name + " is not an aggregate function"},
					{"('x'::text) FILTER(WHERE TRUE)", "FILTER specified, but " + kind.name + " is not an aggregate function"},
					{"('x'::text) OVER()", "OVER specified, but " + kind.name + " is not a window function nor an aggregate function"},
				} {
					t.Run(tc.tail, func(t *testing.T) {
						want := fixture.ControlsReferenceDiagnostic("42809", tc.message)
						want.Position = "8"
						callableAnalysisError(t, p, b, c, "SELECT "+kind.name+tc.tail, want)
					})
				}
			})
		}
		for _, tc := range []struct{ query, signature, position string }{
			{`SELECT "To_Regproc"('x'::text)`, "To_Regproc(text)", "8"},
			{`SELECT "To_Regprocedure"('x'::text)`, "To_Regprocedure(text)", "8"},
			{`SELECT "To_Regoper"('x'::text)`, "To_Regoper(text)", "8"},
			{`SELECT "To_Regoperator"('x'::text)`, "To_Regoperator(text)", "8"},
			{"SELECT to_regprocc('x'::text)", "to_regprocc(text)", "8"},
			{"SELECT /* é🙂 */ to_regproc(42)", "to_regproc(integer)", "17"},
		} {
			t.Run(tc.query, func(t *testing.T) {
				want := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
				want.Position = tc.position
				want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				callableAnalysisError(t, p, b, c, tc.query, want)
			})
		}
		regObjectBoth(t, p, b, c, fixture.Select(`SELECT oid::bigint AS oid,proname,proargtypes::text AS args,prorettype::bigint AS result,provolatile::text AS volatility,proisstrict,proparallel::text AS parallel,proretset,proargnames::text AS names,proallargtypes::text AS alltypes,proargmodes::text AS modes,prosrc FROM pg_catalog.pg_proc WHERE oid IN (3476,3479,3492,3494) AND pronamespace=11 ORDER BY oid`,
			[]string{"oid", "proname", "args", "result", "volatility", "proisstrict", "parallel", "proretset", "names", "alltypes", "modes", "prosrc"},
			[]string{"INT8", "NAME", "TEXT", "INT8", "TEXT", "BOOL", "TEXT", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{
				{int64(3476), "to_regoperator", "25", int64(2204), "s", true, "s", false, nil, nil, nil, "to_regoperator"},
				{int64(3479), "to_regprocedure", "25", int64(2202), "s", true, "s", false, nil, nil, nil, "to_regprocedure"},
				{int64(3492), "to_regoper", "25", int64(2203), "s", true, "s", false, nil, nil, nil, "to_regoper"},
				{int64(3494), "to_regproc", "25", int64(24), "s", true, "s", false, nil, nil, nil, "to_regproc"},
			}))
	})
}
