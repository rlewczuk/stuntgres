package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"reflect"

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
	db, err := sql.Open("stuntgres", "smoke")
	if err != nil {
		return err
	}
	defer db.Close()
	second, err := sql.Open("stuntgres", "smoke")
	if err != nil {
		return err
	}
	defer second.Close()
	if err = db.PingContext(ctx); err != nil {
		return err
	}
	if err = second.PingContext(ctx); err != nil {
		return err
	}
	if err = stuntgres.Reset(ctx, db); err != nil {
		return err
	}
	if _, err = db.ExecContext(ctx, "CREATE TABLE people (id INTEGER NOT NULL, name TEXT, active BOOLEAN NOT NULL)"); err != nil {
		return err
	}
	stmt, err := db.PrepareContext(ctx, "INSERT INTO people (id, name, active) VALUES ($1, $2, $3)")
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, values := range [][]any{{2, "O'Brien", false}, {1, "Ada", true}} {
		result, e := stmt.ExecContext(ctx, values...)
		if e != nil {
			return e
		}
		count, e := result.RowsAffected()
		if e != nil {
			return e
		}
		if count != 1 {
			return fmt.Errorf("insert count: got %d, want 1", count)
		}
	}
	expected := [][]any{{int64(1), "Ada", true}, {int64(2), "O'Brien", false}}
	verify := func(handle *sql.DB) error {
		rows, e := handle.QueryContext(ctx, "SELECT id, name, active FROM people ORDER BY id")
		if e != nil {
			return e
		}
		defer rows.Close()
		names, e := rows.Columns()
		if e != nil {
			return e
		}
		if !reflect.DeepEqual(names, []string{"id", "name", "active"}) {
			return fmt.Errorf("columns: %v", names)
		}
		types, e := rows.ColumnTypes()
		if e != nil {
			return e
		}
		for i, want := range []string{"INT4", "TEXT", "BOOL"} {
			if types[i].DatabaseTypeName() != want {
				return fmt.Errorf("column type: got %s, want %s", types[i].DatabaseTypeName(), want)
			}
		}
		var actual [][]any
		for rows.Next() {
			var id int64
			var name string
			var active bool
			if e = rows.Scan(&id, &name, &active); e != nil {
				return e
			}
			actual = append(actual, []any{id, name, active})
		}
		if e = rows.Err(); e != nil {
			return e
		}
		if !reflect.DeepEqual(actual, expected) {
			return fmt.Errorf("rows: got %v, want %v", actual, expected)
		}
		return nil
	}
	if err = verify(db); err != nil {
		return err
	}
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		return err
	}
	if err = stuntgres.Reset(ctx, db); err != nil {
		return err
	}
	rows, err := second.QueryContext(ctx, "SELECT id FROM people")
	if rows != nil {
		rows.Close()
	}
	var state interface{ SQLState() string }
	if !errors.As(err, &state) || state.SQLState() != "42P01" {
		return fmt.Errorf("reset: expected 42P01, got %v", err)
	}
	if err = stuntgres.Restore(ctx, db, &dump); err != nil {
		return err
	}
	if err = verify(second); err != nil {
		return err
	}
	fmt.Print("1 Ada true\n2 O'Brien false\nreset: 42P01\nrestore: 2 rows\n")
	return nil
}
