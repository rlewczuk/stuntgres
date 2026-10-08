package stuntgres_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"stuntgres"
)

func TestBtreeIndexMaintenance(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(n INT,s TEXT)")
	execSQL(t, db, "INSERT INTO t VALUES(2,'b'),(1,'a'),(2,'b'),(NULL,NULL),(3,'c')")
	execSQL(t, db, "CREATE INDEX ni ON t(n DESC NULLS LAST,s ASC NULLS FIRST)")
	execSQL(t, db, "CREATE INDEX si ON t USING btree(s)")
	execSQL(t, db, "INSERT INTO t VALUES(2,'b')")
	stmt, err := db.Prepare("INSERT INTO t VALUES($1,$2)")
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]any{{0, ""}, {nil, nil}} {
		result, err := stmt.Exec(args...)
		if err != nil {
			t.Fatal(err)
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			t.Fatal(count, err)
		}
	}
	stmt.Close()
	execSQL(t, db, "INSERT INTO t SELECT n,s FROM t WHERE n=3")
	rows, err := db.Query("SELECT n,s FROM t ORDER BY n ASC NULLS LAST")
	queryCoreRows(t, rows, err, []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), ""}, {int64(1), "a"}, {int64(2), "b"}, {int64(2), "b"}, {int64(2), "b"}, {int64(3), "c"}, {int64(3), "c"}, {nil, nil}, {nil, nil}}, false)
	for _, tc := range []struct {
		query string
		args  []any
		want  [][]any
	}{
		{"SELECT n,s FROM t WHERE n<$1", []any{2}, [][]any{{int64(0), ""}, {int64(1), "a"}}},
		{"SELECT n,s FROM t WHERE n<=$1", []any{1}, [][]any{{int64(0), ""}, {int64(1), "a"}}},
		{"SELECT n,s FROM t WHERE n>$1", []any{2}, [][]any{{int64(3), "c"}, {int64(3), "c"}}},
		{"SELECT n,s FROM t WHERE n>=$1", []any{3}, [][]any{{int64(3), "c"}, {int64(3), "c"}}},
		{"SELECT n,s FROM t WHERE n=$1", []any{9}, [][]any{}},
	} {
		statement, err := db.Prepare(tc.query)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := statement.Query(tc.args...)
		queryCoreRows(t, rows, err, []string{"n", "s"}, []string{"INT4", "TEXT"}, tc.want, true)
		statement.Close()
	}
	execSQL(t, db, "UPDATE t SET n=10 WHERE n=1")
	execSQL(t, db, "DELETE FROM t WHERE n=3")
	wantTuples(t, db, "SELECT n FROM t ORDER BY n ASC NULLS FIRST", [][]any{{nil}, {nil}, {int64(0)}, {int64(2)}, {int64(2)}, {int64(2)}, {int64(10)}})
}
func TestBtreeIndexDiagnosticsAndNameBinding(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(n INT)")
	execSQL(t, db, "CREATE SEQUENCE seq")
	execSQL(t, db, "CREATE VIEW v AS SELECT n FROM t")
	execSQL(t, db, "CREATE INDEX idx ON t(n)")
	for _, tc := range []struct{ query, code, message, detail string }{
		{"CREATE INDEX retry ON public.missing(n)", "42P01", `relation "public.missing" does not exist`, ""},
		{"CREATE INDEX retry ON absent.t(n)", "3F000", `schema "absent" does not exist`, ""},
		{"CREATE INDEX idx ON t(missing)", "42703", `column "missing" does not exist`, ""},
		{"CREATE INDEX IF NOT EXISTS idx ON t(missing)", "42703", `column "missing" does not exist`, ""},
		{"CREATE INDEX retry ON seq(n)", "42809", `cannot create index on relation "seq"`, "This operation is not supported for sequences."},
		{"CREATE INDEX retry ON v(n)", "42809", `cannot create index on relation "v"`, "This operation is not supported for views."},
		{"CREATE INDEX retry ON idx(n)", "42809", `cannot open relation "idx"`, "This operation is not supported for indexes."},
		{"CREATE INDEX retry ON t USING absent(n)", "42704", `access method "absent" does not exist`, ""},
		{"CREATE INDEX retry ON t(ctid)", "0A000", "index creation on system columns is not supported", ""},
		{"CREATE INDEX retry ON t(" + strings.TrimSuffix(strings.Repeat("n,", 33), ",") + ")", "54011", "cannot use more than 32 columns in an index", ""},
	} {
		want := stuntgres.Error{Code: tc.code, Message: tc.message, Detail: tc.detail}
		_, err := db.Exec(tc.query)
		typeCoreDiagnostic(t, err, want)
		stmt, err := db.Prepare(tc.query)
		if err != nil {
			t.Fatal("semantic error at prepare", tc.query, err)
		}
		_, err = stmt.Exec()
		stmt.Close()
		typeCoreDiagnostic(t, err, want)
		wantTuples(t, db, "SELECT relname FROM pg_catalog.pg_class WHERE relname='retry'", [][]any{})
	}
	execSQL(t, db, "CREATE INDEX retry ON t(n)")
	execSQL(t, db, "CREATE INDEX ON t(n)")
	execSQL(t, db, "CREATE INDEX ON t(n)")
	wantTuples(t, db, "SELECT relname FROM pg_catalog.pg_class WHERE relname='t_n_idx' OR relname='t_n_idx1' ORDER BY relname", [][]any{{"t_n_idx"}, {"t_n_idx1"}})
	held, err := db.Prepare("CREATE INDEX held ON later(n)")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	_, err = held.Exec()
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "later" does not exist`})
	execSQL(t, db, "CREATE TABLE later(n INT)")
	if _, err = held.Exec(); err != nil {
		t.Fatal("prepared utility did not rebind", err)
	}
}
func TestBtreeIndexControls(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(n INT,s TEXT)")
	execSQL(t, db, "INSERT INTO t VALUES(2,'b'),(NULL,NULL),(2,'b')")
	execSQL(t, db, "CREATE INDEX idx ON t(n DESC NULLS LAST,s ASC NULLS FIRST)")
	execSQL(t, db, "ALTER TABLE t RENAME n TO key")
	execSQL(t, db, "ALTER TABLE t RENAME TO renamed")
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	definition := `CREATE INDEX "idx" ON "public"."renamed" USING btree ("key" DESC NULLS LAST, "s" ASC NULLS FIRST);`
	if !strings.Contains(dump.String(), definition) {
		t.Fatal("missing actual current definition", dump.String())
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT relname FROM pg_catalog.pg_class WHERE relname='idx'", [][]any{})
	if err := stuntgres.Restore(ctx, db, strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, "INSERT INTO renamed VALUES(3,'c')")
	wantTuples(t, db, "SELECT key,s FROM renamed ORDER BY key DESC NULLS LAST", [][]any{{int64(3), "c"}, {int64(2), "b"}, {int64(2), "b"}, {nil, nil}})
	broken := dump.String() + "CREATE INDEX broken ON renamed(missing);\n"
	if err := stuntgres.Restore(ctx, db, strings.NewReader(broken)); err == nil {
		t.Fatal("invalid restore succeeded")
	}
	wantTuples(t, db, "SELECT key FROM renamed ORDER BY key DESC NULLS LAST", [][]any{{int64(3)}, {int64(2)}, {int64(2)}, {nil}})
	wantTuples(t, db, "SELECT relkind FROM pg_catalog.pg_class WHERE relname='idx'", [][]any{{"i"}})
}
func TestBtreeIndexUtilityBoundary(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(1)
	execSQL(t, db, "CREATE TABLE t(n INT)")
	for _, readOnly := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			tx, err := db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if readOnly {
				if _, err := tx.Exec("SET TRANSACTION READ ONLY"); err != nil {
					t.Fatal(err)
				}
			}
			query := "CREATE INDEX idx ON t(n)"
			want := stuntgres.Error{Code: "0A000", Message: "statement is unsupported in an explicit transaction"}
			if readOnly {
				query = "CREATE INDEX idx ON missing(n)"
				want = stuntgres.Error{Code: "25006", Message: "cannot execute CREATE INDEX in a read-only transaction"}
			}
			if prepared {
				stmt, prepareErr := tx.Prepare(query)
				if prepareErr != nil {
					t.Fatal("syntax-only utility Prepare", prepareErr)
				}
				_, err = stmt.Exec()
				stmt.Close()
			} else {
				_, err = tx.Exec(query)
			}
			typeCoreDiagnostic(t, err, want)
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
		}
	}
	wantTuples(t, db, "SELECT relname FROM pg_catalog.pg_class WHERE relname='idx'", [][]any{})
}

func TestBtreeIndexDistinctOwnerControls(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(1)
	execSQL(t, db, "CREATE USER index_schema_owner")
	execSQL(t, db, "CREATE USER index_table_owner")
	seed := `CREATE SCHEMA s AUTHORIZATION index_schema_owner; SET SESSION AUTHORIZATION index_table_owner; CREATE TABLE s.t(n INT,s TEXT); INSERT INTO s.t VALUES(2,'b'),(NULL,NULL),(2,'b'); CREATE INDEX idx ON s.t(n DESC NULLS LAST,s ASC NULLS FIRST);`
	if err := stuntgres.Restore(ctx, db, strings.NewReader(seed)); err != nil {
		t.Fatal("trusted split-owner replay", err)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`CREATE SCHEMA "s" AUTHORIZATION "index_schema_owner";`, `SET SESSION AUTHORIZATION 'index_table_owner';`, `CREATE INDEX "idx" ON "s"."t" USING btree ("n" DESC NULLS LAST, "s" ASC NULLS FIRST);`} {
		if !strings.Contains(dump.String(), want) {
			t.Fatal(want, dump.String())
		}
	}
	for range 2 {
		if err := stuntgres.Reset(ctx, db); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(ctx, db, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		wantTuples(t, db, "SELECT current_user", [][]any{{"postgres"}})
		wantTuples(t, db, "SELECT n,s FROM s.t ORDER BY n DESC NULLS LAST", [][]any{{int64(2), "b"}, {int64(2), "b"}, {nil, nil}})
		execSQL(t, db, "SET SESSION AUTHORIZATION index_table_owner")
		_, err := db.Exec("CREATE INDEX denied ON s.t(n)")
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42501", Message: "permission denied for schema s"})
		execSQL(t, db, "RESET SESSION AUTHORIZATION")
		execSQL(t, db, "INSERT INTO s.t VALUES(3,'c')")
		wantTuples(t, db, "SELECT n FROM s.t ORDER BY n DESC NULLS LAST", [][]any{{int64(3)}, {int64(2)}, {int64(2)}, {nil}})
	}
}

func TestBtreeIndexHeldRowsAfterReset(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, q := range []string{"CREATE TABLE t(n INT)", "INSERT INTO t VALUES(2),(1)", "CREATE INDEX idx ON t(n)"} {
		execSQL(t, db, q)
	}
	rows, err := db.Query("SELECT n FROM t ORDER BY n")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"CREATE TABLE t(s TEXT)", "INSERT INTO t VALUES('new')", "CREATE INDEX idx ON t(s)"} {
		execSQL(t, db, q)
	}
	for _, want := range []int64{1, 2} {
		if !rows.Next() {
			t.Fatal("old eager row missing", rows.Err())
		}
		var got int64
		if err := rows.Scan(&got); err != nil || got != want {
			t.Fatal(got, want, err)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	wantTuples(t, db, "SELECT s FROM t", [][]any{{"new"}})
}
