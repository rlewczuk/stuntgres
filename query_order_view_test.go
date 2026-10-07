package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"stuntgres"
	"testing"
)

func TestQueryOrderViewRegression(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT,a INT)")
	execSQL(t, db, "INSERT INTO t VALUES(1,2),(2,1),(3,NULL)")
	for _, q := range []string{
		"CREATE VIEW alias_order(k,id) AS SELECT a+0 AS x,id FROM t ORDER BY x DESC NULLS LAST,id",
		"CREATE VIEW hidden_order AS SELECT id FROM t ORDER BY COALESCE(a,0)+1 DESC NULLS LAST,id",
		"CREATE VIEW ordinal_order AS SELECT id,a FROM t ORDER BY 2 NULLS FIRST,1 DESC",
	} {
		execSQL(t, db, q)
	}
	execSQL(t, db, "ALTER TABLE t RENAME COLUMN a TO renamed")
	for _, name := range []string{"alias_order", "hidden_order", "ordinal_order"} {
		var definition string
		if err := db.QueryRow("SELECT definition FROM pg_catalog.pg_views WHERE viewname='" + name + "'").Scan(&definition); err != nil {
			t.Fatal(err)
		}
		execSQL(t, db, "CREATE VIEW replay_"+name+" AS "+definition)
	}
	check := func(db *sql.DB) {
		for _, prefix := range []string{"", "replay_"} {
			for _, tc := range []struct {
				q            string
				names, types []string
				want         [][]any
			}{
				{"SELECT k,id FROM " + prefix + "alias_order ORDER BY id", []string{"k", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(1)}, {int64(1), int64(2)}, {nil, int64(3)}}},
				{"SELECT id FROM " + prefix + "hidden_order ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}},
				{"SELECT id,a FROM " + prefix + "ordinal_order ORDER BY id", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(1)}, {int64(3), nil}}},
			} {
				rows, err := db.Query(tc.q)
				queryCoreRows(t, rows, err, tc.names, tc.types, tc.want, false)
				stmt, err := db.Prepare(tc.q)
				if err != nil {
					t.Fatal(err)
				}
				rows, err = stmt.Query()
				queryCoreRows(t, rows, err, tc.names, tc.types, tc.want, false)
				stmt.Close()
			}
		}
	}
	check(db)
	execSQL(t, db, "CREATE SEQUENCE alias_sequence START WITH 1")
	execSQL(t, db, "CREATE SEQUENCE hidden_sequence START WITH 1")
	execSQL(t, db, "CREATE VIEW volatile_alias AS SELECT nextval('alias_sequence') AS n FROM t ORDER BY n DESC")
	execSQL(t, db, "CREATE VIEW volatile_hidden AS SELECT id FROM t ORDER BY nextval('hidden_sequence') DESC")
	rows, err := db.Query("SELECT n FROM volatile_alias ORDER BY n DESC")
	queryCoreRows(t, rows, err, []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}, {int64(2)}, {int64(1)}}, false)
	rows, err = db.Query("SELECT id FROM volatile_hidden ORDER BY id")
	queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}, false)
	for _, name := range []string{"alias_sequence", "hidden_sequence"} {
		var next int64
		if err := db.QueryRow("SELECT nextval('" + name + "')").Scan(&next); err != nil || next != 4 {
			t.Fatal(name, next, err)
		}
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &dump); err != nil {
		t.Fatal(err)
	}
	restored := openLocal(t, stuntgres.NewDriver(), t.Name()+"_restored")
	if err := stuntgres.Restore(context.Background(), restored, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	check(restored)
}
