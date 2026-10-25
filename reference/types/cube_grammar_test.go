package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCubeGrammar(t *testing.T) {
	// REL_18_6 cubeparse.y/cubescan.l/cube_out and float8 shortest output
	// ground these expectations; root's first oracle run must confirm them.
	cases := []struct{ input, canonical string }{
		{"1", "(1)"}, {"(1)", "(1)"}, {"1.", "(1)"}, {".1", "(0.1)"}, {"+.1", "(0.1)"},
		{"1,2", "(1, 2)"}, {"(1,2)", "(1, 2)"}, {"(1),(2)", "(1),(2)"}, {"[(1),(2)]", "(1),(2)"},
		{"(1,2),(3,4)", "(1, 2),(3, 4)"}, {"()", "()"}, {"(),()", "()"}, {"[(),()]", "()"},
		{"(1,2),(1,2)", "(1, 2)"}, {"(3,1),(1,4)", "(3, 1),(1, 4)"}, {"(3,4),(1,2)", "(3, 4),(1, 2)"},
		{"(-0),(0)", "(-0)"}, {"(0),(-0)", "(0)"}, {"(NaN),(NaN)", "(NaN),(NaN)"},
		{"(Infinity),(Infinity)", "(Infinity)"}, {"nAn", "(NaN)"}, {"+iNf,-INFINITY", "(Infinity, -Infinity)"},
		{" \t[\n(\r1\f,\v2 ) , (3,4) ] ", "(1, 2),(3, 4)"},
		{"1e27", "(1e+27)"}, {"1e-7", "(1e-07)"}, {"1.234567890123456e15", "(1.234567890123456e+15)"},
		{"0.10000000000000002", "(0.10000000000000002)"}, {"5e-324", "(5e-324)"},
		{"2.2250738585072014e-308", "(2.2250738585072014e-308)"}, {"1.7976931348623157e308", "(1.7976931348623157e+308)"},
	}
	coordinates := make([]string, 100)
	for i := range coordinates {
		coordinates[i] = fmt.Sprint(i + 1)
	}
	point := "(" + strings.Join(coordinates, ",") + ")"
	canonical := "(" + strings.Join(coordinates, ", ") + ")"
	cases = append(cases, struct{ input, canonical string }{point, canonical})
	cases = append(cases, struct{ input, canonical string }{point + "," + point, canonical})
	cases = append(cases, struct{ input, canonical string }{point + ",(" + strings.Repeat("0,", 99) + "0)", canonical + ",(" + strings.Repeat("0, ", 99) + "0)"})
	for i, tc := range cases {
		t.Run(fmt.Sprintf("%02d", i), func(t *testing.T) {
			cubeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				cubeInstall(t, p, b, c)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE cube_grammar(id integer NOT NULL,v cube)", 0))
				run(fixture.Exec("INSERT INTO cube_grammar VALUES (1,"+cubeLiteral(tc.input)+")", 1))
				run(fixture.Exec("INSERT INTO cube_grammar VALUES (2,$1)", 1, tc.input))
				run(fixture.Prepared(fixture.Exec("INSERT INTO cube_grammar VALUES (3,$1)", 1, tc.input)))
				s := fixture.CubeSelect(b, "SELECT id,v FROM cube_grammar ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), tc.canonical}, {int64(2), tc.canonical}, {int64(3), tc.canonical}})
				run(s)
				run(fixture.Prepared(s))
			})
		})
	}
	t.Run("display_precision", func(t *testing.T) {
		cubeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			cubeInstall(t, p, b, c)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET extra_float_digits=0", 0))
			s := fixture.CubeSelect(b, "SELECT $1::cube AS v", []string{"v"}, [][]any{{"(0.1, 1.23456789012346e+15, 4.94065645841247e-324, -0)"}}, "0.10000000000000002,1.234567890123456e15,5e-324,-0")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		})
	})
}
