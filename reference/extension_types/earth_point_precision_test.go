package reference_test

import (
	"database/sql"
	"fmt"
	"math"
	"testing"

	"stuntgres/reference/fixture"
)

// Match the public FLOAT8 policy without treating an absolute or rounded
// tolerance as a ULP bound. NaN payloads are not portable; categories and all
// other special-value signs are exact.
func earthPointRawEqual(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	x, xOK := a.(float64)
	y, yOK := b.(float64)
	if !xOK || !yOK {
		return false
	}
	if math.IsNaN(x) || math.IsNaN(y) {
		return math.IsNaN(x) && math.IsNaN(y)
	}
	if x == 0 || y == 0 || math.IsInf(x, 0) || math.IsInf(y, 0) {
		return math.Float64bits(x) == math.Float64bits(y)
	}
	if math.Signbit(x) != math.Signbit(y) {
		return false
	}
	return earthPointULPDistance(x, y) <= 2
}

type earthPointRawKey struct {
	name, route    string
	repeat, column int
}

func earthPointULPDistance(x, y float64) uint64 {
	xBits, yBits := math.Float64bits(x), math.Float64bits(y)
	if xBits > yBits {
		return xBits - yBits
	}
	return yBits - xBits
}

func earthPointRawDescription(value any) string {
	if number, ok := value.(float64); ok {
		return fmt.Sprintf("%.17g bits=%016x", number, math.Float64bits(number))
	}
	return fmt.Sprintf("%#v", value)
}

type earthPointFiniteBounds struct{ minimum, maximum float64 }

