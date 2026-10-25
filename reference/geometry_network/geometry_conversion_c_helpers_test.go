package reference_test

import (
	"database/sql"
	"math"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func geometryConversionCScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

// Independently specified release arithmetic, not native execution goldens.
// PostgreSQL's observed libc spellings are recorded separately below.
func geometryConversionCCircle(n int, x, y, radius float64) string {
	points := make([]string, n)
	step := 2 * math.Pi / float64(n)
	for i := range points {
		angle := step * float64(i)
		px := x - radius*math.Cos(angle)
		py := y + radius*math.Sin(angle)
		points[i] = "(" + strconv.FormatFloat(px, 'g', -1, 64) + "," + strconv.FormatFloat(py, 'g', -1, 64) + ")"
	}
	return "(" + strings.Join(points, ",") + ")"
}

var geometryConversionCCases = []geometryConversionBCase{
	{geometryConversionCase{"polygon_box", "polygon(box '(1,1),(0,0)')", "polygon($1::box)", "POLYGON", "((0,0),(0,1),(1,1),(1,0))", []any{"(1,1),(0,0)"}}, []string{"box"}, 1448},
	{geometryConversionCase{"polygon_path", "polygon(path '((0,0),(1,1),(2,0))')", "polygon($1::path)", "POLYGON", "((0,0),(1,1),(2,0))", []any{"((0,0),(1,1),(2,0))"}}, []string{"path"}, 1449},
	{geometryConversionCase{"polygon_circle", "polygon(circle '<(0,0),1>')", "polygon($1::circle)", "POLYGON", geometryConversionCCircle(12, 0, 0, 1), []any{"<(0,0),1>"}}, []string{"circle"}, 1544},
	{geometryConversionCase{"polygon_points", "polygon(4,circle '<(0,0),1>')", "polygon($1::integer,$2::circle)", "POLYGON", geometryConversionCCircle(4, 0, 0, 1), []any{int64(4), "<(0,0),1>"}}, []string{"integer", "circle"}, 1475},
}

func geometryConversionCSelect(b fixture.Backend, q string, want any, args ...any) fixture.Step {
	return geometryConversionCProjection(b, q, []string{"v"}, []string{"POLYGON"}, [][]any{{want}}, args...)
}

// Observed PostgreSQL 18.6 / UTF8 / C values (root tests-1). Backend-specific
// last bits do not change logical count/order or permit snapping residuals.
var geometryConversionCPGCircle = map[string]string{
	geometryConversionCCircle(2, 0, 0, 1):                      "((-1,0),(1,1.2246467991473532e-16))",
	geometryConversionCCircle(3, 0, 0, 1):                      "((-1,0),(0.4999999999999998,0.8660254037844387),(0.5000000000000004,-0.8660254037844384))",
	geometryConversionCCircle(4, 0, 0, 1):                      "((-1,0),(-6.123233995736766e-17,1),(1,1.2246467991473532e-16),(1.8369701987210297e-16,-1))",
	geometryConversionCCircle(5, 0, 0, 1):                      "((-1,0),(-0.30901699437494745,0.9510565162951535),(0.8090169943749473,0.5877852522924732),(0.8090169943749476,-0.587785252292473),(-0.30901699437494723,-0.9510565162951536))",
	geometryConversionCCircle(12, 0, 0, 1):                     "((-1,0),(-0.8660254037844387,0.49999999999999994),(-0.5000000000000001,0.8660254037844386),(-6.123233995736766e-17,1),(0.4999999999999998,0.8660254037844387),(0.8660254037844385,0.5000000000000003),(1,1.2246467991473532e-16),(0.8660254037844388,-0.4999999999999997),(0.5000000000000004,-0.8660254037844384),(1.8369701987210297e-16,-1),(-0.49999999999999933,-0.866025403784439),(-0.8660254037844384,-0.5000000000000004))",
	geometryConversionCCircle(4, 0, 0, -1):                     "((1,0),(6.123233995736766e-17,-1),(-1,-1.2246467991473532e-16),(-1.8369701987210297e-16,1))",
	geometryConversionCCircle(2, 0, 0, -1.0000000000000002e-6): "((1.0000000000000002e-06,0),(-1.0000000000000002e-06,-1.2246467991473535e-22))",
}

func geometryConversionCExpected(b fixture.Backend, want any) any {
	if text, ok := want.(string); ok && b.PG {
		if pg, ok := geometryConversionCPGCircle[text]; ok {
			return pg
		}
	}
	return want
}

func geometryConversionCProjection(b fixture.Backend, q string, names, types []string, wants [][]any, args ...any) fixture.Step {
	expected := make([][]any, len(wants))
	for i, row := range wants {
		expected[i] = make([]any, len(row))
		for j, want := range row {
			expected[i][j] = geometryConversionCExpected(b, want)
		}
	}
	if wants == nil {
		expected = nil
	}
	s := geometryConversionSelect(b, q, names, types, expected, args...)
	raw := s.CheckRaw
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		checked := raw(t, column, row, value)
		if value != nil && row < len(wants) {
			if text, ok := wants[row][column].(string); ok {
				if _, circle := geometryConversionCPGCircle[text]; circle {
					geometryConversionCCoordinates(t, fixture.Normalize(value).(string), text)
				}
			}
		}
		return checked
	}
	return s
}

// Relative, not absolute, tolerance preserves the tiny axis residuals. Structural
// count/order and nonfinite categories remain exact. BOX/PATH never enter here.
func geometryConversionCCoordinates(t *testing.T, got, want string) {
	t.Helper()
	parse := func(text string) [][2]float64 {
		if !strings.HasPrefix(text, "((") || !strings.HasSuffix(text, "))") {
			t.Fatalf("polygon shape %q", text)
		}
		points := strings.Split(text[2:len(text)-2], "),(")
		out := make([][2]float64, len(points))
		for i, p := range points {
			xy := strings.Split(p, ",")
			if len(xy) != 2 {
				t.Fatalf("coordinate pair %q", p)
			}
			for j := range xy {
				f, err := strconv.ParseFloat(xy[j], 64)
				if err != nil {
					t.Fatal(err)
				}
				out[i][j] = f
			}
		}
		return out
	}
	actual, expected := parse(got), parse(want)
	if len(actual) != len(expected) {
		t.Fatalf("point count %d want %d", len(actual), len(expected))
	}
	for i, p := range expected {
		for j, w := range p {
			g := actual[i][j]
			if math.IsNaN(w) {
				if !math.IsNaN(g) {
					t.Fatalf("point %d/%d want NaN got %g", i, j, g)
				}
				continue
			}
			if math.IsInf(w, 0) || w == 0 {
				if g != w {
					t.Fatalf("point %d/%d got %g want %g", i, j, g, w)
				}
				continue
			}
			if math.IsNaN(g) || math.IsInf(g, 0) || g == 0 || math.Signbit(g) != math.Signbit(w) || math.Abs(g-w) > 1e-14*math.Abs(w) {
				t.Fatalf("point %d/%d got %.17g want %.17g (relative 1e-14; no snapping)", i, j, g, w)
			}
		}
	}
}
