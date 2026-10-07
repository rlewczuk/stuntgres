package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"math"
	"stuntgres"
	"testing"
)

func TestQueryOrderViewUsingOverrideRegression(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, q := range []string{
		"CREATE TABLE a(id INT,k VARCHAR(2))", "CREATE TABLE b(k VARCHAR(3))",
		"CREATE SEQUENCE s START WITH 1",
		"CREATE VIEW collision(k,id) AS SELECT nextval('s') AS n,a.id FROM a JOIN b USING(k) ORDER BY k,a.id",
		"CREATE VIEW merged_usage AS SELECT k,a.id FROM a JOIN b USING(k) ORDER BY k,a.id",
	} {
		execSQL(t, db, q)
	}
	for _, name := range []string{"collision", "merged_usage"} {
		var definition string
		if err := db.QueryRow("SELECT definition FROM pg_catalog.pg_views WHERE viewname='" + name + "'").Scan(&definition); err != nil {
			t.Fatal(err)
		}
		execSQL(t, db, "CREATE VIEW replay_"+name+" AS "+definition)
	}
	read := func(t *testing.T, db *sql.DB, name string, prepared, empty bool) {
		t.Helper()
		q := "SELECT k,id FROM " + name + " ORDER BY id"
		var rows *sql.Rows
		var err error
		if prepared {
			stmt, failure := db.Prepare(q)
			if failure != nil {
				t.Fatal(failure)
			}
			defer stmt.Close()
			rows, err = stmt.Query()
		} else {
			rows, err = db.Query(q)
		}
		want := [][]any{{int64(2), int64(1)}, {int64(1), int64(2)}}
		if empty {
			want = [][]any{}
		}
		queryCoreRows(t, rows, err, []string{"k", "id"}, []string{"INT8", "INT4"}, want, false)
		var next int64
		nextWant := int64(3)
		if empty {
			nextWant = 1
		}
		if err := db.QueryRow("SELECT nextval('s')").Scan(&next); err != nil || next != nextWant {
			t.Fatal(next, nextWant, err)
		}
	}
	metadata := func(t *testing.T, db *sql.DB, empty bool) {
		for _, prefix := range []string{"", "replay_"} {
			for _, prepared := range []bool{false, true} {
				q := "SELECT k,id FROM " + prefix + "merged_usage ORDER BY id"
				var rows *sql.Rows
				var err error
				var held *sql.Stmt
				if prepared {
					held, err = db.Prepare(q)
					if err != nil {
						t.Fatal(err)
					}
					rows, err = held.Query()
				} else {
					rows, err = db.Query(q)
				}
				if err != nil {
					t.Fatal(err)
				}
				types, err := rows.ColumnTypes()
				if err != nil || len(types) != 2 {
					rows.Close()
					t.Fatal(types, err)
				}
				// VARCHAR(2)/VARCHAR(3) merge to VARCHAR without typmod. A
				// physical input substitution would incorrectly retain2 or3.
				length, known := types[0].Length()
				if !known || length != math.MaxInt64 {
					rows.Close()
					t.Fatal("lost merged usage", length, known)
				}
				want := [][]any{{"b", int64(1)}, {"a", int64(2)}}
				if empty {
					want = [][]any{}
				}
				queryCoreRows(t, rows, nil, []string{"k", "id"}, []string{"VARCHAR", "INT4"}, want, false)
				if held != nil {
					held.Close()
				}
			}
		}
	}
	metadata(t, db, true)
	for _, name := range []string{"collision", "replay_collision"} {
		for _, prepared := range []bool{false, true} {
			execSQL(t, db, "ALTER SEQUENCE s RESTART WITH 1")
			read(t, db, name, prepared, true)
		}
	}
	execSQL(t, db, "INSERT INTO a VALUES(1,'b'),(2,'a')")
	// One empty operand still binds the synthetic merged key and full schema.
	metadata(t, db, true)
	for _, name := range []string{"collision", "replay_collision"} {
		for _, prepared := range []bool{false, true} {
			execSQL(t, db, "ALTER SEQUENCE s RESTART WITH 1")
			read(t, db, name, prepared, true)
		}
	}
	execSQL(t, db, "ALTER SEQUENCE s RESTART WITH 1")
	var emptySnapshot bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &emptySnapshot); err != nil {
		t.Fatal(err)
	}
	for _, prepared := range []bool{false, true} {
		t.Run("empty_restored"+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
			restored := openLocal(t, stuntgres.NewDriver(), t.Name())
			if err := stuntgres.Restore(context.Background(), restored, bytes.NewReader(emptySnapshot.Bytes())); err != nil {
				t.Fatal(err)
			}
			read(t, restored, "collision", prepared, true)
			execSQL(t, restored, "ALTER SEQUENCE s RESTART WITH 1")
			read(t, restored, "replay_collision", prepared, true)
			metadata(t, restored, true)
		})
	}
	execSQL(t, db, "INSERT INTO b VALUES('a'),('b')")
	execSQL(t, db, "ALTER SEQUENCE s RESTART WITH 1")
	var snapshot bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &snapshot); err != nil {
		t.Fatal(err)
	}
	metadata(t, db, false)
	for _, name := range []string{"collision", "replay_collision"} {
		for _, prepared := range []bool{false, true} {
			t.Run(name+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				execSQL(t, db, "ALTER SEQUENCE s RESTART WITH 1")
				read(t, db, name, prepared, false)
			})
		}
	}
	for _, prepared := range []bool{false, true} {
		t.Run("restored"+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
			restored := openLocal(t, stuntgres.NewDriver(), t.Name())
			if err := stuntgres.Restore(context.Background(), restored, bytes.NewReader(snapshot.Bytes())); err != nil {
				t.Fatal(err)
			}
			// No corrective restart before the first pristine restored read.
			read(t, restored, "collision", prepared, false)
			execSQL(t, restored, "ALTER SEQUENCE s RESTART WITH 1")
			read(t, restored, "replay_collision", prepared, false)
			metadata(t, restored, false)
		})
	}
}
