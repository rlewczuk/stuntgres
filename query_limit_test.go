package stuntgres_test

import (
	"database/sql"
	"stuntgres"
	"testing"
)

func queryLimitFixture(t *testing.T) *sql.DB {
	t.Helper()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT NOT NULL,v INT)")
	execSQL(t, db, "INSERT INTO t VALUES(1,2),(2,0),(3,3),(4,4)")
	return db
}

func queryLimitIDs(ids ...int64) [][]any {
	rows := make([][]any, len(ids))
	for i, id := range ids { rows[i] = []any{id} }
	return rows
}

func queryLimitSelect(t *testing.T, db *sql.DB, query string, name, typ string, want [][]any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			var rows *sql.Rows
			var err error
			if prepared {
				stmt, e := db.Prepare(query)
				if e != nil { t.Fatal(e) }
				defer stmt.Close()
				rows, err = stmt.Query()
			} else { rows, err = db.Query(query) }
			queryCoreRows(t, rows, err, []string{name}, []string{typ}, want, false)
		})
	}
}

func TestQueryLimitBasics(t *testing.T) {
	db := queryLimitFixture(t)
	for _, tc := range []struct{ suffix string; ids []int64 }{
		{"OFFSET 1 LIMIT 2", []int64{2,3}}, {"LIMIT 2 OFFSET 1", []int64{2,3}},
		{"LIMIT 0", nil}, {"LIMIT NULL", []int64{1,2,3,4}}, {"LIMIT ALL", []int64{1,2,3,4}},
		{"OFFSET NULL", []int64{1,2,3,4}}, {"OFFSET 0", []int64{1,2,3,4}},
		{"OFFSET 99", nil}, {"OFFSET 9223372036854775807 LIMIT 9223372036854775807", nil},
		{"OFFSET 1 LIMIT 9223372036854775807", []int64{2,3,4}},
		{"FETCH FIRST ROW ONLY", []int64{1}}, {"FETCH NEXT ROWS ONLY", []int64{1}},
		{"FETCH FIRST 2 ROWS ONLY", []int64{1,2}}, {"FETCH NEXT (1+1) ROW ONLY", []int64{1,2}},
		{"OFFSET 1 ROW FETCH NEXT 2 ROWS ONLY", []int64{2,3}},
		{"FETCH FIRST 2 ROWS ONLY OFFSET 1 ROWS", []int64{2,3}},
		{"FETCH FIRST NULL ROWS ONLY", []int64{1,2,3,4}},
	} {
		t.Run(tc.suffix, func(t *testing.T) { queryLimitSelect(t, db, "SELECT id FROM t ORDER BY id "+tc.suffix, "id", "INT4", queryLimitIDs(tc.ids...)) })
	}
	// Identical occurrences make the unordered witness independent of winners.
	queryLimitSelect(t, db, "SELECT 7 AS n FROM t LIMIT 2", "n", "INT4", queryLimitIDs(7,7))
}

func TestQueryLimitTyping(t *testing.T) {
	db := queryLimitFixture(t)
	for _, tc := range []struct{ bound string; ids []int64 }{
		{"'2'", []int64{1,2}}, {"2.5", []int64{1,2,3}}, {"-0.4", nil}, {"(1+1)", []int64{1,2}},
	} {
		t.Run(tc.bound, func(t *testing.T) { queryLimitSelect(t, db, "SELECT id FROM t ORDER BY id LIMIT "+tc.bound, "id", "INT4", queryLimitIDs(tc.ids...)) })
	}
	stmt, err := db.Prepare("SELECT id FROM t WHERE id >= $1 ORDER BY id LIMIT $1")
	if err != nil { t.Fatal(err) }
	defer stmt.Close()
	rows, err := stmt.Query(int32(2))
	queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, queryLimitIDs(2,3), false)
	execSQL(t, db, "CREATE TABLE typed(f DOUBLE PRECISION,s TEXT)")
	execSQL(t, db, "INSERT INTO typed VALUES(2.5,'2')")
	floatStmt, err := db.Prepare("SELECT id FROM t,typed WHERE typed.f=$1 ORDER BY id LIMIT $1")
	if err != nil { t.Fatal(err) }
	defer floatStmt.Close()
	rows, err = floatStmt.Query(float64(2.5))
	queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, queryLimitIDs(1,2), false)
	for _, arg := range []any{"2",nil} {
		rows, err = db.Query("SELECT id FROM t,typed WHERE typed.s=$1 ORDER BY id LIMIT $1",arg)
		if rows != nil { rows.Close(); t.Fatal("typed TEXT bound exposed rows") }
		typeCoreDiagnostic(t,err,stuntgres.Error{Code:"42804",Message:"argument of LIMIT must be type bigint, not type text",Position:"59"})
	}
}

