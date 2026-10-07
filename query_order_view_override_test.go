package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"stuntgres"
	"testing"
)

func TestQueryOrderViewOverrideRegression(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, q := range []string{
		"CREATE TABLE t(id INT,b INT)", "INSERT INTO t VALUES(1,2),(2,1)",
		"CREATE SEQUENCE s START WITH 1",
		"CREATE VIEW collision(b,id) AS SELECT nextval('s') AS n,id FROM t ORDER BY b,id",
	} {
		execSQL(t, db, q)
	}
	var definition string
	if err := db.QueryRow("SELECT definition FROM pg_catalog.pg_views WHERE viewname='collision'").Scan(&definition); err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, "CREATE VIEW replay AS "+definition)
	// Persist pristine START1/is_called=false state before any view read.
	var dump bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &dump); err != nil {
		t.Fatal(err)
	}
	check := func(db *sql.DB, phase string) {
		for _, name := range []string{"collision", "replay"} {
			for _, prepared := range []bool{false, true} {
				t.Run(phase+"/"+name+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					if phase != "restored" || name != "collision" || prepared {
						execSQL(t, db, "ALTER SEQUENCE s RESTART WITH 1")
					}
					q := "SELECT b,id FROM " + name + " ORDER BY id"
					want := [][]any{{int64(2), int64(1)}, {int64(1), int64(2)}}
					if prepared {
						stmt, err := db.Prepare(q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						rows, err := stmt.Query()
						queryCoreRows(t, rows, err, []string{"b", "id"}, []string{"INT8", "INT4"}, want, false)
					} else {
						rows, err := db.Query(q)
						queryCoreRows(t, rows, err, []string{"b", "id"}, []string{"INT8", "INT4"}, want, false)
					}
					var next int64
					if err := db.QueryRow("SELECT nextval('s')").Scan(&next); err != nil || next != 3 {
						t.Fatal(next, err)
					}
				})
			}
		}
	}
	check(db, "original")
	restored := openLocal(t, stuntgres.NewDriver(), t.Name()+"_restored")
	if err := stuntgres.Restore(context.Background(), restored, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	check(restored, "restored")
}
