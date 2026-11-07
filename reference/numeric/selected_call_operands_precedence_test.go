package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSelectedCallOperandsPrecedence(t *testing.T) {
	selectedCallScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		selectedCallRun(t, p, b, c, fixture.Exec("SET search_path TO pg_catalog,public", 0))
		for _, qualifier := range []string{"", "pg_catalog."} {
			for _, tc := range []struct {
				expression, typ string
				want            any
			}{
				{"1+abs(-2)*3", "INT4", int64(7)},
				{"(1+abs(-2))*3", "INT4", int64(9)},
				{"-abs(-2)^2", "FLOAT8", float64(4)},
				{"abs(-2)^3^2", "FLOAT8", float64(64)},
				{"abs(-2)::text || 'x'", "TEXT", "2x"},
				{"CAST(abs(-2)+1 AS text)", "TEXT", "3"},
				{"NOT abs(-1)+1=2 OR abs(-2)*2=4 AND FALSE", "BOOL", false},
				{"TRUE OR abs(-2)*2=4 AND FALSE", "BOOL", true},
				{"(TRUE OR abs(-2)*2=4) AND FALSE", "BOOL", false},
				{"abs(-2)+abs(-3)*4", "INT4", int64(14)},
				{"abs(-2) OPERATOR(pg_catalog.+) abs(-3)*4", "INT4", int64(14)},
				{"abs(-2)*3=3*abs(-2)", "BOOL", true},
				{"abs(-2)<3 AND 1<abs(-2)", "BOOL", true},
				{"+abs(-2)", "INT4", int64(2)},
				{"-abs(-2)", "INT4", int64(-2)},
				{"(current_schemas(false))[1]", "NAME", "pg_catalog"},
				{"(ARRAY[10,20])[abs(-1)]", "INT4", int64(10)},
				{"(ARRAY[10,20])[abs(-1):abs(-2)]", "_INT4", "{10,20}"},
				{"(ROW(abs(-1),2)).f1", "INT4", int64(1)},
			} {
				expression := strings.ReplaceAll(tc.expression, "abs(", qualifier+"abs(")
				expression = strings.ReplaceAll(expression, "current_schemas(", qualifier+"current_schemas(")
				selectedCallBoth(t, p, b, c, fixture.Select("SELECT "+expression+" AS value", []string{"value"}, []string{tc.typ}, [][]any{{tc.want}}))
			}
			for _, tc := range []struct {
				expression, typ string
				want            any
				args            []any
			}{
				{"(ARRAY[10,20])[" + qualifier + "abs($1::int4)]", "INT4", int64(10), []any{int64(-1)}},
				{"(ARRAY[10,20])[" + qualifier + "abs($1::int4):" + qualifier + "abs($2::int4)]", "_INT4", "{10,20}", []any{int64(-1), int64(-2)}},
			} {
				selectedCallBoth(t, p, b, c, fixture.Select("SELECT "+tc.expression+" AS value", []string{"value"}, []string{tc.typ}, [][]any{{tc.want}}, tc.args...))
			}
		}
	})
}
