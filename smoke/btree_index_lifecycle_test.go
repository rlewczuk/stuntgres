package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
)

func btreeSmokeDB(t *testing.T) *sql.DB {
	t.Helper()
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	t.Cleanup(func() { db.Close() })
	return db
}
func btreeSmokeExec(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	if _, err := db.Exec(query); err != nil {
		t.Fatal(query, err)
	}
}
func btreeSmokeRows(t *testing.T, db *sql.DB, query string, want [][]any) {
	t.Helper()
	rows, err := db.Query(query)
	if err != nil {
		t.Fatal(query, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	got := make([][]any, 0)
	for rows.Next() {
		row := make([]any, len(columns))
		targets := make([]any, len(row))
		for i := range row {
			targets[i] = &row[i]
		}
		if err := rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if want == nil {
		want = make([][]any, 0)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal(query, got, want)
	}
}
func TestBtreeIndexControlsSmoke(t *testing.T) {
	ctx := context.Background()
	db := btreeSmokeDB(t)
	for _, q := range []string{"CREATE TABLE t(n INT,s TEXT)", "INSERT INTO t VALUES(2,'b'),(NULL,NULL),(2,'b')", "CREATE INDEX idx ON t(n DESC NULLS LAST,s ASC NULLS FIRST)", "ALTER TABLE t RENAME n TO key", "ALTER TABLE t RENAME TO renamed"} {
		btreeSmokeExec(t, db, q)
	}
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
	btreeSmokeRows(t, db, "SELECT relname FROM pg_catalog.pg_class WHERE relname='idx'", nil)
	if err := stuntgres.Restore(ctx, db, strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	btreeSmokeExec(t, db, "INSERT INTO renamed VALUES(3,'c')")
	btreeSmokeRows(t, db, "SELECT key,s FROM renamed ORDER BY key DESC NULLS LAST", [][]any{{int64(3), "c"}, {int64(2), "b"}, {int64(2), "b"}, {nil, nil}})
	if err := stuntgres.Restore(ctx, db, strings.NewReader(dump.String()+"CREATE INDEX bad ON renamed(missing);")); err == nil {
		t.Fatal("late bad index replaced destination")
	}
	btreeSmokeRows(t, db, "SELECT key FROM renamed ORDER BY key DESC NULLS LAST", [][]any{{int64(3)}, {int64(2)}, {int64(2)}, {nil}})
	btreeSmokeRows(t, db, "SELECT relkind FROM pg_catalog.pg_class WHERE relname='idx'", [][]any{{"i"}})
}
func TestBtreeIndexLifecycleSmoke(t *testing.T) {
	db := btreeSmokeDB(t)
	for _, q := range []string{"CREATE TABLE t(extra INT,n INT,s TEXT)", "INSERT INTO t VALUES(0,1,'a'),(0,2,'b'),(0,10,'c')", "CREATE INDEX ni ON t(n)", "CREATE INDEX si ON t(s)", "UPDATE t SET n=3 WHERE n=2", "DELETE FROM t WHERE n=1", "ALTER TABLE t RENAME n TO key", "ALTER TABLE t RENAME TO renamed", "ALTER TABLE renamed DROP extra", "ALTER TABLE renamed ALTER key TYPE TEXT USING key::text", "CREATE SCHEMA moved", "CREATE TABLE moved.ni(n INT)"} {
		btreeSmokeExec(t, db, q)
	}
	if _, err := db.Exec("ALTER TABLE renamed SET SCHEMA moved"); err == nil {
		t.Fatal("index destination collision succeeded")
	}
	btreeSmokeRows(t, db, "SELECT key FROM renamed ORDER BY key", [][]any{{"10"}, {"3"}})
	btreeSmokeExec(t, db, "DROP TABLE moved.ni")
	btreeSmokeExec(t, db, "ALTER TABLE renamed SET SCHEMA moved")
	btreeSmokeRows(t, db, "SELECT key FROM moved.renamed ORDER BY key", [][]any{{"10"}, {"3"}})
	btreeSmokeExec(t, db, "ALTER TABLE moved.renamed DROP s")
	btreeSmokeRows(t, db, "SELECT relname FROM pg_catalog.pg_class WHERE relname='si'", nil)
	btreeSmokeExec(t, db, "DROP TABLE moved.renamed RESTRICT")
	btreeSmokeRows(t, db, "SELECT relname FROM pg_catalog.pg_class WHERE relname='ni'", nil)
	btreeSmokeExec(t, db, "CREATE TABLE moved.ni(n INT)")
}
