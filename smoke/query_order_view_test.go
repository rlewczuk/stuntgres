package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"stuntgres"
	"testing"
)

func TestQueryOrderViewSmoke(t *testing.T) {
	ctx := context.Background()
	open := func(name string) *sql.DB {
		connector, err := stuntgres.NewDriver().OpenConnector(name)
		if err != nil {
			t.Fatal(err)
		}
		db := sql.OpenDB(connector)
		t.Cleanup(func() { db.Close() })
		return db
	}
	db := open(t.Name())
	for _, q := range []string{
		"CREATE TABLE t(id INT,a INT)", "INSERT INTO t VALUES(1,2),(2,1),(3,NULL)",
		"CREATE VIEW alias_order(k,id) AS SELECT a+0 AS x,id FROM t ORDER BY x DESC NULLS LAST,id",
		"CREATE VIEW hidden_order AS SELECT id FROM t ORDER BY COALESCE(a,0)+1 DESC NULLS LAST,id",
		"CREATE VIEW ordinal_order AS SELECT id,a FROM t ORDER BY 2 NULLS FIRST,1 DESC",
		"ALTER TABLE t RENAME COLUMN a TO renamed",
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	check := func(db *sql.DB) {
		for _, tc := range []struct {
			q            string
			names, types []string
			want         [][]any
		}{
			{"SELECT k,id FROM alias_order ORDER BY id", []string{"k", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(1)}, {int64(1), int64(2)}, {nil, int64(3)}}},
			{"SELECT id FROM hidden_order ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}},
			{"SELECT id,a FROM ordinal_order ORDER BY id", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(1)}, {int64(3), nil}}},
		} {
			rows, err := db.QueryContext(ctx, tc.q)
			orderSmokeRows(t, rows, err, tc.names, tc.types, tc.want)
			stmt, err := db.PrepareContext(ctx, tc.q)
			if err != nil {
				t.Fatal(err)
			}
			rows, err = stmt.QueryContext(ctx)
			orderSmokeRows(t, rows, err, tc.names, tc.types, tc.want)
			stmt.Close()
		}
	}
	check(db)
	for _, q := range []string{
		"CREATE SEQUENCE s START WITH 1",
		"CREATE VIEW volatile_order AS SELECT nextval('s') AS n FROM t ORDER BY n DESC",
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	rows, err := db.QueryContext(ctx, "SELECT n FROM volatile_order ORDER BY n DESC")
	orderSmokeRows(t, rows, err, []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}, {int64(2)}, {int64(1)}})
	var next int64
	if err := db.QueryRowContext(ctx, "SELECT nextval('s')").Scan(&next); err != nil || next != 4 {
		t.Fatal(next, err)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	restored := open(t.Name() + "_restored")
	if err := stuntgres.Restore(ctx, restored, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	check(restored)
	stmt, err := restored.PrepareContext(ctx, "SELECT n FROM volatile_order ORDER BY n DESC")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	rows, err = stmt.QueryContext(ctx)
	orderSmokeRows(t, rows, err, []string{"n"}, []string{"INT8"}, [][]any{{int64(7)}, {int64(6)}, {int64(5)}})
	if err := restored.QueryRowContext(ctx, "SELECT nextval('s')").Scan(&next); err != nil || next != 8 {
		t.Fatal(next, err)
	}
	t.Run("hidden_input_override", func(t *testing.T) {
		db := open(t.Name())
		exec := func(q string) {
			t.Helper()
			if _, err := db.ExecContext(ctx, q); err != nil {
				t.Fatal(q, err)
			}
		}
		for _, q := range []string{
			"CREATE TABLE t(id INT,b INT)", "INSERT INTO t VALUES(1,2),(2,1)",
			"CREATE SEQUENCE s START WITH 1",
			"CREATE VIEW collision(b,id) AS SELECT nextval('s') AS n,id FROM t ORDER BY b,id",
		} {
			exec(q)
		}
		var definition string
		if err := db.QueryRowContext(ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname='collision'").Scan(&definition); err != nil {
			t.Fatal(err)
		}
		exec("CREATE VIEW replay AS " + definition)
		// Persist pristine generator state before any view read.
		var snapshot bytes.Buffer
		if err := stuntgres.Dump(ctx, db, &snapshot); err != nil {
			t.Fatal(err)
		}
		read := func(db *sql.DB, name string, prepared bool) {
			t.Helper()
			q := "SELECT b,id FROM " + name + " ORDER BY id"
			want := [][]any{{int64(2), int64(1)}, {int64(1), int64(2)}}
			if prepared {
				stmt, err := db.PrepareContext(ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				rows, err := stmt.QueryContext(ctx)
				orderSmokeRows(t, rows, err, []string{"b", "id"}, []string{"INT8", "INT4"}, want)
				stmt.Close()
			} else {
				rows, err := db.QueryContext(ctx, q)
				orderSmokeRows(t, rows, err, []string{"b", "id"}, []string{"INT8", "INT4"}, want)
			}
			var next int64
			if err := db.QueryRowContext(ctx, "SELECT nextval('s')").Scan(&next); err != nil || next != 3 {
				t.Fatal(name, next, err)
			}
		}
		for _, name := range []string{"collision", "replay"} {
			for _, prepared := range []bool{false, true} {
				exec("ALTER SEQUENCE s RESTART WITH 1")
				read(db, name, prepared)
			}
		}
		for _, prepared := range []bool{false, true} {
			restored := open(t.Name() + map[bool]string{false: "_direct", true: "_prepared"}[prepared])
			if err := stuntgres.Restore(ctx, restored, bytes.NewReader(snapshot.Bytes())); err != nil {
				t.Fatal(err)
			}
			// No restart after Restore: verify its untouched START1 state.
			read(restored, "collision", prepared)
		}
	})
	t.Run("numeric_unary_plus", func(t *testing.T) {
		db := open(t.Name())
		exec := func(q string) {
			t.Helper()
			if _, err := db.ExecContext(ctx, q); err != nil {
				t.Fatal(q, err)
			}
		}
		exec("CREATE TABLE t(id INT)")
		exec("INSERT INTO t VALUES(3),(1),(2),(2)")
		names := []string{"fraction", "exponent", "large"}
		for i, key := range []string{"+1.5", "+1e0", "+2147483648"} {
			exec("CREATE VIEW " + names[i] + " AS SELECT id FROM t ORDER BY " + key + ",id DESC")
			var definition string
			if err := db.QueryRowContext(ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname='"+names[i]+"'").Scan(&definition); err != nil {
				t.Fatal(err)
			}
			exec("CREATE VIEW replay_" + names[i] + " AS " + definition)
		}
		read := func(db *sql.DB) {
			for _, prefix := range []string{"", "replay_"} {
				for _, name := range names {
					q := "SELECT id FROM " + prefix + name + " ORDER BY id DESC"
					want := [][]any{{int64(3)}, {int64(2)}, {int64(2)}, {int64(1)}}
					rows, err := db.QueryContext(ctx, q)
					orderSmokeRows(t, rows, err, []string{"id"}, []string{"INT4"}, want)
					stmt, err := db.PrepareContext(ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					rows, err = stmt.QueryContext(ctx)
					orderSmokeRows(t, rows, err, []string{"id"}, []string{"INT4"}, want)
					stmt.Close()
				}
			}
		}
		read(db)
		var snapshot bytes.Buffer
		if err := stuntgres.Dump(ctx, db, &snapshot); err != nil {
			t.Fatal(err)
		}
		restored := open(t.Name() + "_restored")
		if err := stuntgres.Restore(ctx, restored, bytes.NewReader(snapshot.Bytes())); err != nil {
			t.Fatal(err)
		}
		read(restored)
	})
}
