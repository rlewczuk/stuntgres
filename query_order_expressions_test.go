package stuntgres_test

import (
	"testing"

	"stuntgres"
)

func TestQueryOrderScalarConsumers(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT,i INT,j INT,a BOOL,b BOOL)")
	execSQL(t, db, "INSERT INTO t VALUES(1,1,2,TRUE,FALSE),(2,3,1,FALSE,TRUE),(3,NULL,2,NULL,NULL)")
	for _, tc := range []struct {
		query, label, typ string
		want              [][]any
	}{
		{"SELECT id,i<j AS x FROM t ORDER BY x DESC NULLS LAST,id", "x", "BOOL", [][]any{{int64(1), true}, {int64(2), false}, {int64(3), nil}}},
		{"SELECT id,i IS NULL result FROM t ORDER BY result DESC,id", "result", "BOOL", [][]any{{int64(3), true}, {int64(1), false}, {int64(2), false}}},
		{"SELECT id,a AND a AS result FROM t ORDER BY a AND a DESC NULLS LAST,id", "result", "BOOL", [][]any{{int64(1), true}, {int64(2), false}, {int64(3), nil}}},
		{"SELECT id,num_nulls(i) AS n FROM t ORDER BY n DESC,id", "n", "INT4", [][]any{{int64(3), int64(1)}, {int64(1), int64(0)}, {int64(2), int64(0)}}},
		{"SELECT id,i BETWEEN 1 AND 2 AS x FROM t ORDER BY i BETWEEN 1 AND 2 DESC NULLS LAST,id", "x", "BOOL", [][]any{{int64(1), true}, {int64(2), false}, {int64(3), nil}}},
		{"SELECT id,CAST(i AS int4) cast_value FROM t ORDER BY cast_value DESC NULLS LAST,id", "cast_value", "INT4", [][]any{{int64(2), int64(3)}, {int64(1), int64(1)}, {int64(3), nil}}},
		{"SELECT id,i::int4 AS renamed FROM t ORDER BY renamed DESC NULLS LAST,id", "renamed", "INT4", [][]any{{int64(2), int64(3)}, {int64(1), int64(1)}, {int64(3), nil}}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			rows, err := db.Query(tc.query)
			queryCoreRows(t, rows, err, []string{"id", tc.label}, []string{"INT4", tc.typ}, tc.want, false)
			stmt, err := db.Prepare(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			rows, err = stmt.Query()
			queryCoreRows(t, rows, err, []string{"id", tc.label}, []string{"INT4", tc.typ}, tc.want, false)
		})
	}
}
