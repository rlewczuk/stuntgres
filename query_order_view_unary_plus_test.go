package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"stuntgres"
	"testing"
)

func TestQueryOrderViewUnaryPlusRegression(t *testing.T) {
	for _, key := range []string{"+1.5", "+1e0", "+2147483648"} {
		t.Run(key, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE t(id INT)")
			execSQL(t, db, "INSERT INTO t VALUES(1),(2),(2),(3)")
			execSQL(t, db, "CREATE VIEW constant_order AS SELECT id FROM t ORDER BY "+key)
			execSQL(t, db, "CREATE VIEW later_order AS SELECT id FROM t ORDER BY "+key+",id DESC")
			execSQL(t, db, "CREATE TABLE empty_t(id INT)")
			execSQL(t, db, "CREATE VIEW empty_order AS SELECT id FROM empty_t ORDER BY "+key)
			for _, name := range []string{"constant_order", "later_order", "empty_order"} {
				var definition string
				if err := db.QueryRow("SELECT definition FROM pg_catalog.pg_views WHERE viewname='" + name + "'").Scan(&definition); err != nil {
					t.Fatal(err)
				}
				execSQL(t, db, "CREATE VIEW replay_"+name+" AS "+definition)
			}
			check := func(db *sql.DB) {
				for _, prefix := range []string{"", "replay_"} {
					for _, name := range []string{"constant_order", "later_order", "empty_order"} {
						q := "SELECT id FROM " + prefix + name
						bag := true
						want := [][]any{{int64(1)}, {int64(2)}, {int64(2)}, {int64(3)}}
						if name == "later_order" {
							q += " ORDER BY id DESC"
							bag = false
							want = [][]any{{int64(3)}, {int64(2)}, {int64(2)}, {int64(1)}}
						}
						if name == "empty_order" {
							want = [][]any{}
						}
						rows, err := db.Query(q)
						queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, want, bag)
						stmt, err := db.Prepare(q)
						if err != nil {
							t.Fatal(err)
						}
						rows, err = stmt.Query()
						queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, want, bag)
						stmt.Close()
					}
				}
			}
			check(db)
			var dump bytes.Buffer
			if err := stuntgres.Dump(context.Background(), db, &dump); err != nil {
				t.Fatal(err)
			}
			restored := openLocal(t, stuntgres.NewDriver(), t.Name()+"_restored")
			if err := stuntgres.Restore(context.Background(), restored, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check(restored)
		})
	}
}
