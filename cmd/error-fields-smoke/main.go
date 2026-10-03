package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"os"

	"stuntgres"
)

func run() error {
	driver := stuntgres.NewDriver()
	connector, err := driver.OpenConnector("error-fields-smoke")
	if err != nil {
		return err
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	if err := db.Ping(); err != nil {
		return err
	}
	if _, err := db.Exec("CREATE TABLE diagnostic_rows (id INT NOT NULL,note TEXT)"); err != nil {
		return err
	}
	for _, operation := range []func(string) error{
		func(q string) error { _, e := db.Exec(q); return e },
		func(q string) error { _, e := db.Query(q); return e },
		func(q string) error { _, e := db.Prepare(q); return e },
	} {
		err := operation("INSERT INTO t VALUES ('é', )")
		var got *stuntgres.Error
		if !errors.As(fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", err)), &got) ||
			got.Code != "42601" || got.Severity != "ERROR" || got.SeverityUnlocalized != "ERROR" ||
			got.Message != `syntax error at or near ")"` || got.Position != "28" ||
			got.Detail != "" || got.Hint != "" || got.Schema != "" || got.Table != "" || got.Column != "" {
			return fmt.Errorf("unexpected syntax diagnostic: %#v (%v)", got, err)
		}
	}
	checkNotNull := func(err error) error {
		var got *stuntgres.Error
		if !errors.As(err, &got) || got.Code != "23502" || got.SQLState() != "23502" ||
			got.Severity != "ERROR" || got.SeverityUnlocalized != "ERROR" ||
			got.Message != `null value in column "id" of relation "diagnostic_rows" violates not-null constraint` ||
			got.Detail != "Failing row contains (null, bad)." || got.Schema != "public" ||
			got.Table != "diagnostic_rows" || got.Column != "id" || got.Hint != "" ||
			got.Position != "" || got.InternalPosition != "" || got.InternalQuery != "" ||
			got.Context != "" || got.DataType != "" || got.Constraint != "" || got.File != "" ||
			got.Line != "" || got.Routine != "" {
			return fmt.Errorf("unexpected NOT NULL diagnostic: %#v (%v)", got, err)
		}
		return nil
	}
	_, err = db.Exec("INSERT INTO diagnostic_rows VALUES (NULL,'bad')")
	if failure := checkNotNull(err); failure != nil {
		return failure
	}
	stmt, err := db.Prepare("INSERT INTO diagnostic_rows VALUES ($1,$2)")
	if err != nil {
		return err
	}
	_, err = stmt.Exec(nil, "bad")
	stmt.Close()
	if failure := checkNotNull(err); failure != nil {
		return failure
	}
	_, err = db.Exec("INSERT INTO diagnostic_rows VALUES (1,'good'),(NULL,'bad')")
	if failure := checkNotNull(err); failure != nil {
		return failure
	}
	rows, err := db.Query("SELECT id,note FROM diagnostic_rows ORDER BY id")
	if err != nil {
		return err
	}
	if rows.Next() {
		rows.Close()
		return fmt.Errorf("failed inserts published rows")
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	columns, err := db.Query("SELECT id,note FROM diagnostic_rows")
	if err != nil {
		return err
	}
	columnTypes, err := columns.ColumnTypes()
	columns.Close()
	if err != nil || len(columnTypes) != 2 || columnTypes[0].DatabaseTypeName() != "INT4" || columnTypes[1].DatabaseTypeName() != "TEXT" {
		return fmt.Errorf("unexpected restored row metadata: %v, %v", columnTypes, err)
	}
	if _, err := db.Exec("INSERT INTO diagnostic_rows VALUES (7,'kept')"); err != nil {
		return err
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &dump); err != nil {
		return err
	}
	if err := stuntgres.Reset(context.Background(), db); err != nil {
		return err
	}
	if err := stuntgres.Restore(context.Background(), db, bytes.NewReader(dump.Bytes())); err != nil {
		return err
	}
	_, err = db.Exec("INSERT INTO diagnostic_rows VALUES (NULL,'bad')")
	if failure := checkNotNull(err); failure != nil {
		return failure
	}
	before, err := readRows(db)
	if err != nil {
		return err
	}
	if err := stuntgres.Restore(context.Background(), db, bytes.NewBufferString("CREATE TABLE replacement (x TEXT); INSERT INTO replacement VALUES ('é', )")); err == nil {
		return fmt.Errorf("malformed Restore succeeded")
	} else {
		var got *stuntgres.Error
		if !errors.As(err, &got) || got.Code != "42601" || got.Message != `syntax error at or near ")"` || got.Position != "73" {
			return fmt.Errorf("malformed Restore diagnostic: %#v (%v)", got, err)
		}
	}
	after, err := readRows(db)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(after, [][]any{{int64(7), "kept"}}) {
		return fmt.Errorf("failed Restore changed rows: before=%v after=%v", before, after)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := db.ExecContext(ctx, "SELECT 1"); !errors.Is(err, context.Canceled) {
		return fmt.Errorf("pre-canceled operation = %v", err)
	} else {
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			return fmt.Errorf("context error fabricated SQLSTATE %s", state.SQLState())
		}
	}
	return nil
}

func readRows(db *sql.DB) ([][]any, error) {
	rows, err := db.Query("SELECT id,note FROM diagnostic_rows ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result [][]any
	for rows.Next() {
		var id any
		var note any
		if err := rows.Scan(&id, &note); err != nil {
			return nil, err
		}
		result = append(result, []any{id, note})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return result, nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stdout, "error fields: ok\n")
}