func TestQueryLimitPrepared(t *testing.T) {
	db := queryLimitFixture(t)
	stmt, err := db.Prepare("SELECT id FROM t ORDER BY id LIMIT $1 OFFSET $2")
	if err != nil { t.Fatal(err) }
	defer stmt.Close()
	for _, tc := range []struct{ count, offset any; ids []int64 }{
		{int64(2), int64(1), []int64{2,3}}, {int64(0), int64(0), nil},
		{nil, nil, []int64{1,2,3,4}}, {int64(1), int64(3), []int64{4}},
	} {
		rows, err := stmt.Query(tc.count, tc.offset)
		queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, queryLimitIDs(tc.ids...), false)
	}
	rows, err := stmt.Query(int64(-1), int64(0))
	if rows != nil { rows.Close(); t.Fatal("negative bound exposed rows") }
	typeCoreDiagnostic(t, err, stuntgres.Error{Code:"2201W", Message:"LIMIT must not be negative"})
}

func TestQueryLimitDiagnostics(t *testing.T) {
	db := queryLimitFixture(t)
	for _, tc := range []struct{ query, code, message, position string; runtime bool }{
		{"SELECT id FROM t LIMIT TRUE", "42804", "argument of LIMIT must be type bigint, not type boolean", "24", false},
		{"SELECT id FROM t OFFSET TRUE", "42804", "argument of OFFSET must be type bigint, not type boolean", "25", false},
		{"SELECT id FROM t LIMIT id", "42P10", "argument of LIMIT must not contain variables", "24", false},
		{"SELECT id FROM t OFFSET id", "42P10", "argument of OFFSET must not contain variables", "25", false},
		{"SELECT id FROM t LIMIT missing", "42703", `column "missing" does not exist`, "24", false},
		{"SELECT /*é*/ id FROM t LIMIT id", "42P10", "argument of LIMIT must not contain variables", "30", false},
		{"SELECT id FROM t LIMIT count(*)", "42803", "aggregate functions are not allowed in LIMIT", "24", false},
		{"SELECT id FROM t OFFSET count(*)", "42803", "aggregate functions are not allowed in OFFSET", "25", false},
		{"SELECT id FROM t LIMIT 'bad'", "22P02", `invalid input syntax for type bigint: "bad"`, "24", false},
		{"SELECT id FROM t LIMIT -1", "2201W", "LIMIT must not be negative", "", true},
		{"SELECT id FROM t FETCH FIRST -1 ROW ONLY", "2201W", "LIMIT must not be negative", "", true},
		{"SELECT id FROM t OFFSET -1", "2201X", "OFFSET must not be negative", "", true},
		{"SELECT id FROM t LIMIT -1 OFFSET -1", "2201X", "OFFSET must not be negative", "", true},
		{"SELECT id FROM t OFFSET -1 LIMIT -1", "2201X", "OFFSET must not be negative", "", true},
		{"SELECT id FROM t LIMIT 0 OFFSET -1", "2201X", "OFFSET must not be negative", "", true},
		{"SELECT id FROM t LIMIT -0.5", "2201W", "LIMIT must not be negative", "", true},
	} {
		t.Run(tc.query, func(t *testing.T) {
			want := stuntgres.Error{Code:tc.code, Message:tc.message, Position:tc.position}
			rows, err := db.Query(tc.query)
			if rows != nil { rows.Close(); t.Fatal("failure exposed rows") }
			typeCoreDiagnostic(t, err, want)
			stmt, err := db.Prepare(tc.query)
			if tc.runtime {
				if err != nil { t.Fatal("runtime bound failed Prepare", err) }
				defer stmt.Close()
				rows, err = stmt.Query()
				if rows != nil { rows.Close(); t.Fatal("failed execution exposed rows") }
			} else if stmt != nil { stmt.Close(); t.Fatal("analysis failure exposed statement") }
			typeCoreDiagnostic(t, err, want)
		})
	}
	t.Run("occurrence conflict",func(t *testing.T) {
		want := stuntgres.Error{Code:"42P08",Message:"inconsistent types deduced for parameter $1",Detail:"bigint versus text",Position:"8"}
		rows,err := db.Query("SELECT $1 FROM t LIMIT $1",int64(1))
		if rows != nil { rows.Close(); t.Fatal("conflicting occurrence exposed rows") }
		typeCoreDiagnostic(t,err,want)
		stmt,err := db.Prepare("SELECT $1 FROM t LIMIT $1")
		if stmt != nil { stmt.Close(); t.Fatal("conflicting occurrence exposed statement") }
		typeCoreDiagnostic(t,err,want)
	})
}
