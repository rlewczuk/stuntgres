package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// pq rows.go's public metadata adapter and PostgreSQL result descriptors are
// independent authorities. Expected facts do not consult native cast plans.
func TestReferenceCastImplementedMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, tc := range []struct {
				target, fact, input, output string
				native, pg                  reflect.Type
				length, precision, scale    int64
			}{
				{"uuid", "uuid", "00000000000000000000000000000001", "00000000-0000-0000-0000-000000000001", reflect.TypeFor[string](), reflect.TypeFor[any](), 0, 0, 0},
				{"integer", "int4", "42", "42", reflect.TypeFor[int64](), reflect.TypeFor[int32](), 0, 0, 0},
				{"real", "float4", "1.5", "1.5", reflect.TypeFor[float64](), reflect.TypeFor[float32](), 0, 0, 0},
				{"boolean", "bool", "true", "t", reflect.TypeFor[bool](), reflect.TypeFor[bool](), 0, 0, 0},
				{"numeric(4,2)", "numeric", "1.25", "1.25", reflect.TypeFor[string](), reflect.TypeFor[any](), 0, 4, 2},
				{"varchar(3)", "varchar", "abcdef", "abc", reflect.TypeFor[string](), reflect.TypeFor[string](), 3, 0, 0},
				{"text", "text", "red", "red", reflect.TypeFor[string](), reflect.TypeFor[string](), math.MaxInt64, 0, 0},
				{"bytea", "bytea", `\x`, `\x`, reflect.TypeFor[[]byte](), reflect.TypeFor[[]byte](), math.MaxInt64, 0, 0},
				{"int8[]", "int8[]", "[0:2]={1,NULL,2}", "[0:2]={1,NULL,2}", reflect.TypeFor[string](), reflect.TypeFor[any](), 0, 0, 0},
			} {
				q := "SELECT CAST(CAST($1 AS text) AS " + tc.target + ") AS v WHERE $2"
				fact := castInventoryType(tc.fact)
				makeStep := func(arg any, selected bool) fixture.Step {
					var expected any
					if arg != nil {
						expected = castExpectedValue(t, fact, tc.output, b.PG)
					}
					s := castResultStep(t, q, fact, expected, b.PG, arg, selected)
					if !selected {
						s.Rows = nil
					}
					s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
						cols, err := r.ColumnTypes()
						if err != nil {
							t.Fatal(err)
						}
						if len(cols) != 1 {
							t.Fatal(cols)
						}
						c := cols[0]
						scan := tc.native
						if b.PG {
							scan = tc.pg
						}
						length := tc.length
						// pq reports an unbounded byte length; native exposes no
						// character-length contract for BYTEA. Both use []byte ScanType.
						if !b.PG && tc.fact == "bytea" {
							length = 0
						}
						n, nk := c.Nullable()
						l, lk := c.Length()
						precision, scale, dk := c.DecimalSize()
						if c.Name() != "v" || c.DatabaseTypeName() != castResultType(fact, b.PG) || c.ScanType() != scan || l != length || lk != (length != 0) || precision != tc.precision || scale != tc.scale || dk != (tc.precision != 0) {
							t.Fatalf("metadata %s/%s scan=%v nullable=%v/%v length=%d/%v decimal=%d,%d/%v", c.Name(), c.DatabaseTypeName(), c.ScanType(), n, nk, l, lk, precision, scale, dk)
						}
					}
					return s
				}
				castWitnessRoutes(t, p, b, makeStep(tc.input, true), makeStep(nil, true), makeStep(tc.input, false))
			}
		})
	}
}
