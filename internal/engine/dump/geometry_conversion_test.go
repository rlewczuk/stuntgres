package dump

import (
	"testing"

	"stuntgres/internal/parser"
)

func TestGeometryConversionReplayShapes(t *testing.T) {
	for _, tc := range []struct {
		expression string
		valid      bool
	}{
		{"pg_catalog.circle('(0,0)'::pg_catalog.point,'-1'::pg_catalog.float8)", true},
		{"CAST(pg_catalog.circle('(0,0)'::pg_catalog.point,'-1'::pg_catalog.float8) AS circle)", true},
		{"circle('(0,0)'::pg_catalog.point,'-1'::pg_catalog.float8)", false},
		{"pg_catalog.circle('(0,0)'::pg_catalog.point)", false},
		{"pg_catalog.circle($1::pg_catalog.point,'-1'::pg_catalog.float8)", false},
		{"pg_catalog.circle('(0,0)'::pg_catalog.point,(random())::pg_catalog.float8)", false},
		{"pg_catalog.circle('(0,0)'::pg_catalog.point,'-1'::pg_catalog.text)", false},
		{"pg_catalog.box('(0,0)'::pg_catalog.point)", false},
	} {
		statements, err := parser.Parse("SELECT " + tc.expression)
		if err != nil {
			t.Fatal(tc.expression, err)
		}
		raw := statements[0].(*parser.Select).Targets[0]
		if err := RestoreScalar(raw, false); (err == nil) != tc.valid {
			t.Fatalf("%s: %v valid=%t", tc.expression, err, tc.valid)
		}
	}
}
