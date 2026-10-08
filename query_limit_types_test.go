package stuntgres_test

import (
	"math"
	"stuntgres"
	"testing"
)

func TestQueryLimitTypingProvider(t *testing.T) {
	db := queryLimitFixture(t)
	execSQL(t, db, "CREATE TABLE providers(n NUMERIC,f4 REAL,f8 DOUBLE PRECISION)")
	execSQL(t, db, "INSERT INTO providers VALUES(2.5,2.5,2.5),(NULL,NULL,NULL)")
	for _, tc := range []struct {
		bound string
		ids   []int64
	}{
		{"CAST(2 AS INTEGER)", []int64{1, 2}},
		{"CAST(2 AS BIGINT)", []int64{1, 2}},
		{"CAST(2 AS OID)", []int64{1, 2}},
		{"2.5", []int64{1, 2, 3}},
	} {
		t.Run(tc.bound, func(t *testing.T) {
			queryLimitSelect(t, db, "SELECT id FROM t ORDER BY id LIMIT "+tc.bound, "id", "INT4", queryLimitIDs(tc.ids...))
		})
	}
	queryLimitSelect(t, db, "SELECT id FROM t ORDER BY id FETCH FIRST CAST(2 AS BIGINT) ROWS ONLY", "id", "INT4", queryLimitIDs(1, 2))
	for _, tc := range []struct {
		column string
		arg    any
		ids    []int64
	}{
		{"n", "2.5", []int64{1, 2, 3}},
		{"f4", float32(2.5), []int64{1, 2}},
		{"f8", float64(2.5), []int64{1, 2}},
		{"n", nil, []int64{1, 2, 3, 4}},
		{"f4", nil, []int64{1, 2, 3, 4}},
		{"f8", nil, []int64{1, 2, 3, 4}},
	} {
		t.Run(tc.column+map[bool]string{false: "/value", true: "/null"}[tc.arg == nil], func(t *testing.T) {
			q := "SELECT t.id FROM t,providers WHERE providers." + tc.column + " IS NOT DISTINCT FROM $1 ORDER BY t.id LIMIT $1"
			rows, err := db.Query(q, tc.arg)
			queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, queryLimitIDs(tc.ids...), false)
			stmt, err := db.Prepare(q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			rows, err = stmt.Query(tc.arg)
			queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, queryLimitIDs(tc.ids...), false)
		})
	}
	for _, tc := range []struct {
		column        string
		arg           any
		code, message string
	}{
		{"n", "NaN", "0A000", "cannot convert NaN to bigint"},
		{"n", "Infinity", "0A000", "cannot convert infinity to bigint"},
		{"n", "-Infinity", "0A000", "cannot convert infinity to bigint"},
		{"f4", math.NaN(), "22003", "bigint out of range"},
		{"f8", math.Inf(1), "22003", "bigint out of range"},
		{"f8", math.Inf(-1), "22003", "bigint out of range"},
		{"f8", float64(9223372036854775807), "22003", "bigint out of range"},
		{"", nil, "22003", "bigint out of range"},
	} {
		t.Run(tc.column+"/"+tc.message, func(t *testing.T) {
			q := "SELECT id FROM t LIMIT 9223372036854775808"
			var args []any
			if tc.column != "" {
				q = "SELECT t.id FROM t,providers WHERE providers." + tc.column + "=$1 LIMIT $1"
				args = []any{tc.arg}
			}
			want := stuntgres.Error{Code: tc.code, Message: tc.message}
			rows, err := db.Query(q, args...)
			if rows != nil {
				rows.Close()
				t.Fatal("failed bound exposed rows")
			}
			typeCoreDiagnostic(t, err, want)
			stmt, err := db.Prepare(q)
			if err != nil {
				t.Fatal("runtime conversion failed Prepare", err)
			}
			defer stmt.Close()
			rows, err = stmt.Query(args...)
			if rows != nil {
				rows.Close()
				t.Fatal("failed bound exposed rows")
			}
			typeCoreDiagnostic(t, err, want)
		})
	}
}