func TestReferenceEarthPointRawPrecision(t *testing.T) {
	t.Run("matcher", func(t *testing.T) {
		two := math.Nextafter(math.Nextafter(1, math.Inf(1)), math.Inf(1))
		three := math.Nextafter(two, math.Inf(1))
		for _, tc := range []struct {
			a, b any
			want bool
		}{
			{float64(1), two, true}, {two, float64(1), true},
			{float64(1), three, false}, {three, float64(1), false},
			{float64(0), math.Copysign(0, -1), false},
			{float64(0), math.SmallestNonzeroFloat64, false},
			{float64(1), float64(-1), false},
			{math.NaN(), math.NaN(), true}, {math.NaN(), float64(0), false},
			{math.Inf(1), math.Inf(1), true}, {math.Inf(1), math.Inf(-1), false},
			{nil, nil, true}, {nil, float64(0), false},
		} {
			if got := earthPointRawEqual(tc.a, tc.b); got != tc.want {
				t.Errorf("raw matcher %s / %s = %v, want %v", earthPointRawDescription(tc.a), earthPointRawDescription(tc.b), got, tc.want)
			}
		}
	})
	// Published mile values plus independently reasoned boundary/special cases.
	// Independent assertions run on every route before values enter parity maps.
	cases := []struct {
		name, a, b string
		want       any
		tolerance  float64
	}{
		{"coincident", "(0,0)", "(0,0)", float64(0), 0},
		{"antipodal", "(0,0)", "(180,0)", "12436.77274", 0},
		{"quarter_meridian", "(0,0)", "(0,90)", "6218.38637", 0},
		{"quarter_equator", "(0,0)", "(90,0)", "6218.38637", 0},
		{"degree_equator", "(0,0)", "(1,0)", "69.09318", 0},
		{"degree_meridian", "(0,0)", "(0,1)", "69.09318", 0},
		{"longitude30", "(0,30)", "(1,30)", "59.83626", 0},
		{"latitude30", "(0,30)", "(0,31)", "69.09318", 0},
		{"longitude60", "(0,60)", "(1,60)", "34.54626", 0},
		{"latitude60", "(0,60)", "(0,61)", "69.09318", 0},
		{"published_cities", "(87.6,41.8)", "(106.7,35.1)", "1129.18983", 0},
		{"antimeridian", "(179,0)", "(-179,0)", "138.18636", 0},
		{"antimeridian_reverse", "(-179,0)", "(179,0)", "138.18636", 0},
		{"poles", "(0,90)", "(0,-90)", "12436.77274", 0},
		{"longitude60_reverse", "(1,60)", "(0,60)", "34.54626", 0},
		{"axes_reversed", "(60,0)", "(60,1)", "69.09318", 0},
		{"noncanonical_longitude", "(360,0)", "(361,0)", "69.09318", 0},
		{"noncanonical_latitude", "(0,100)", "(0,100)", float64(0), 0},
		{"near_antipodal", "(0,0)", "(179.999999,0.000001)", earthPointFiniteBounds{12436, 12436.772742000978}, 0},
		{"tiny", "(0,0)", "(0,0.000000001)", float64(6.909318190000541e-8), 3e-23},
		{"raw_degree", "(0,0)", "(1,0)", float64(69.09318190000541), 4e-12},
		{"raw_quarter", "(0,0)", "(90,0)", float64(6218.386371000487), 4e-12},
		{"raw_half", "(0,0)", "(180,0)", float64(12436.772742000974), 4e-12},
		{"null_left", "", "(1,0)", nil, 0},
		{"null_right", "(0,0)", "", nil, 0},
		{"null_both", "", "", nil, 0},
		{"null_nonfinite", "", "(Infinity,NaN)", nil, 0},
		{"nonfinite_null", "(NaN,-Infinity)", "", nil, 0},
	}
	for _, nonfinite := range []string{"(NaN,0)", "(0,NaN)", "(Infinity,0)", "(-Infinity,0)", "(0,Infinity)", "(0,-Infinity)"} {
		cases = append(cases, struct {
			name, a, b string
			want       any
			tolerance  float64
		}{"nonfinite_left_" + nonfinite, nonfinite, "(1,2)", math.NaN(), 0}, struct {
			name, a, b string
			want       any
			tolerance  float64
		}{"nonfinite_right_" + nonfinite, "(1,2)", nonfinite, math.NaN(), 0})
	}
	observed := [2]map[earthPointRawKey]any{make(map[earthPointRawKey]any), make(map[earthPointRawKey]any)}
	var ran [2]bool
	earthPointScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		earthInstall(t, p, b, c)
		backend := 0
		if b.PG {
			backend = 1
		}
		ran[backend] = true
		point := func(text string) string {
			if text == "" {
				return "NULL::point"
			}
			return "point '" + text + "'"
		}
		for _, tc := range cases {
			q := earthPointQuery(point(tc.a), point(tc.b))
			for _, route := range []string{"direct", "fresh", "held"} {
				var executor fixture.Executor = c
				var statement *sql.Stmt
				if route != "direct" {
					var err error
					statement, err = c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(tc.name, route, err)
					}
					executor = fixture.HeldStatement{Executor: c, Statement: statement, SQL: q}
				}
				func() {
					if statement != nil {
						defer statement.Close()
					}
					repeats := 1
					if route == "held" {
						repeats = 2
					}
					for repeat := range repeats {
						rows, err := executor.QueryContext(p.Ctx, q)
						if err != nil {
							t.Fatal(tc.name, route, err)
						}
						func() {
							defer rows.Close()
							meta, err := rows.ColumnTypes()
							if err != nil || len(meta) != 2 {
								t.Fatal(tc.name, route, "metadata", meta, err)
							}
							for column, name := range []string{"operator_miles", "function_miles"} {
								if meta[column].Name() != name || meta[column].DatabaseTypeName() != "FLOAT8" {
									t.Fatal(tc.name, route, "FLOAT8 metadata", meta[column])
								}
							}
							if !rows.Next() {
								t.Fatal(tc.name, route, "missing result", rows.Err())
							}
							var raw [2]any
							if err := rows.Scan(&raw[0], &raw[1]); err != nil {
								t.Fatal(err)
							}
							for column, value := range raw {
								if tc.want == nil {
									if value != nil {
										t.Fatalf("%s/%s column%d=%v want NULL", tc.name, route, column, value)
									}
								} else {
									got, ok := value.(float64)
									if !ok {
										t.Fatalf("%s/%s column%d raw=%T, want float64", tc.name, route, column, value)
									}
									if bounds, ok := tc.want.(earthPointFiniteBounds); ok {
										if math.IsNaN(got) || math.IsInf(got, 0) || got < bounds.minimum || got > bounds.maximum {
											t.Fatalf("%s/%s column%d raw=%.17g, want finite in [%g,%g]", tc.name, route, column, got, bounds.minimum, bounds.maximum)
										}
									} else if rounded, ok := tc.want.(string); ok {
										if fmt.Sprintf("%.5f", got) != rounded {
											t.Fatalf("%s/%s column%d raw=%.17g want rounded %s", tc.name, route, column, got, rounded)
										}
									} else {
										earthCoordinateFloat(t, got, tc.want.(float64), tc.tolerance)
									}
								}
								observed[backend][earthPointRawKey{tc.name, route, repeat, column}] = value
							}
							if rows.Next() || rows.Err() != nil {
								t.Fatal(tc.name, route, "extra result/error", rows.Err())
							}
						}()
					}
				}()
			}
		}
	})
	// A selected backend subtest is useful for oracle discovery; two-backend
	// runs additionally require the raw finite bound and exact special handling.
	if t.Failed() || !ran[0] || !ran[1] {
		return
	}
	wantCount := len(cases) * 4 * 2 // direct/fresh/two held executions, two surfaces
	if len(observed[0]) != wantCount || len(observed[1]) != wantCount {
		t.Fatalf("raw observation cardinality native=%d postgres=%d want=%d each", len(observed[0]), len(observed[1]), wantCount)
	}
	for key, native := range observed[0] {
		postgres, found := observed[1][key]
		if !found || !earthPointRawEqual(native, postgres) {
			distance := "non-numeric"
			if x, ok := native.(float64); ok {
				if y, ok := postgres.(float64); ok {
					distance = fmt.Sprint(earthPointULPDistance(x, y))
				}
			}
			t.Errorf("%s/%s repeat%d surface=%s native=%s postgres=%s ULP distance=%s, require <=2 ULP or exact special category/sign", key.name, key.route, key.repeat, []string{"operator", "function"}[key.column], earthPointRawDescription(native), earthPointRawDescription(postgres), distance)
		}
	}
}
