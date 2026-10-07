package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestQueryOrderSecondKeyNulls(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT,a INT,b INT)")
	execSQL(t, db, "INSERT INTO t VALUES(1,1,NULL),(2,1,2),(3,1,1),(4,NULL,NULL),(5,NULL,3)")
	for _, tc := range []struct {
		policy string
		ids    []int64
	}{
		{"ASC NULLS FIRST", []int64{4, 5, 1, 3, 2}},
		{"ASC NULLS LAST", []int64{5, 4, 3, 2, 1}},
		{"DESC NULLS FIRST", []int64{4, 5, 1, 2, 3}},
		{"DESC NULLS LAST", []int64{5, 4, 2, 3, 1}},
	} {
		q := "SELECT id FROM t ORDER BY a ASC NULLS FIRST,b " + tc.policy + ",id"
		want := make([][]any, len(tc.ids))
		for i, id := range tc.ids {
			want[i] = []any{id}
		}
		rows, err := db.Query(q)
		queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, want, false)
		stmt, err := db.Prepare(q)
		if err != nil {
			t.Fatal(err)
		}
		rows, err = stmt.Query()
		queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, want, false)
		stmt.Close()
	}
}
