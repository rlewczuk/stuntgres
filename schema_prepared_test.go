package stuntgres_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"testing"
)

func TestSchemaPreparedFrozenContracts(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "schema-prepared")
	ctx := context.Background()
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	command := func(q string) {
		t.Helper()
		if _, err := c.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	for _, q := range []string{"CREATE SCHEMA s1 CREATE TABLE t(id INT)", "CREATE SCHEMA s2 CREATE TABLE t(id INT)", "CREATE SCHEMA text_shape CREATE TABLE t(id TEXT)", "CREATE SCHEMA label_shape CREATE TABLE t(other INT)", "CREATE SCHEMA count_shape CREATE TABLE t(id INT,note TEXT)", "INSERT INTO s1.t VALUES(7)", "INSERT INTO s2.t VALUES(9)", "SET search_path TO s1"} {
		command(q)
	}
	selectStmt, err := c.PrepareContext(ctx, "SELECT * FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer selectStmt.Close()
	insertStmt, err := c.PrepareContext(ctx, "INSERT INTO t VALUES($1)")
	if err != nil {
		t.Fatal(err)
	}
	defer insertStmt.Close()
	read := func(want int64) {
		t.Helper()
		rows, err := selectStmt.QueryContext(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		types, err := rows.ColumnTypes()
		if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" {
			t.Fatal(types, err)
		}
		if !rows.Next() {
			t.Fatal(rows.Err())
		}
		var got int64
		if err = rows.Scan(&got); err != nil || got != want {
			t.Fatal(got, want, err)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal(rows.Err())
		}
	}
	read(7)
	command("SET search_path TO s2")
	read(9)
	result, err := insertStmt.ExecContext(ctx, 12)
	if err != nil {
		t.Fatal(err)
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		t.Fatal(count, err)
	}
	var inserted int64
	if err = c.QueryRowContext(ctx, "SELECT id FROM s2.t WHERE id=12").Scan(&inserted); err != nil || inserted != 12 {
		t.Fatal(inserted, err)
	}
	absent, err := c.QueryContext(ctx, "SELECT id FROM s1.t WHERE id=12")
	if err != nil {
		t.Fatal(err)
	}
	if absent.Next() || absent.Err() != nil {
		t.Fatal("compatible INSERT used old namespace", absent.Err())
	}
	absent.Close()
	failed := func(err error, message string) {
		t.Helper()
		var e *stuntgres.Error
		want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: message}
		if !errors.As(err, &e) || *e != want {
			t.Fatalf("diagnostic %#v (%v) want %#v", e, err, want)
		}
	}
	for _, schema := range []string{"text_shape", "label_shape", "count_shape"} {
		command("SET search_path TO " + schema)
		rows, err := selectStmt.QueryContext(ctx)
		if rows != nil {
			rows.Close()
			t.Fatal("incompatible returned cursor")
		}
		failed(err, "cached plan must not change result type")
	}
	command("SET search_path TO text_shape")
	result, err = insertStmt.ExecContext(ctx, 7)
	if err != nil {
		t.Fatal("retained INT4 input into TEXT", err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	var text string
	if err = c.QueryRowContext(ctx, "SELECT id FROM text_shape.t").Scan(&text); err != nil || text != "7" {
		t.Fatal(text, err)
	}
	command("SET search_path TO s1")
	if err = stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	command("CREATE SCHEMA s1 CREATE TABLE t(id INT)")
	command("INSERT INTO s1.t VALUES(11)")
	read(11)
}

func TestSchemaTransactionBoundary(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "schema-transaction")
	for _, q := range []string{"CREATE SCHEMA transaction_schema", "ALTER SCHEMA public RENAME TO transaction_schema", "DROP SCHEMA public"} {
		tx, err := db.BeginTx(context.Background(), &sql.TxOptions{})
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(q)
		sqlState(t, err, "0A000")
		if err = tx.Rollback(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("CREATE TABLE transaction_schema.t(id INT)"); err == nil {
		t.Fatal("explicit CREATE leaked")
	}
}

func TestSchemaScalarAndLockIntegration(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, q := range []string{
		"CREATE SCHEMA scalar_first CREATE TABLE t(id INT, flag BOOL)",
		"CREATE SCHEMA scalar_second CREATE TABLE t(id INT, flag BOOL)",
		"INSERT INTO scalar_first.t VALUES(7,TRUE)",
		"INSERT INTO scalar_second.t VALUES(9,TRUE)",
		"SET search_path TO scalar_first",
	} {
		if _, err := c.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	held, err := c.PrepareContext(ctx, "SELECT id, 11, $1 FROM t WHERE flag")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	read := func(want int64) {
		t.Helper()
		var id, literal int64
		var parameter string
		if err := held.QueryRowContext(ctx, "bound").Scan(&id, &literal, &parameter); err != nil {
			t.Fatal(err)
		}
		if id != want || literal != 11 || parameter != "bound" {
			t.Fatal(id, literal, parameter)
		}
	}
	read(7)
	if _, err := c.ExecContext(ctx, "SET search_path TO scalar_second"); err != nil {
		t.Fatal(err)
	}
	read(9)
	// LOCK Prepare remains metadata-only, including a missing schema target.
	missing, err := c.PrepareContext(ctx, "LOCK TABLE missing_schema.t IN ACCESS EXCLUSIVE MODE")
	if err != nil {
		t.Fatal(err)
	}
	missing.Close()
	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, q := range []string{"LOCK TABLE t IN ACCESS EXCLUSIVE MODE", "LOCK TABLE scalar_first.t IN ACCESS EXCLUSIVE MODE"} {
		result, err := tx.ExecContext(ctx, q)
		if err != nil {
			t.Fatal(q, err)
		}
		if n, err := result.RowsAffected(); err != nil || n != 0 {
			t.Fatal(n, err)
		}
	}
	var id, literal int64
	if err := tx.QueryRowContext(ctx, "SELECT id, 11 FROM t WHERE flag").Scan(&id, &literal); err != nil || id != 9 || literal != 11 {
		t.Fatal(id, literal, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
