package stuntgres_test

import (
	"database/sql"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres"
)

// Long valid expressions and late bad operands exercise ordinary analysis,
// not a timing threshold or an operand evaluation-order guarantee.
func TestLogicalLongChainsAndPositions(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "logical-chains")
	execSQL(t, db, "CREATE TABLE t(id INT)")
	execSQL(t, db, "INSERT INTO t VALUES(1)")
	for _, tc := range []struct {
		expr     string
		want     any
		retained bool
	}{
		{strings.Repeat("TRUE AND ", 256) + "NULL", nil, false},
		{strings.Repeat("FALSE OR ", 256) + "TRUE", true, true},
		{strings.Repeat("NOT ", 256) + "NULL", nil, false},
		{strings.Repeat("NOT ", 257) + "TRUE", false, false},
	} {
		q := "SELECT /*é*/ " + tc.expr + " FROM t"
		for _, prepared := range []bool{false, true} {
			var rows *sql.Rows
			var err error
			if prepared {
				stmt, e := db.Prepare(q)
				if e != nil {
					t.Fatal(e)
				}
				rows, err = stmt.Query()
				stmt.Close()
			} else {
				rows, err = db.Query(q)
			}
			if err != nil {
				t.Fatal(err)
			}
			meta, err := rows.ColumnTypes()
			if err != nil || len(meta) != 1 || meta[0].Name() != "?column?" || meta[0].DatabaseTypeName() != "BOOL" || meta[0].ScanType() != reflect.TypeFor[bool]() {
				t.Fatal(meta, err)
			}
			if !rows.Next() {
				t.Fatal("missing logical result", rows.Err())
			}
			var got any
			if err := rows.Scan(&got); err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatal(got, tc.want, err)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("unexpected trailing result", rows.Err())
			}
			rows.Close()
		}
		want := [][]any{}
		if tc.retained {
			want = [][]any{{int64(1)}}
		}
		wantTuples(t, db, "SELECT id FROM t WHERE "+tc.expr, want)
	}
	for _, tc := range []struct{ prefix, construct string }{
		{"SELECT /*é*/ " + strings.Repeat("TRUE AND ", 256) + "(", "AND"},
		{"SELECT /*é*/ " + strings.Repeat("FALSE OR ", 256) + "(", "OR"},
		{"SELECT /*é*/ " + strings.Repeat("NOT ", 256) + "(", "NOT"},
	} {
		q := tc.prefix + "1) FROM t"
		want := stuntgres.Error{Code: "42804", Message: "argument of " + tc.construct + " must be type boolean, not type integer", Position: strconv.Itoa(utf8.RuneCountInString(tc.prefix) + 1)}
		rows, err := db.Query(q)
		if rows != nil {
			rows.Close()
			t.Fatal("late invalid operand exposed cursor")
		}
		typeCoreDiagnostic(t, err, want)
		stmt, err := db.Prepare(q)
		if stmt != nil {
			stmt.Close()
			t.Fatal("late invalid operand exposed handle")
		}
		typeCoreDiagnostic(t, err, want)
	}
}
