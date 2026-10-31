package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSecurityLabelOrderedSetModifiers(t *testing.T) {
	securityLabelScenarios(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		for _, pair := range [][2]string{
			{"1", "1"},
			{"01", "1"},
			{"CAST(1 AS int4)", "1::int4"},
			{"1::int4", "CAST(1 AS int4)"},
			{"abs(1)", "(abs(1))"},
			{"(abs(1))", "abs(1)"},
			{"abs(ALL 1)", "abs(1)"},
			{"abs(1)", "abs(ALL 1)"},
			{"abs(1) WITHIN GROUP (ORDER BY 1) FILTER (WHERE true)", "abs (1) WITHIN GROUP (ORDER BY 1) FILTER (WHERE true)"},
			{"abs(1) WITHIN GROUP (ORDER BY 1) FILTER (WHERE true) OVER ()", "abs (1) WITHIN GROUP (ORDER BY 1) FILTER (WHERE true) OVER ( )"},
			{"CAST(1 AS s.ty)", `CAST(1 AS s."ty")`},
			{`CAST(1 AS s."ty")`, "CAST(1 AS s.ty)"},
			{"((SELECT 1) UNION SELECT 2)", "(SELECT 1 UNION SELECT 2)"},
			{"(SELECT 1 UNION SELECT 2)", "((SELECT 1) UNION SELECT 2)"},
			{"abs(1)+2", "abs ( 1 ) + 2"},
			{"(SELECT 1)", "( SELECT 1 )"},
			{"(VALUES (1))", "( VALUES ( 1 ) )"},
			{"$1+2", "$1 + 2"},
		} {
			q := "SECURITY LABEL ON AGGREGATE a(VARIADIC int4(" + pair[0] + ") ORDER BY VARIADIC int4(" + pair[1] + ")) IS NULL"
			securityLabelError(t, p, b, c, prepared, q, securityLabelNoProvider)
		}
		for _, tc := range []struct{ query, position string }{
			{"SECURITY LABEL ON AGGREGATE a(VARIADIC int4(1) ORDER BY VARIADIC int4(2)) IS NULL", "57"},
			{"SECURITY LABEL ON AGGREGATE a(VARIADIC int4(abs(1)+2) ORDER BY VARIADIC int4(abs(1)+3)) IS NULL", "64"},
			{"SECURITY LABEL ON AGGREGATE a(VARIADIC int4(int4(1)) ORDER BY VARIADIC int4(1::int4)) IS NULL", "63"},
			{"SECURITY LABEL ON AGGREGATE a(VARIADIC int4(abs(1)) ORDER BY VARIADIC int4((abs(2)))) IS NULL", "62"},
			{"SECURITY LABEL ON AGGREGATE a(VARIADIC int4(1::int4) ORDER BY VARIADIC int4(1::int8)) IS NULL", "63"},
			{"SECURITY LABEL ON AGGREGATE a(VARIADIC int4(1+1) ORDER BY VARIADIC int4(2)) IS NULL", "59"},
		} {
			s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "0A000", Message: "an ordered-set aggregate with a VARIADIC direct argument must have one VARIADIC aggregated argument of the same data type", Position: tc.position})
			s.Prepared, s.ErrorStage = prepared, "execute"
			if prepared { s.ErrorStage = "prepare" }
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		for _, tc := range []struct {
			pair [2]string
			same, signature bool
		}{
			{pair: [2]string{"CAST(1 AS decimal)", "CAST(1 AS numeric)"}, same: true},
			{pair: [2]string{"CAST(1 AS integer)", "CAST(1 AS pg_catalog.int4)"}, same: true},
			{pair: [2]string{"CAST(1 AS dec)", "CAST(1 AS pg_catalog.numeric)"}, same: true},
			{pair: [2]string{"CAST(1 AS decimal(4,1)[])", "CAST(1 AS pg_catalog.numeric(4,1)[])"}, same: true},
			{pair: [2]string{"CAST(1 AS smallint)", "CAST(1 AS pg_catalog.int2)"}, same: true},
			{pair: [2]string{"CAST(1 AS bigint)", "CAST(1 AS pg_catalog.int8)"}, same: true},
			{pair: [2]string{"CAST(1 AS boolean)", "CAST(1 AS pg_catalog.bool)"}, same: true},
			{pair: [2]string{"CAST(1 AS real)", "CAST(1 AS pg_catalog.float4)"}, same: true},
			{pair: [2]string{"CAST(1 AS double precision)", "CAST(1 AS pg_catalog.float8)"}, same: true},
			{pair: [2]string{"CAST(1 AS integer)", `CAST(1 AS "pg_catalog"."int4")`}, same: true},
			{pair: [2]string{"CAST(1 AS int4(CAST(1 AS decimal)))", "CAST(1 AS int4(CAST(1 AS numeric)))"}, same: true},
			{pair: [2]string{"CAST(1 AS int4)", "CAST(1 AS integer)"}},
			{pair: [2]string{`CAST(1 AS "integer")`, "CAST(1 AS integer)"}},
			{pair: [2]string{"CAST(1 AS pg_catalog.integer)", "CAST(1 AS integer)"}},
			{pair: [2]string{`CAST(1 AS s."Ty")`, "CAST(1 AS s.ty)"}},
			{pair: [2]string{`CAST(1 AS pg_catalog."INT4")`, "CAST(1 AS integer)"}},
			{pair: [2]string{"CAST(1 AS decimal(4,1))", "CAST(1 AS numeric(4,2))"}},
			{pair: [2]string{"CAST(1 AS numeric[])", "CAST(1 AS numeric[][])"}},
			{pair: [2]string{"CAST(1 AS numeric[])", "CAST(1 AS numeric[3])"}},
			{pair: [2]string{"CAST(1 AS numeric[])", "CAST(1 AS numeric[0])"}},
			{pair: [2]string{"CAST(1 AS numeric)", "CAST(1 AS numeric[])"}},
			{pair: [2]string{"CAST(1 AS int4(CAST(1 AS decimal)))", "CAST(1 AS int4(CAST(2 AS numeric)))"}},
			{pair: [2]string{"SETOF integer", "SETOF pg_catalog.int4"}, same: true, signature: true},
			{pair: [2]string{"SETOF integer", "integer"}, signature: true},
			{pair: [2]string{"t.v%TYPE", "t.v%TYPE"}, same: true, signature: true},
			{pair: [2]string{"t.v%TYPE", "t.w%TYPE"}, signature: true},
			{pair: [2]string{"t.v%TYPE", "t.v"}, signature: true},
			{pair: [2]string{"SETOF t.v%TYPE", "t.v%TYPE"}, signature: true},
			{pair: [2]string{"nchar", "pg_catalog.bpchar(1)"}, same: true, signature: true},
			{pair: [2]string{"national char", "pg_catalog.bpchar(1)"}, same: true, signature: true},
			{pair: [2]string{"national character", "pg_catalog.bpchar(1)"}, same: true, signature: true},
			{pair: [2]string{"nchar(3)", "pg_catalog.bpchar(3)"}, same: true, signature: true},
			{pair: [2]string{"national char(3)", "pg_catalog.bpchar(3)"}, same: true, signature: true},
			{pair: [2]string{"national character(3)", "pg_catalog.bpchar(3)"}, same: true, signature: true},
			{pair: [2]string{"nchar varying", "pg_catalog.varchar"}, same: true, signature: true},
			{pair: [2]string{"national char varying", "pg_catalog.varchar"}, same: true, signature: true},
			{pair: [2]string{"national character varying", "pg_catalog.varchar"}, same: true, signature: true},
			{pair: [2]string{"nchar varying(3)", "pg_catalog.varchar(3)"}, same: true, signature: true},
			{pair: [2]string{"national char varying(3)", "pg_catalog.varchar(3)"}, same: true, signature: true},
			{pair: [2]string{"national character varying(3)", "pg_catalog.varchar(3)"}, same: true, signature: true},
			{pair: [2]string{"nchar[]", "pg_catalog.bpchar(1)[]"}, same: true, signature: true},
			{pair: [2]string{"national character varying(3)[]", "pg_catalog.varchar(3)[]"}, same: true, signature: true},
			{pair: [2]string{"nchar", "pg_catalog.bpchar"}, signature: true},
			{pair: [2]string{"nchar", "pg_catalog.bpchar(2)"}, signature: true},
			{pair: [2]string{"nchar", "nchar varying"}, signature: true},
			{pair: [2]string{"nchar", `"nchar"`}, signature: true},
			{pair: [2]string{"nchar", "pg_catalog.nchar"}, signature: true},
			{pair: [2]string{"national character", `"national"`}, signature: true},
			{pair: [2]string{"nchar varying", "pg_catalog.varchar(3)"}, signature: true},
			{pair: [2]string{"nchar varying", `"nchar varying"`}, signature: true},
			{pair: [2]string{"s.nchar", `s."nchar"`}, same: true, signature: true},
			{pair: [2]string{"s.national(1)", `s."national"(1)`}, same: true, signature: true},
			{pair: [2]string{`s."Nchar"`, "s.nchar"}, signature: true},
			{pair: [2]string{"CAST(1 AS nchar)", "CAST(1 AS pg_catalog.bpchar(1))"}, same: true},
			{pair: [2]string{"CAST(1 AS national character varying(3))", "CAST(1 AS pg_catalog.varchar(3))"}, same: true},
			{pair: [2]string{"CAST(1 AS nchar)", `CAST(1 AS "nchar")`}},
			{pair: [2]string{"CAST(1 AS nchar)", "CAST(1 AS pg_catalog.bpchar(2))"}},
		} {
			for _, pair := range [][2]string{tc.pair, {tc.pair[1], tc.pair[0]}} {
				left, right := pair[0], pair[1]
				if !tc.signature { left, right = "int4("+left+")", "int4("+right+")" }
				query := "SECURITY LABEL ON AGGREGATE a(VARIADIC " + left + " ORDER BY VARIADIC " + right + ") IS NULL"
				if tc.same {
					securityLabelError(t, p, b, c, prepared, query, securityLabelNoProvider)
				} else {
					// Independent raw SQL character count at the outer VARIADIC.
					position := strconv.Itoa(len([]rune("SECURITY LABEL ON AGGREGATE a(VARIADIC "+left+" ORDER BY ")) + 1)
					s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "0A000", Message: "an ordered-set aggregate with a VARIADIC direct argument must have one VARIADIC aggregated argument of the same data type", Position: position})
					s.Prepared, s.ErrorStage = prepared, "execute"
					if prepared { s.ErrorStage = "prepare" }
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			}
		}
	})
}

