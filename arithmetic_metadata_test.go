package stuntgres_test

import (
	"reflect"
	"testing"
)

func TestArithmeticComputedMetadata(t *testing.T) {
	_, db := arithmeticDB(t)
	arithmeticExec(t, db, "CREATE TABLE t(i INT,b BIGINT)", 0)
	arithmeticExec(t, db, "INSERT INTO t VALUES(NULL,NULL)", 1)
	for _, q := range []string{"SELECT i+1,b*2 FROM t", "SELECT i+1,b*2 FROM t WHERE FALSE"} {
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
				stmt.Close()
				t.Fatal(err)
			}
			meta, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			for i, typ := range []string{"INT4", "INT8"} {
				if meta[i].Name() != "?column?" || meta[i].DatabaseTypeName() != typ || meta[i].ScanType() != reflect.TypeOf(int64(0)) {
					t.Fatal(meta[i])
				}
				if _, known := meta[i].Nullable(); known {
					t.Fatal("computed nullability is not unknown")
				}
			}
			for rows.Next() {
				var a, b any
				if err := rows.Scan(&a, &b); err != nil || a != nil || b != nil {
					t.Fatal(a, b, err)
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			stmt.Close()
		}
	}
	arithmeticRows(t, db, "SELECT 7% -2,-7% -2 FROM t", []string{"INT4", "INT4"}, [][]any{{int64(1), int64(-1)}})
}

func TestArithmeticExactProductAndDivision(t *testing.T) {
	_, db := arithmeticDB(t)
	arithmeticExec(t, db, "CREATE TABLE one(i INT)", 0)
	arithmeticExec(t, db, "INSERT INTO one VALUES(1)", 1)
	arithmeticRows(t, db, "SELECT 9007199254740993*2,18014398509481986/2 FROM one", []string{"INT8", "INT8"}, [][]any{{int64(18014398509481986), int64(9007199254740993)}})
}
