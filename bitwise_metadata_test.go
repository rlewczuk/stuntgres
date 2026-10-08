package stuntgres_test

import (
	"reflect"
	"stuntgres"
	"testing"
)

func TestBitwiseComputedMetadataAndParameterFrames(t *testing.T) {
	_, db := arithmeticDB(t)
	arithmeticExec(t, db, "CREATE TABLE t(s SMALLINT,i INTEGER,b BIGINT)", 0)
	arithmeticExec(t, db, "INSERT INTO t VALUES(NULL,NULL,NULL)", 1)
	for _, q := range []string{"SELECT ~s,i & 1,b << 1 FROM t", "SELECT ~s,i & 1,b << 1 FROM t WHERE FALSE"} {
		for _, prepared := range []bool{false, true} {
			stmt, err := db.Prepare(q)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := db.Query(q)
			if prepared {
				if rows != nil {
					rows.Close()
				}
				rows, err = stmt.Query()
			}
			if err != nil {
				t.Fatal(err)
			}
			meta, err := rows.ColumnTypes()
			if err != nil || len(meta) != 3 {
				t.Fatal(meta, err)
			}
			for i, typ := range []string{"INT2", "INT4", "INT8"} {
				if meta[i].Name() != "?column?" || meta[i].DatabaseTypeName() != typ || meta[i].ScanType() != reflect.TypeOf(int64(0)) {
					t.Fatal(meta[i])
				}
				if _, known := meta[i].Nullable(); known {
					t.Fatal("computed nullable became physical")
				}
			}
			for rows.Next() {
				var a, b, c any
				if err := rows.Scan(&a, &b, &c); err != nil || a != nil || b != nil || c != nil {
					t.Fatal(a, b, c, err)
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			stmt.Close()
		}
	}
	stmt, err := db.Prepare("SELECT s & $1,b << $2 FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, tc := range []struct{ arg, code, message string }{{"bad", "22P02", `invalid input syntax for type smallint: "bad"`}, {"32768", "22003", `value "32768" is out of range for type smallint`}} {
		rows, err := stmt.Query(tc.arg, 1)
		if rows != nil {
			rows.Close()
		}
		arithmeticError(t, err, stuntgres.Error{Code: tc.code, Message: tc.message,Context:"unnamed portal parameter $1 = '...'"})
	}
	for _, arg := range []any{3, nil, 1} {
		var a, b any
		if err := stmt.QueryRow(arg, 1).Scan(&a, &b); err != nil || a != nil || b != nil {
			t.Fatal(a, b, err)
		}
	}
	_, err = db.Prepare("SELECT s & $1, CASE WHEN $1 THEN 1 ELSE 0 END FROM t")
	arithmeticError(t, err, stuntgres.Error{Code: "42804", Message: "argument of CASE/WHEN must be type boolean, not type smallint", Position: "26"})
}