func TestReferenceSecurityLabelCallDecorations(t *testing.T) {
	securityLabelScenarios(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		// Released func_expr composes these three optional clauses in order.
		// No callee/type resolution is reachable before the provider boundary.
		for _, call := range []string{"abs(1)", "lower('x')", "format('x')", "now()", "f(1)", `"f"(1)`, "s.f(1)", "pg_catalog.abs(1)", "sum(1)"} {
			for _, suffix := range []string{
				"", " WITHIN GROUP (ORDER BY 1)", " FILTER (WHERE true)", " OVER ()",
				" WITHIN GROUP (ORDER BY 1) FILTER (WHERE true)",
				" WITHIN GROUP (ORDER BY 1) OVER ()", " FILTER (WHERE true) OVER ()",
				" WITHIN GROUP (ORDER BY 1) FILTER (WHERE true) OVER ()",
			} {
				query := "SECURITY LABEL ON TYPE int4(" + call + suffix + ") IS NULL"
				securityLabelError(t, p, b, c, prepared, query, securityLabelNoProvider)
			}
		}
		for _, pair := range [][2]string{
			{"abs(1) WITHIN GROUP (ORDER BY 1) FILTER (WHERE true)", "abs(1) WITHIN GROUP (ORDER BY 1) FILTER (WHERE false)"},
			{"abs(1) WITHIN GROUP (ORDER BY 1) FILTER (WHERE true)", "abs(1) WITHIN GROUP (ORDER BY 2) FILTER (WHERE true)"},
			{"abs(1) WITHIN GROUP (ORDER BY 1) FILTER (WHERE true) OVER ()", "abs(1) WITHIN GROUP (ORDER BY 1) FILTER (WHERE true) OVER (PARTITION BY 1)"},
			{"abs(1) WITHIN GROUP (ORDER BY 1) FILTER (WHERE true) OVER (ORDER BY 1)", "abs(1) WITHIN GROUP (ORDER BY 1) FILTER (WHERE true) OVER (ORDER BY 2)"},
			{"abs(1 ORDER BY 1) FILTER (WHERE true)", "abs(1) WITHIN GROUP (ORDER BY 1) FILTER (WHERE true)"},
		} {
			query := "SECURITY LABEL ON AGGREGATE a(VARIADIC int4(" + pair[0] + ") ORDER BY VARIADIC int4(" + pair[1] + ")) IS NULL"
			// The ordered-set validator points at the aggregated VARIADIC,
			// independently of the modifier's spelling or backend output.
			position := strconv.Itoa(strings.Index(query, ") ORDER BY VARIADIC") + len(") ORDER BY ") + 1)
			s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "0A000", Message: "an ordered-set aggregate with a VARIADIC direct argument must have one VARIADIC aggregated argument of the same data type", Position: position})
			s.Prepared, s.ErrorStage = prepared, "execute"
			if prepared { s.ErrorStage = "prepare" }
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	})
}
