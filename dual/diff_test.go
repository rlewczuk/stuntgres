package dual

import (
	"database/sql/driver"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestDualDiffCap(t *testing.T) {
	for _, tc := range []struct {
		adds, lines int
		last        string
	}{
		{50, 50, "line 50"},
		{51, 51, "… 1 more differences"},
		{60, 51, "… 10 more differences"},
	} {
		var d diffLines
		for i := 1; i <= tc.adds; i++ {
			d.add(fmt.Sprintf("line %d", i))
		}
		got := d.finish()
		if len(got) != tc.lines || got[len(got)-1] != tc.last {
			t.Errorf("%d adds: %d lines ending %q, want %d ending %q", tc.adds, len(got), got[len(got)-1], tc.lines, tc.last)
		}
	}
	var none diffLines
	if got := none.finish(); got != nil {
		t.Errorf("no lines: %q", got)
	}
}

func int64p(n int64) *int64 { return &n }

func TestDualDiffLines(t *testing.T) {
	lines := func(f func(*diffLines)) []string {
		var d diffLines
		f(&d)
		return d.finish()
	}
	sqlErr := func(code, message string) errInfo { return errInfo{class: classSQL, code: code, message: message} }
	for _, tc := range []struct {
		name string
		got  []string
		want []string
	}{
		{"error pair", lines(func(d *diffLines) {
			compareErrors(d, errInfo{}, sqlErr("42P01", `relation "t" does not exist`), false)
		}), []string{`error: stuntgres none, reference 42P01 relation "t" does not exist`}},
		{"ordinary and canceled", lines(func(d *diffLines) {
			compareErrors(d, errInfo{class: classOrdinary, message: "boom"}, errInfo{class: classCanceled, message: "context canceled"}, false)
		}), []string{"error: stuntgres error boom, reference canceled"}},
		{"equal ordinary", lines(func(d *diffLines) {
			compareErrors(d, errInfo{class: classOrdinary, message: "a"}, errInfo{class: classOrdinary, message: "b"}, false)
		}), nil},
		{"details", lines(func(d *diffLines) {
			n, r := sqlErr("23505", "dup"), sqlErr("23505", "dup")
			n.detail, r.hint, n.position, r.position = "d", "h", "8", "9"
			compareErrors(d, n, r, true)
		}), []string{`error detail: stuntgres "d", reference null`, `error hint: stuntgres null, reference "h"`, `error position: stuntgres "8", reference "9"`}},
		{"exec", lines(func(d *diffLines) {
			compareExec(d, execSummary{RowsAffected: int64p(2)}, execSummary{RowsAffected: int64p(3), LastInsertID: int64p(7)})
		}), []string{"rows_affected: stuntgres 2, reference 3", "last_insert_id: stuntgres unavailable, reference 7"}},
		{"exec unavailable", lines(func(d *diffLines) {
			compareExec(d, execSummary{RowsAffected: int64p(1)}, execSummary{})
		}), []string{"rows_affected: stuntgres 1, reference unavailable"}},
		{"num_input", lines(func(d *diffLines) {
			comparePrepare(d, prepSummary{NumInput: 1}, prepSummary{NumInput: 2})
			comparePrepare(d, prepSummary{NumInput: -1}, prepSummary{NumInput: 2})
		}), []string{"num_input: stuntgres 1, reference 2"}},
		{"sets", lines(func(d *diffLines) {
			n := &rowsOutcome{sets: []rawSet{intSet("a", 1)}, complete: true}
			r := &rowsOutcome{sets: []rawSet{intSet("a", 1), intSet("b", 2)}, complete: true}
			compareRows(d, &Options{}, "SELECT 1; SELECT 2", n, r)
		}), []string{"sets: stuntgres 1, reference 2"}},
		{"columns and types", lines(func(d *diffLines) {
			n := rawSet{columns: []string{"id", "v"}, types: []string{"INT4", "TEXT"}}
			r := rawSet{columns: []string{"id", "w"}, types: []string{"INT4", "VARCHAR"}}
			compareSet(d, &Options{}, 0, n, r, true)
		}), []string{`#0 columns: stuntgres ["id","v"], reference ["id","w"]`, `#0 types[1]: stuntgres "TEXT", reference "VARCHAR"`}},
		{"values and one-sided", lines(func(d *diffLines) {
			n := rawSet{columns: []string{"id", "v"}, rows: [][]driver.Value{{int64(1), "a"}, {int64(2), "b"}, {int64(4), nil}}}
			r := rawSet{columns: []string{"id", "v"}, rows: [][]driver.Value{{int64(1), "a"}, {int64(2), "z"}}}
			compareSet(d, &Options{}, 1, n, r, true)
		}), []string{"#1 rows: stuntgres 3, reference 2", `#1 row 1 v: stuntgres "b", reference "z"`, `#1 stuntgres only row 2 ["4",null]`}},
		{"partial count", lines(func(d *diffLines) {
			n := rawSet{columns: []string{"g"}, rows: [][]driver.Value{{int64(1)}, {int64(2)}, {int64(3)}}, partial: true}
			r := rawSet{columns: []string{"g"}, rows: [][]driver.Value{{int64(1)}, {int64(2)}}}
			compareSet(d, &Options{}, 0, n, r, true)
		}), []string{"#0 rows: stuntgres at least 3 (partial), reference 2", `#0 stuntgres only row 2 ["3"]`}},
		{"duplicate column names", lines(func(d *diffLines) {
			n := rawSet{columns: []string{"x", "x"}, rows: [][]driver.Value{{"a", "b"}}}
			r := rawSet{columns: []string{"x", "x"}, rows: [][]driver.Value{{"a", "c"}}}
			compareSet(d, &Options{}, 0, n, r, true)
		}), []string{`#0 row 0 [1]: stuntgres "b", reference "c"`}},
	} {
		if !slices.Equal(tc.got, tc.want) {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, tc.got, tc.want)
		}
	}

	long := strings.Repeat("a", 200)
	if got, want := jsonValue(&long), `"`+strings.Repeat("a", 120)+`…"`; got != want {
		t.Errorf("long value: %s, want %s", got, want)
	}
	multibyte := strings.Repeat("a", 119) + "é" + strings.Repeat("b", 10)
	if got, want := shown(multibyte), strings.Repeat("a", 119)+"…"; got != want {
		t.Errorf("rune at cut: %q, want %q", got, want)
	}
}

func intSet(column string, values ...int64) rawSet {
	s := rawSet{columns: []string{column}, types: []string{"INT4"}}
	for _, v := range values {
		s.rows = append(s.rows, []driver.Value{v})
	}
	return s
}
