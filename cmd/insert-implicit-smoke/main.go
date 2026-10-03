package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"stuntgres"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()
	driver := stuntgres.NewDriver()
	connector, err := driver.OpenConnector("insert-implicit-smoke")
	if err != nil {
		return err
	}
	first := sql.OpenDB(connector)
	defer first.Close()
	second := sql.OpenDB(connector)
	defer second.Close()
	if err = first.PingContext(ctx); err != nil {
		return err
	}
	if err = second.PingContext(ctx); err != nil {
		return err
	}
	if _, err = first.ExecContext(ctx, "CREATE TABLE t(id INT NOT NULL,big BIGINT,label TEXT,flag BOOL)"); err != nil {
		return err
	}
	for _, insert := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO t VALUES (1)", nil},
		{"INSERT INTO t VALUES ($1,$2)", []any{2, "20"}},
		{"INSERT INTO t(label,id) VALUES ('reordered',3)", nil},
	} {
		result, e := first.ExecContext(ctx, insert.query, insert.args...)
		if e != nil {
			return e
		}
		count, e := result.RowsAffected()
		if e != nil {
			return e
		}
		if count != 1 {
			return fmt.Errorf("affected %d rows, want 1", count)
		}
		if _, e = result.LastInsertId(); e == nil {
			return fmt.Errorf("LastInsertId unexpectedly succeeded")
		}
	}
	want := [][]any{{int64(1), nil, nil, nil}, {int64(2), int64(20), nil, nil}, {int64(3), nil, "reordered", nil}}
	verify := func(db *sql.DB) error {
		rows, e := db.QueryContext(ctx, "SELECT * FROM t ORDER BY id")
		if e != nil {
			return e
		}
		defer rows.Close()
		names, e := rows.Columns()
		if e != nil {
			return e
		}
		if !reflect.DeepEqual(names, []string{"id", "big", "label", "flag"}) {
			return fmt.Errorf("column names %v", names)
		}
		types, e := rows.ColumnTypes()
		if e != nil {
			return e
		}
		scan := []reflect.Type{reflect.TypeOf(int64(0)), reflect.TypeOf(int64(0)), reflect.TypeOf(""), reflect.TypeOf(false)}
		for i, name := range []string{"INT4", "INT8", "TEXT", "BOOL"} {
			nullable, known := types[i].Nullable()
			if types[i].DatabaseTypeName() != name || types[i].ScanType() != scan[i] || !known || nullable != (i != 0) {
				return fmt.Errorf("column metadata %d: type=%s scan=%v nullable=%v/%v", i, types[i].DatabaseTypeName(), types[i].ScanType(), nullable, known)
			}
		}
		actual := make([][]any, 0)
		for rows.Next() {
			values := make([]any, 4)
			dest := []any{&values[0], &values[1], &values[2], &values[3]}
			if e = rows.Scan(dest...); e != nil {
				return e
			}
			actual = append(actual, values)
		}
		if e = rows.Err(); e != nil {
			return e
		}
		if !reflect.DeepEqual(actual, want) {
			return fmt.Errorf("rows %v, want %v", actual, want)
		}
		return nil
	}
	if err = verify(first); err != nil {
		return err
	}
	if err = verify(second); err != nil {
		return err
	}
	checkError := func(db *sql.DB, query, code, message, detail string, position int, schema, table, column string) error {
		positionText := ""
		if position != 0 {
			positionText = strconv.Itoa(position)
		}
		_, e := db.ExecContext(ctx, query)
		var got *stuntgres.Error
		if !errors.As(e, &got) || got.Code != code || got.Message != message || got.Detail != detail || got.Hint != "" || got.Position != positionText || got.Schema != schema || got.Table != table || got.Column != column {
			return fmt.Errorf("%s: diagnostic=%+v error=%v", query, got, e)
		}
		return nil
	}
	q := "INSERT INTO t VALUES (4),('oops')"
	if err = checkError(first, q, "22P02", `invalid input syntax for type integer: "oops"`, "", utf8.RuneCountInString(q[:strings.Index(q, "'oops'")])+1, "", "", ""); err != nil {
		return err
	}
	if err = verify(first); err != nil {
		return err
	}
	q = "INSERT INTO t VALUES (4),(5,6)"
	if err = checkError(first, q, "42601", "VALUES lists must all be the same length", "", utf8.RuneCountInString(q[:strings.Index(q, "5,6")])+1, "", "", ""); err != nil {
		return err
	}
	if err = verify(first); err != nil {
		return err
	}
	if _, err = first.ExecContext(ctx, "CREATE TABLE required(id INT,flag BOOL NOT NULL)"); err != nil {
		return err
	}
	if err = checkError(first, "INSERT INTO required VALUES (1)", "23502", `null value in column "flag" of relation "required" violates not-null constraint`, "Failing row contains (1, null).", 0, "public", "required", "flag"); err != nil {
		return err
	}
	rows, err := first.QueryContext(ctx, "SELECT * FROM t ORDER BY id")
	if err != nil {
		return err
	}
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, first, &dump); err != nil {
		rows.Close()
		return err
	}
	if err = stuntgres.Reset(ctx, first); err != nil {
		rows.Close()
		return err
	}
	if err = stuntgres.Restore(ctx, first, bytes.NewReader(dump.Bytes())); err != nil {
		rows.Close()
		return err
	}
	var snapshot [][]any
	for rows.Next() {
		values := make([]any, 4)
		if err = rows.Scan(&values[0], &values[1], &values[2], &values[3]); err != nil {
			rows.Close()
			return err
		}
		snapshot = append(snapshot, values)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if !reflect.DeepEqual(snapshot, want) {
		return fmt.Errorf("retained snapshot %v", snapshot)
	}
	if err = verify(second); err != nil {
		return err
	}
	var again bytes.Buffer
	if err = stuntgres.Dump(ctx, second, &again); err != nil {
		return err
	}
	if dump.String() != again.String() {
		return fmt.Errorf("dump changed after restore")
	}
	if err = stuntgres.Restore(ctx, first, bytes.NewReader([]byte("CREATE TABLE staged(v INT); INSERT INTO staged VALUES(1); CREATE TABLE required_again(v INT NOT NULL); INSERT INTO required_again VALUES(NULL);"))); err == nil {
		return fmt.Errorf("invalid staging restore succeeded")
	}
	if err = verify(second); err != nil {
		return err
	}
	fmt.Print("implicit short INSERT: rows, diagnostics, metadata, atomicity, snapshot and dump/restore OK\n")
	return nil
}
