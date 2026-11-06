package dual

import (
	"database/sql/driver"
	"slices"
	"testing"
)

func TestDualOrderByTopLevel(t *testing.T) {
	for _, q := range []string{
		"SELECT 1 ORDER BY 1",
		"select a from t order\n by a",
		"SELECT a FROM t UNION SELECT b FROM u ORDER BY 1",
		"SELECT $1 ORDER BY 1",
		"SELECT 1; SELECT 2 ORDER BY 1",
		"SELECT a FROM t ORDER /* c */ BY a",
	} {
		if !hasTopLevelOrderBy(q) {
			t.Errorf("%q: want top-level ORDER BY", q)
		}
	}
	for _, q := range []string{
		"SELECT string_agg(a, ',' ORDER BY a) FROM t",
		"SELECT * FROM (SELECT a FROM t ORDER BY a) s",
		"WITH x AS (SELECT 1 ORDER BY 1) SELECT * FROM x",
		"SELECT row_number() OVER (ORDER BY a) FROM t",
		"SELECT 'ORDER BY'",
		`SELECT E'\' ORDER BY '`,
		`SELECT "ORDER" BY`,
		"SELECT 1 -- ORDER BY 1",
		"SELECT /* /* */ ORDER BY */ 1",
		"SELECT $$ ORDER BY $$",
		"SELECT $q$ ORDER BY $q$",
		"SELECT orderly FROM t",
		"SELECT (ARRAY[1,2])[1] FROM t",
	} {
		if hasTopLevelOrderBy(q) {
			t.Errorf("%q: want no top-level ORDER BY", q)
		}
	}
}

func canonRows(values ...string) [][]*string {
	rows := make([][]*string, len(values))
	for i, v := range values {
		rows[i] = []*string{&v}
	}
	return rows
}

func classify1(n, r []string, partial, ordered bool) ([]string, bool) {
	var d diffLines
	only := classifySet(&d, 0, canonSet{columns: []string{""}, rows: canonRows(n...), partial: partial}, canonSet{columns: []string{""}, rows: canonRows(r...)}, ordered)
	return d.finish(), only
}

func TestDualClassifyRows(t *testing.T) {
	for _, tc := range []struct {
		name             string
		n, r             []string
		partial, ordered bool
		lines            []string
		orderOnly        bool
	}{
		{"positional", []string{"1", "2"}, []string{"1", "2"}, false, true, nil, false},
		{"order ordered", []string{"1", "2"}, []string{"2", "1"}, false, true, []string{"#0 rows: same multiset, different order (first at row 0)"}, true},
		{"order unordered", []string{"1", "2"}, []string{"2", "1"}, false, false, nil, true},
		{"duplicates", []string{"1", "1", "2"}, []string{"1", "2", "2"}, false, true, []string{`#0 row 1 (reference 2) [0]: stuntgres "1", reference "2"`}, false},
		{"extra reference row", []string{"1", "2"}, []string{"1", "2", "3"}, false, true, []string{"#0 rows: stuntgres 2, reference 3", `#0 reference only row 2 ["3"]`}, false},
		{"partial prefix", []string{"1"}, []string{"1", "2"}, true, true, nil, false},
		{"partial order", []string{"2"}, []string{"1", "2"}, true, true, []string{"#0 rows: same multiset, different order (first at row 0)"}, true},
		{"partial value", []string{"9"}, []string{"1", "2"}, true, true, []string{`#0 row 0 [0]: stuntgres "9", reference "1"`}, false},
	} {
		lines, only := classify1(tc.n, tc.r, tc.partial, tc.ordered)
		if !slices.Equal(lines, tc.lines) || only != tc.orderOnly {
			t.Errorf("%s: lines %q order-only %t, want %q %t", tc.name, lines, only, tc.lines, tc.orderOnly)
		}
	}

	var d diffLines
	n := rawSet{columns: []string{"a", "b"}, rows: [][]driver.Value{{int64(1), int64(2)}}}
	r := rawSet{columns: []string{"a"}, rows: [][]driver.Value{{int64(3)}}}
	if only := compareSet(&d, &Options{}, 0, n, r, true); only {
		t.Error("column mismatch: order-only")
	}
	if got, want := d.finish(), []string{`#0 columns: stuntgres ["a","b"], reference ["a"]`}; !slices.Equal(got, want) {
		t.Errorf("column mismatch: %q, want %q", got, want)
	}
}

func TestDualHiddenReferenceError(t *testing.T) {
	native := func(partial bool, rows ...int64) *rowsOutcome {
		s := intSet("g", rows...)
		s.partial = partial
		return &rowsOutcome{sets: []rawSet{intSet("a", 1), s}}
	}
	ref := func(errSet int, rows ...int64) *rowsOutcome {
		return &rowsOutcome{sets: []rawSet{intSet("a", 1), intSet("g", rows...)}, errSet: errSet}
	}
	for _, tc := range []struct {
		name string
		n, r *rowsOutcome
		want bool
	}{
		{"later set", native(true, 1), ref(2, 1, 2), true},
		{"after native's last row", native(true, 1, 2), ref(1, 1, 2, 3), true},
		{"at native's last row", native(true, 1, 2), ref(1, 1, 2), true},
		{"inside native's rows", native(true, 1, 2), ref(1, 1), false},
		{"set native finished", native(false, 1, 2), ref(1, 1, 2), false},
		{"earlier set", native(true, 1), ref(0), false},
	} {
		if got := unreached(tc.n, tc.r); got != tc.want {
			t.Errorf("%s: hidden %t, want %t", tc.name, got, tc.want)
		}
	}
}
