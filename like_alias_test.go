package stuntgres_test

import (
	"reflect"
	"stuntgres"
	"testing"
)

func TestLikeContextualLabels(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(x INTEGER)")
	execSQL(t, db, "INSERT INTO t VALUES(1)")
	for _, word := range []string{"like", "ilike"} {
		q := "SELECT x+1 " + word + " FROM t;"
		rows, err := db.Query(q)
		if err != nil {
			t.Fatal(q, err)
		}
		columns, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(columns, []string{word}) {
			rows.Close()
			t.Fatal(columns, err)
		}
		compareOpsRows(t, rows, nil, []string{"INT4"}, [][]any{{int64(2)}})
		stmt, err := db.Prepare(q)
		if err != nil {
			t.Fatal(q, err)
		}
		rows, err = stmt.Query()
		compareOpsRows(t, rows, err, []string{"INT4"}, [][]any{{int64(2)}})
		stmt.Close()
	}
}
