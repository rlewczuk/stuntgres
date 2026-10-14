package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

type inspectFamily struct{ name, subtype, finite, lo, hi, hull string }

var inspectFamilies = []inspectFamily{
	{"int4range", "INT4", "[1,4]", "1", "5", "[1,5)"},
	{"int8range", "INT8", "(1,4]", "2", "5", "[2,5)"},
	{"numrange", "NUMERIC", "(1.00,4.0]", "1.00", "4.0", "(1.00,4.0]"},
	{"daterange", "DATE", "[2000-02-29,2000-02-29]", "2000-02-29", "2000-03-01", "[2000-02-29,2000-03-01)"},
	{"tsrange", "TIMESTAMP", "[2000-02-29 12:34:56,2000-02-29 12:34:56]", "2000-02-29 12:34:56", "2000-02-29 12:34:56", `["2000-02-29 12:34:56","2000-02-29 12:34:56"]`},
	{"tstzrange", "TIMESTAMPTZ", "[2000-02-29 12:34:56+00,2000-02-29 12:34:56+00]", "2000-02-29 12:34:56", "2000-02-29 12:34:56", `["2000-02-29 12:34:56+00","2000-02-29 12:34:56+00"]`},
}

func inspectScenario(t *testing.T, body func(*fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("SET TIME ZONE 'UTC'", 0))
		body(p, b)

	}
}

func inspectBound(b fixture.Backend, subtype, text string) any {
	switch subtype {
	case "INT4", "INT8":
		if text == "0" {
			return int64(0)
		}
		if text == "1" {
			return int64(1)
		}
		if text == "2" {
			return int64(2)
		}
		if text == "3" {
			return int64(3)
		}
		if text == "4" {
			return int64(4)
		}
		if text == "5" {
			return int64(5)
		}
		if text == "9" {
			return int64(9)
		}
		panic("unlisted independent integer bound: " + text)
	case "DATE":
		if !b.PG || strings.Contains(text, "infinity") {
			return text
		}
		v, err := time.Parse("2006-01-02", text)
		if err != nil {
			panic(err)
		}
		return v
	case "TIMESTAMP", "TIMESTAMPTZ":
		if strings.Contains(text, "infinity") {
			return text
		}
		v, err := time.Parse("2006-01-02 15:04:05", text)
		if err != nil {
			panic(err)
		}
		return v
	default:
		return text
	}
}

func inspectSelect(b fixture.Backend, q string, columns, types []string, rows [][]any) fixture.Step {
	return inspectSelectNullable(b, q, columns, types, rows, nil)
}

// A nil mask denotes computed targets; stored columns retain declaration facts.
func inspectSelectNullable(b fixture.Backend, q string, columns, types []string, rows [][]any, nullable []bool) fixture.Step {
	s := rangeSelect(b, q, columns, types, rows, nullable)
	rangeRaw := s.CheckRaw
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if !rangeRaw(t, column, row, value) {
			fixture.AssertRawValue(t, b.PG, types[column], value)
		}
		return true
	}
	rangeMetadata := s.CheckMetadata
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
		rangeMetadata(t, r)
		metadata, err := r.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if nullable == nil {
			fixture.ScalarAssertMetadata(t, b.PG, metadata, 0, 1, 2, 3, 4, 5, 6, 7)
		} else {
			fixture.ScalarAssertMetadata(t, b.PG, metadata)
		}
		for i, c := range metadata {
			var scan reflect.Type
			switch types[i] {
			case "NUMERIC":
				scan = reflect.TypeFor[string]()
				if b.PG {
					scan = reflect.TypeFor[any]()
				}
			case "DATE":
				scan = reflect.TypeFor[string]()
				if b.PG {
					scan = reflect.TypeFor[time.Time]()
				}
			case "TIMESTAMP", "TIMESTAMPTZ":
				scan = reflect.TypeFor[time.Time]()
			}
			if scan != nil && c.ScanType() != scan {
				t.Fatalf("%s ScanType=%v want %v", types[i], c.ScanType(), scan)
			}
			wantNullable, nullableKnown := false, false
			if !b.PG && nullable != nil {
				wantNullable, nullableKnown = nullable[i], true
			}
			if n, ok := c.Nullable(); n != wantNullable || ok != nullableKnown {
				t.Fatalf("query %q column %s nullable=%v/%v want %v/%v", q, c.Name(), n, ok, wantNullable, nullableKnown)
			}
			length, lengthKnown := int64(0), false
			if types[i] == "TEXT" {
				length, lengthKnown = 1<<63-1, true
			}
			if n, ok := c.Length(); n != length || ok != lengthKnown {
				t.Fatalf("computed length=%d/%v want %d/%v", n, ok, length, lengthKnown)
			}
			precision, scale, decimalKnown := int64(0), int64(0), false
			if b.PG && types[i] == "NUMERIC" {
				precision, scale, decimalKnown = 65535, 65531, true
			}
			if p, s, ok := c.DecimalSize(); p != precision || s != scale || ok != decimalKnown {
				t.Fatalf("computed decimal=%d/%d/%v want %d/%d/%v", p, s, ok, precision, scale, decimalKnown)
			}
		}
	}
	return s
}

