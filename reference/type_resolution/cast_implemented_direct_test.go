package reference_test

import (
	"context"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

type castValueWitness struct{ source, target, input, output string }

// These are independently reasoned non-NULL witnesses for every released direct
// edge in the finite inventory. Source seeds are overridden per pair rather
// than confusing a legal type path with an invalid universal string seed.
func castDirectWitness(e castReleasedEdge) castValueWitness {
	s, t := castInventoryType(e.source), castInventoryType(e.target)
	w := castValueWitness{s.name, t.name, s.seed, t.output}
	numeric := func(n string) bool {
		return n == "int2" || n == "int4" || n == "int8" || n == "float4" || n == "float8" || n == "numeric"
	}
	if numeric(s.name) && numeric(t.name) {
		w.output = "1"
		return w
	}
	if t.name == "money" {
		w.output = "$1.00"
		return w
	}
	if s.name == "money" && t.name == "numeric" {
		w.output = "1.00"
		return w
	}
	if t.name == "bytea" {
		switch s.name {
		case "int2":
			w.output = `\x0001`
		case "int4":
			w.output = `\x00000001`
		case "int8":
			w.output = `\x0000000000000001`
		}
		return w
	}
	if s.name == "bytea" {
		w.input = `\x01`
		w.output = "1"
		return w
	}
	if s.family == "Identifiers" && t.family == "Identifiers" {
		w.input = "0"
		w.output = t.output
		if s.name == "xid8" {
			w.input = "1"
			w.output = "1"
		}
		return w
	}
	if numeric(s.name) && t.family == "Identifiers" {
		w.input = "0"
		w.output = t.output
		return w
	}
	if s.family == "Identifiers" && numeric(t.name) {
		w.input = "0"
		w.output = "0"
		return w
	}
	if s.name == "int4" && t.name == "bool" {
		w.input = "-2"
		w.output = "t"
		return w
	}
	if s.name == "bool" && t.name == "int4" {
		w.output = "1"
		return w
	}
	if s.name == `"char"` && t.name == "int4" {
		w.output = "114"
		return w
	}
	if s.name == "int4" && t.name == `"char"` {
		w.input = "114"
		w.output = "r"
		return w
	}
	if t.name == `"char"` {
		w.output = "r"
		return w
	}
	if s.name == "xid8" && t.name == "xid" {
		w.output = "1"
		return w
	}
	if s.name == "jsonb" {
		if t.name == "bool" {
			w.input = "true"
			w.output = "t"
		} else if numeric(t.name) {
			w.output = "1"
		} else {
			w.output = "1"
		}
		return w
	}
	if s.name == "json" && t.name == "jsonb" {
		w.output = "1"
		return w
	}
	if s.companion == t.name {
		w.output = "{" + s.output + "}"
		return w
	}
	if s.name == "interval" && t.name == "time" {
		w.input = "-2 hours"
		w.output = "22:00:00"
		return w
	}
	if s.family == "Temporal" && t.family == "Temporal" {
		w.output = t.output
		if t.name == "interval" && s.name != "interval" {
			w.output = "00:00:00"
		}
		return w
	}
	if s.family == "Geometry" && t.family == "Geometry" {
		// Degenerate finite shapes give exact rational coordinates. Circle to
		// polygon requires a nonzero radius and uses the released trig formula.
		switch s.name {
		case "point":
			w.input = "(0,0)"
		case "lseg":
			w.input = "[(0,0),(0,0)]"
		case "path", "polygon":
			w.input = "((0,0),(0,0),(0,0))"
		case "box":
			w.input = "(0,0),(0,0)"
		case "circle":
			w.input = "<(0,0),0>"
			if t.name == "polygon" {
				w.input = "<(2,2),1>"
			}
		}
		switch t.name {
		case "point":
			w.output = "(0,0)"
		case "box":
			w.output = "(0,0),(0,0)"
		case "lseg":
			w.output = "[(0,0),(0,0)]"
		case "path":
			w.output = "((0,0),(0,0),(0,0))"
		case "circle":
			w.output = "<(0,0),0>"
		case "polygon":
			if s.name == "circle" {
				w.output = castCirclePolygonExpected()
			} else {
				n := 3
				if s.name == "box" {
					n = 4
				}
				w.output = "(" + strings.TrimSuffix(strings.Repeat("(0,0),", n), ",") + ")"
			}
		}
		return w
	}
	if s.name == "macaddr" && t.name == "macaddr8" || s.name == "macaddr8" && t.name == "macaddr" {
		w.output = t.output
		return w
	}
	if s.name == "bit" && numeric(t.name) {
		w.output = "1"
		return w
	}
	if numeric(s.name) && t.name == "bit" {
		w.input = "1"
		w.output = "1"
		return w
	}
	if s.category == "S" && t.name == "regclass" {
		w.input = "cast_direct"
		w.output = "cast_direct"
		return w
	}
	if s.category == "S" || s.name == `"char"` {
		w.output = s.output
		return w
	}
	if t.category == "S" {
		w.output = s.output
		if s.name == "bool" {
			w.output = "true"
		}
		return w
	}
	if s.name == "cidr" && t.name == "inet" || s.name == "inet" && t.name == "cidr" || s.name == "bit" && t.name == "varbit" || s.name == "varbit" && t.name == "bit" || s.name == t.name {
		w.output = s.output
		return w
	}
	panic(fmt.Sprintf("no independently specified direct value for %s -> %s", s.name, t.name))
}

func castResultType(v castImplementedType, pg bool) string {
	if v.base != "" {
		return castResultType(castInventoryType(v.base), pg)
	}
	if v.oid == 0 {
		if v.element != "" && !pg {
			return "_" + strings.ToUpper(v.element)
		}
		if !pg && (v.family == "Installed" || v.kind == "m" || v.kind == "r") {
			return strings.ToUpper(v.name)
		}
		return ""
	}
	if v.name == "cast_anonymous_record" || v.name == "cast_deferred_record" {
		return "RECORD"
	}
	if v.name == "record[]" {
		return "_RECORD"
	}
	if v.name == `"char"` {
		return "CHAR"
	}
	if v.name == `"char"[]` {
		return "_CHAR"
	}
	if pg && (v.name == "xid8" || v.name == "pg_snapshot" || v.name == "jsonpath" || v.name == "jsonpath[]" || v.name == "regcollation[]" || v.name == "regcollation" || v.name == "macaddr8" || v.name == "int4multirange" || v.name == "int8multirange" || v.name == "nummultirange") {
		return ""
	}
	if v.element != "" {
		return "_" + strings.ToUpper(castInventoryType(v.element).name)
	}
	return strings.ToUpper(v.name)
}
func castExpectedValue(t *testing.T, v castImplementedType, text string, pg bool) any {
	t.Helper()
	if v.base != "" {
		return castExpectedValue(t, castInventoryType(v.base), text, pg)
	}
	parseCalendar := func(layout, text string) time.Time {
		bc := strings.HasSuffix(text, " BC")
		d, err := time.Parse(layout, strings.TrimSuffix(text, " BC"))
		if err != nil {
			t.Fatal(err)
		}
		if bc {
			d = time.Date(1-d.Year(), d.Month(), d.Day(), d.Hour(), d.Minute(), d.Second(), d.Nanosecond(), d.Location())
		}
		return d.UTC()
	}
	switch v.name {
	case "bool":
		return text == "t"
	case "int2", "int4", "int8":
		n, e := strconv.ParseInt(text, 10, 64)
		if e != nil {
			t.Fatal(e)
		}
		return n
	case "float4", "float8":
		n, e := strconv.ParseFloat(text, 64)
		if e != nil {
			t.Fatal(e)
		}
		return n
	case "oid":
		if !pg {
			n, e := strconv.ParseInt(text, 10, 64)
			if e != nil {
				t.Fatal(e)
			}
			return n
		}
		return text
	case "bytea":
		raw, e := hex.DecodeString(strings.TrimPrefix(text, `\x`))
		if e != nil {
			t.Fatal(e)
		}
		return string(raw)
	case "date":
		if !pg {
			return text
		}
		return parseCalendar("2006-01-02", text)
	case "time", "timetz":
		if !pg {
			return text
		}
		layout := "15:04:05"
		if v.name == "timetz" {
			layout = "15:04:05Z07"
		}
		d, e := time.Parse(layout, text)
		if e != nil {
			t.Fatal(e)
		}
		return d.UTC()
	case "timestamp", "timestamptz":
		layout := "2006-01-02 15:04:05"
		if v.name == "timestamptz" {
			layout = "2006-01-02 15:04:05Z07"
		}
		return parseCalendar(layout, text)
	}
	return text
}
func castResultStep(t *testing.T, q string, target castImplementedType, output any, pg bool, args ...any) fixture.Step {
	t.Helper()
	s := fixture.Select(q, []string{"v"}, []string{castResultType(target, pg)}, [][]any{{output}}, args...)
	// Unknown extension/user metadata names are intentional lib/pq behavior. Raw
	// callbacks still require the actual public scalar representation, including
	// nonnil byte payloads; they do not fabricate SQL formatting with another cast.
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if value == nil {
			return true
		}
		switch value.(type) {
		case string, []byte, int64, float64, bool, time.Time:
		default:
			t.Fatalf("unexpected public value %T %v", value, value)
		}
		if raw, ok := value.([]byte); ok && raw == nil {
			t.Fatal("typed nil bytes are not SQL NULL")
		}
		return true
	}
	return s
}
func TestReferenceCastImplementedDirectEdges(t *testing.T) {
	var cells []castMatrixCell
	for _, edge := range castReleasedEdges {
		// Equal-ID modifier procedures have real sizing witnesses in Typmods.
		if edge.source == edge.target {
			continue
		}
		cells = append(cells, castCaseCell(edge.source, edge.target))
	}
	castRunCases(t, cells, false)
}

