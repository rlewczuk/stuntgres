package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

var segIntervalNames = []string{"left_of", "right_of", "over_left", "over_right", "same", "overlap", "contains", "contained"}
var segIntervalOperators = []string{"<<", ">>", "&<", "&>", "=", "&&", "@>", "<@"}
var segIntervalRoutines = []string{"seg_left", "seg_right", "seg_over_left", "seg_over_right", "seg_same", "seg_overlap", "seg_contains", "seg_contained"}
var segIntervalTypes = []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}

func segIntervalProjection(a, b string) string {
	parts := make([]string, 8)
	for i, op := range segIntervalOperators {
		parts[i] = a + " " + op + " " + b + " AS " + segIntervalNames[i]
	}
	return strings.Join(parts, ",")
}
func segIntervalSelect(b fixture.Backend, q string, names, types []string, want [][]any, view bool, args ...any) fixture.Step {
	s := fixture.Select(q, names, types, want, args...)
	s.CheckRaw = func(t *testing.T, column, row int, v any) bool {
		if types[column] == "BOOL" && v != nil {
			if _, ok := v.(bool); !ok {
				t.Fatalf("raw BOOL %T", v)
			}
		}
		return false
	}
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		cols, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		segIntervalMetadata(t, b.PG, cols, names, types, view)
	}
	return s
}
func segIntervalMetadata(t *testing.T, pg bool, cols []*sql.ColumnType, names, types []string, view bool) {
	t.Helper()
	if len(cols) != len(names) {
		t.Fatal("column count", len(cols), len(names))
	}
	for i, col := range cols {
		if col.Name() != names[i] || col.DatabaseTypeName() != types[i] {
			t.Fatal("column identity", i, col.Name(), col.DatabaseTypeName())
		}
		if types[i] != "BOOL" {
			continue
		}
		if col.ScanType() != reflect.TypeFor[bool]() {
			t.Fatal("BOOL ScanType", col.ScanType())
		}
		want := view && !pg
		if n, known := col.Nullable(); n != want || known != want {
			t.Fatal("BOOL nullability", n, known, want)
		}
		if n, known := col.Length(); n != 0 || known {
			t.Fatal("BOOL length", n, known)
		}
		if p, s, known := col.DecimalSize(); p != 0 || s != 0 || known {
			t.Fatal("BOOL decimal", p, s, known)
		}
	}
}
func segIntervalBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}
func segIntervalBools(values [8]bool) []any {
	out := make([]any, 8)
	for i, v := range values {
		out[i] = v
	}
	return out
}

// Each vector is derived independently from REL_18_6 seg.c, including its OR
// overlap and metadata-sensitive seg_cmp equality. No backend supplies goldens.
var segIntervalCases = []struct {
	name, a, b string
	want       [8]bool
}{
	{"disjoint", "0 .. 1", "2 .. 3", [8]bool{true, false, true, false, false, false, false, false}},
	{"touch", "0 .. 1", "1 .. 2", [8]bool{false, false, true, false, false, true, false, false}},
	{"point", "1", "1", [8]bool{false, false, true, true, true, true, true, true}},
	{"enclosing_point", "0 .. 2", "1", [8]bool{false, false, false, false, false, true, true, false}},
	{"open_touch", "..0", "0..", [8]bool{false, false, true, false, false, true, false, false}},
	{"precision", "1.0", "1.00", [8]bool{false, false, true, true, false, true, true, true}},
	{"annotation", "~1", "1", [8]bool{false, false, true, true, false, true, true, true}},
	{"signed_zero", "-0.0", "0.0", [8]bool{false, false, true, true, true, true, true, true}},
	{"hidden_upper", "1.0 .. 1.00", "1.0", [8]bool{false, false, true, true, false, true, true, true}},
	{"inverted_overlap", "0(+-)-1", "-2 .. 0", [8]bool{false, true, true, true, false, true, false, true}},
	{"inverted_self", "0(+-)-1", "0(+-)-1", [8]bool{true, true, true, true, true, false, true, true}},
	{"inverted_point", "0(+-)-1", "0", [8]bool{true, true, true, true, false, false, false, true}},
	{"reverse_touch", "1 .. 2", "0 .. 1", [8]bool{false, false, false, true, false, true, false, false}},
	{"reverse_inverted", "-2 .. 0", "0(+-)-1", [8]bool{true, false, false, false, false, true, true, false}},
	{"derived_infinity", "3.4e38(+-)3.4e38", "1", [8]bool{false, false, false, false, false, true, true, false}},
	{"negative_infinity", "-3.4e38(+-)3.4e38", "-1", [8]bool{false, false, false, false, false, true, true, false}},
	{"subnormal", "1e-45", "3e-45", [8]bool{true, false, true, false, false, false, false, false}},
	{"open_lower_self", "..0", "..0", [8]bool{false, false, true, true, true, true, true, true}},
	{"open_upper_self", "0..", "0..", [8]bool{false, false, true, true, true, true, true, true}},
	{"open_contains_finite", "..0", "-1 .. 0", [8]bool{false, false, true, false, false, true, true, false}},
	{"reverse_disjoint", "2 .. 3", "0 .. 1", [8]bool{false, true, false, true, false, false, false, false}},
}

func segIntervalTableQuery() string {
	return "SELECT id," + segIntervalProjection("a", "b") + " FROM seg_interval_data ORDER BY id"
}
func segIntervalSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE seg_interval_data(id integer NOT NULL,a seg,b seg)", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO seg_interval_data VALUES (1,'0 .. 1','1 .. 2'),(2,'..0','0..'),(3,'1','1')", 3))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO seg_interval_data VALUES (4,$1,$2),(5,$3,$2)", 2, "1.0 .. 1.00", "1.0", nil)))
}
func segIntervalSeedRows() [][]any {
	return [][]any{
		{int64(1), false, false, true, false, false, true, false, false},
		{int64(2), false, false, true, false, false, true, false, false},
		{int64(3), false, false, true, true, true, true, true, true},
		{int64(4), false, false, true, true, false, true, true, true},
		{int64(5), nil, nil, nil, nil, nil, nil, nil, nil},
	}
}
func segIntervalTableCheck(b fixture.Backend) fixture.Step {
	return segIntervalSelect(b, segIntervalTableQuery(), append([]string{"id"}, segIntervalNames...), append([]string{"INT4"}, segIntervalTypes...), segIntervalSeedRows(), false)
}