func inspectTupleQuery(expr string) string {
	names := []string{"lower", "upper", "isempty", "lower_inc", "upper_inc", "lower_inf", "upper_inf"}
	aliases := []string{"lo", "hi", "empty", "li", "ui", "lf", "uf"}
	parts := make([]string, len(names))
	for i, name := range names {
		parts[i] = name + "(" + expr + ") AS " + aliases[i]
	}
	return "SELECT " + strings.Join(parts, ",")
}
func inspectTupleStep(b fixture.Backend, expr, subtype string, row []any) fixture.Step {
	return inspectSelect(b, inspectTupleQuery(expr), []string{"lo", "hi", "empty", "li", "ui", "lf", "uf"}, []string{subtype, subtype, "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{row})
}
func inspectRunBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
}

// A declared destination supplies range authority without enabling explicit casts.
func inspectInputTuple(t *testing.T, p *fixture.Pair, b fixture.Backend, typ, subtype string, input any, row []any) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE inspect_input(v "+typ+")", 0))
	for _, prepared := range []bool{false, true} {
		insert := fixture.Exec("INSERT INTO inspect_input VALUES ($1)", 1, input)
		insert.Prepared = prepared
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, insert)
		s := inspectTupleStep(b, "v", subtype, row)
		s.Query += " FROM inspect_input"
		inspectRunBoth(t, p, b, s)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DELETE FROM inspect_input", 1))
	}
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DROP TABLE inspect_input", 0))
}

func TestReferenceRangeInspectRanges(t *testing.T) {
	inspectScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		for _, f := range inspectFamilies {
			li, ui := true, false
			if f.name == "numrange" {
				li, ui = false, true
			}
			if f.name == "tsrange" || f.name == "tstzrange" {
				ui = true
			}
			for _, tc := range []struct {
				input string
				row   []any
			}{
				{f.finite, []any{inspectBound(b, f.subtype, f.lo), inspectBound(b, f.subtype, f.hi), false, li, ui, false, false}},
				{"empty", []any{nil, nil, true, false, false, false, false}},
				{"(,)", []any{nil, nil, false, false, false, true, true}},
				{"[,]", []any{nil, nil, false, false, false, true, true}},
				{"(," + f.hi + "]", []any{nil, inspectBound(b, f.subtype, f.hi), false, false, true, true, false}},
				{"[" + f.lo + ",)", []any{inspectBound(b, f.subtype, f.lo), nil, false, true, false, false, true}},
			} {
				// Discrete upper inclusive syntax advances finite upper bounds.
				if strings.HasPrefix(tc.input, "(,") && tc.input != "(,)" && (f.name == "int4range" || f.name == "int8range" || f.name == "daterange") {
					tc.input = "(," + f.hi + ")"
					tc.row[4] = false
				}
				inspectInputTuple(t, p, b, f.name, f.subtype, tc.input, tc.row)
			}
			inspectInputTuple(t, p, b, f.name, f.subtype, nil, []any{nil, nil, nil, nil, nil, nil, nil})
			if f.name == "numrange" || f.name == "daterange" || f.name == "tsrange" || f.name == "tstzrange" {
				lo, hi := "-infinity", "infinity"
				if f.name == "numrange" {
					lo, hi = "-Infinity", "Infinity"
				}
				inspectInputTuple(t, p, b, f.name, f.subtype, "["+lo+","+hi+"]", []any{lo, hi, false, true, true, false, false})
			}
		}
		inspectInputTuple(t, p, b, "int4range", "INT4", "[0,1)", []any{int64(0), int64(1), false, true, false, false, false})
		inspectInputTuple(t, p, b, "numrange", "NUMERIC", "[NaN,NaN]", []any{"NaN", "NaN", false, true, true, false, false})
		for _, f := range inspectFamilies[2:] {
			for _, brackets := range []string{"[)", "(]", "()"} {
				input := brackets[:1] + f.lo + "," + f.lo + brackets[1:]
				inspectInputTuple(t, p, b, f.name, f.subtype, input, []any{nil, nil, true, false, false, false, false})
			}
		}
	})
}

func TestReferenceRangeInspectMultiranges(t *testing.T) {
	inspectScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		for _, f := range inspectFamilies[:3] {
			name := strings.Replace(f.name, "range", "multirange", 1)
			for _, tc := range []struct {
				input string
				row   []any
			}{
				{"{}", []any{nil, nil, true, false, false, false, false}},
				{"{empty,empty}", []any{nil, nil, true, false, false, false, false}},
				{"{(,)}", []any{nil, nil, false, false, false, true, true}},
				{"{[8,9),empty,[1,3),[2,4)}", []any{inspectBound(b, f.subtype, "1"), inspectBound(b, f.subtype, "9"), false, true, false, false, false}},
				{"{(,2),[8,)}", []any{nil, nil, false, false, false, true, true}},
			} {
				inspectInputTuple(t, p, b, name, f.subtype, tc.input, tc.row)
			}
			inspectInputTuple(t, p, b, name, f.subtype, nil, []any{nil, nil, nil, nil, nil, nil, nil})
		}
		inspectInputTuple(t, p, b, "nummultirange", "NUMERIC", "{(1.00,2.0),[5.000,9.0]}", []any{"1.00", "9.0", false, false, true, false, false})
		inspectInputTuple(t, p, b, "nummultirange", "NUMERIC", "{[-Infinity,Infinity]}", []any{"-Infinity", "Infinity", false, true, true, false, false})
		inspectInputTuple(t, p, b, "nummultirange", "NUMERIC", "{[NaN,NaN]}", []any{"NaN", "NaN", false, true, true, false, false})
	})
}
