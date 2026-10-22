package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func geometryConversionBScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET extra_float_digits=3", 0))
			body(t, p, b, c)
		})
	}
}

type geometryConversionBCase struct {
	geometryConversionCase
	formals []string
	oid     int64
}

var geometryConversionBCases = []geometryConversionBCase{
	{geometryConversionCase{"lseg_box", "lseg(box '(1,0),(-1,0)')", "lseg($1::box)", "LSEG", "[(1,0),(-1,0)]", []any{"(1,0),(-1,0)"}}, []string{"box"}, 1541},
	{geometryConversionCase{"lseg_points", "lseg(point '(-1,0)',point '(1,0)')", "lseg($1::point,$2::point)", "LSEG", "[(-1,0),(1,0)]", []any{"(-1,0)", "(1,0)"}}, []string{"point", "point"}, 993},
	{geometryConversionCase{"path_polygon", "path(polygon '((0,0),(1,1),(2,0))')", "path($1::polygon)", "PATH", "((0,0),(1,1),(2,0))", []any{"((0,0),(1,1),(2,0))"}}, []string{"polygon"}, 1447},
	{geometryConversionCase{"coordinates", "point(23.4,-44.5)", "point($1::float8,$2::float8)", "POINT", "(23.4,-44.5)", []any{float64(23.4), float64(-44.5)}}, []string{"float8", "float8"}, 1440},
	{geometryConversionCase{"box_center", "point(box '(1,0),(-1,0)')", "point($1::box)", "POINT", "(0,0)", []any{"(1,0),(-1,0)"}}, []string{"box"}, 1534},
	{geometryConversionCase{"circle_center", "point('<(0,0),2>'::circle)", "point($1::circle)", "POINT", "(0,0)", []any{"<(0,0),2>"}}, []string{"circle"}, 1416},
	{geometryConversionCase{"lseg_center", "point(lseg '[(-1,0),(1,0)]')", "point($1::lseg)", "POINT", "(0,0)", []any{"[(-1,0),(1,0)]"}}, []string{"lseg"}, 1532},
	{geometryConversionCase{"polygon_center", "point(polygon '((0,0),(1,1),(2,0))')", "point($1::polygon)", "POINT", "(1,0.3333333333333333)", []any{"((0,0),(1,1),(2,0))"}}, []string{"polygon"}, 1540},
}

func geometryConversionBNullExpression(tc geometryConversionBCase, mask int) string {
	expr := tc.parameter
	for i, typ := range tc.formals {
		literal := "NULL::" + typ
		if mask&(1<<i) == 0 {
			literal = fmt.Sprintf("'%v'::%s", tc.args[i], typ)
		}
		expr = strings.ReplaceAll(expr, fmt.Sprintf("$%d::%s", i+1, typ), literal)
	}
	return expr
}

func geometryConversionBCombined(b fixture.Backend) fixture.Step {
	var exprs, names, types []string
	var row []any
	for _, tc := range geometryConversionBCases {
		exprs = append(exprs, tc.expression+" AS "+tc.name)
		names = append(names, tc.name)
		types = append(types, tc.typ)
		row = append(row, tc.want)
	}
	return geometryConversionSelect(b, "SELECT "+strings.Join(exprs, ","), names, types, [][]any{row})
}