func castInputArgument(t *testing.T, source castImplementedType, input string) any {
	t.Helper()
	if source.name != "bytea" {
		return input
	}
	raw, err := hex.DecodeString(strings.TrimPrefix(input, `\x`))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// REL_18_6 geo_ops.c circle_poly: step=2*pi/12, vertex=(2-cos(step*i),
// 2+sin(step*i)) for center (2,2), radius 1. This avoids near-zero coordinates
// where Go and libm trigonometric argument reduction differ by many tiny ULPs.
func castCirclePolygonExpected() string {
	points := make([]string, 12)
	pi := float64(math.Pi)
	step := 2 * pi / 12
	for i := range points {
		angle := step * float64(i)
		points[i] = "(" + strconv.FormatFloat(2-math.Cos(angle), 'g', -1, 64) + "," + strconv.FormatFloat(2+math.Sin(angle), 'g', -1, 64) + ")"
	}
	return "(" + strings.Join(points, ",") + ")"
}

func castCirclePolygonValue(t *testing.T, p *fixture.Pair, b fixture.Backend, q string, target castImplementedType, output any, args []any) {
	t.Helper()
	coordinates := func(text string) []float64 {
		parts := strings.Split(strings.NewReplacer("(", "", ")", "").Replace(text), ",")
		if len(parts) != 24 {
			t.Fatalf("polygon has %d coordinates, want 24: %s", len(parts), text)
		}
		values := make([]float64, len(parts))
		for i, part := range parts {
			value, err := strconv.ParseFloat(part, 64)
			if err != nil {
				t.Fatal(err)
			}
			values[i] = value
		}
		return values
	}
	want := coordinates(output.(string))
	rows, err := b.DB.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	meta, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(meta) != 1 || meta[0].Name() != "v" || meta[0].DatabaseTypeName() != castResultType(target, b.PG) {
		t.Fatalf("polygon metadata %+v", meta)
	}
	if !rows.Next() {
		t.Fatalf("missing polygon row: %v", rows.Err())
	}
	var raw any
	if err := rows.Scan(&raw); err != nil {
		t.Fatal(err)
	}
	text, ok := fixture.Normalize(raw).(string)
	if !ok {
		t.Fatalf("polygon value %T %v", raw, raw)
	}
	for i, value := range coordinates(text) {
		lower, upper := want[i], want[i]
		for range 2 {
			lower = math.Nextafter(lower, math.Inf(-1))
			upper = math.Nextafter(upper, math.Inf(1))
		}
		if math.IsNaN(value) || value < lower || value > upper {
			t.Fatalf("polygon coordinate %d = %.17g, want %.17g within 2 ULP", i, value, want[i])
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("unexpected extra polygon row or row error: %v", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}
