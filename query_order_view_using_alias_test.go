package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"stuntgres"
	"testing"
)

func TestQueryOrderViewUsingAliasNamespaces(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"reserved", "SELECT nextval('s') AS n,__view_using_0.id FROM a AS __view_using_0 JOIN b AS __view_using_0_1 USING(k,j) ORDER BY k,j,__view_using_0.id"},
		{"explicit", "SELECT nextval('s') AS n,a.id FROM a JOIN b USING(k,j) AS merged ORDER BY merged.k,merged.j,a.id"},
		{"whole_join", "SELECT nextval('s') AS n,q.id FROM (a JOIN b USING(k,j)) AS q(k,j,id) ORDER BY q.k,q.j,q.id"},
		{"nested", "SELECT nextval('s') AS n,a.id FROM (a JOIN b USING(k,j)) JOIN c USING(k,j) ORDER BY k,j,a.id"},
		{"natural", "SELECT nextval('s') AS n,a.id FROM a NATURAL JOIN b ORDER BY k,j,a.id"},
		{"mixed_slots", "SELECT nextval('s') AS n,a.id FROM a JOIN b USING(k,j) ORDER BY k,j,a.id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			jWidth := "3"
			if tc.name == "mixed_slots" {
				jWidth = "2"
			}
			for _, q := range []string{
				"CREATE TABLE a(id INT,k VARCHAR(2),j VARCHAR(2))", "CREATE TABLE b(k VARCHAR(3),j VARCHAR(" + jWidth + "))", "CREATE TABLE c(k VARCHAR(4),j VARCHAR(4))",
				"INSERT INTO a VALUES(1,'b','z'),(2,'a','y')", "INSERT INTO b VALUES('a','y'),('b','z')", "INSERT INTO c VALUES('a','y'),('b','z')",
				"CREATE SEQUENCE s START WITH 1", "CREATE VIEW collision(k,id) AS " + tc.body,
			} {
				execSQL(t, db, q)
			}
			var definition string
			if err := db.QueryRow("SELECT definition FROM pg_catalog.pg_views WHERE viewname='collision'").Scan(&definition); err != nil {
				t.Fatal(err)
			}
			execSQL(t, db, "CREATE VIEW replay AS "+definition)
			var snapshot bytes.Buffer
			if err := stuntgres.Dump(context.Background(), db, &snapshot); err != nil {
				t.Fatal(err)
			}
			read := func(db *sql.DB, name string, prepared bool) {
				q := "SELECT k,id FROM " + name + " ORDER BY id"
				want := [][]any{{int64(2), int64(1)}, {int64(1), int64(2)}}
				if prepared {
					stmt, err := db.Prepare(q)
					if err != nil {
						t.Fatal(err)
					}
					rows, err := stmt.Query()
					queryCoreRows(t, rows, err, []string{"k", "id"}, []string{"INT8", "INT4"}, want, false)
					stmt.Close()
				} else {
					rows, err := db.Query(q)
					queryCoreRows(t, rows, err, []string{"k", "id"}, []string{"INT8", "INT4"}, want, false)
				}
				var next int64
				if err := db.QueryRow("SELECT nextval('s')").Scan(&next); err != nil || next != 3 {
					t.Fatal(next, err)
				}
			}
			for _, name := range []string{"collision", "replay"} {
				for _, prepared := range []bool{false, true} {
					execSQL(t, db, "ALTER SEQUENCE s RESTART WITH 1")
					read(db, name, prepared)
				}
			}
			for _, prepared := range []bool{false, true} {
				restored := openLocal(t, stuntgres.NewDriver(), t.Name()+map[bool]string{false: "_direct", true: "_prepared"}[prepared])
				if err := stuntgres.Restore(context.Background(), restored, bytes.NewReader(snapshot.Bytes())); err != nil {
					t.Fatal(err)
				}
				read(restored, "collision", prepared)
				execSQL(t, restored, "ALTER SEQUENCE s RESTART WITH 1")
				read(restored, "replay", prepared)
			}
		})
	}
}
