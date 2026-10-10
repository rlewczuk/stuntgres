package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"stuntgres"
)

// Shared nonSQL diagnostic helper retained after the type-core SQL migration.
func typeCoreDiagnostic(err error, want stuntgres.Error) error {
	want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
	var got *stuntgres.Error
	if !errors.As(err, &got) || *got != want {
		return fmt.Errorf("diagnostic got %#v (%v), want %#v", got, err, want)
	}
	return nil
}

func TestSmoke(t *testing.T) {
	if err := baselineRun(); err != nil {
		t.Fatal(err)
	}
}

func baselineRun() error {
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
	if _, err = db.ExecContext(ctx, "CREATE TABLE public.people (id INTEGER NOT NULL, name TEXT, active BOOLEAN NOT NULL)"); err != nil {
		return err
	}
	stmt, err := db.PrepareContext(ctx, "INSERT INTO public.people (id, name, active) VALUES ($1, $2, $3)")
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
		for _, query := range []string{"SELECT id, name, active FROM public.people ORDER BY id", "SELECT id, name, active FROM people ORDER BY id"} {
			rows, e := handle.QueryContext(ctx, query)
			if e != nil {
				return e
			}
			names, e := rows.Columns()
			if e != nil {
				rows.Close()
				return e
			}
			if !reflect.DeepEqual(names, []string{"id", "name", "active"}) {
				rows.Close()
				return fmt.Errorf("columns: %v", names)
			}
			types, e := rows.ColumnTypes()
			if e != nil {
				rows.Close()
				return e
			}
			for i, want := range []string{"INT4", "TEXT", "BOOL"} {
				if types[i].DatabaseTypeName() != want {
					rows.Close()
					return fmt.Errorf("column type: got %s, want %s", types[i].DatabaseTypeName(), want)
				}
			}
			var actual [][]any
			for rows.Next() {
				var id int64
				var name string
				var active bool
				if e = rows.Scan(&id, &name, &active); e != nil {
					rows.Close()
					return e
				}
				actual = append(actual, []any{id, name, active})
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return e
			}
			if !reflect.DeepEqual(actual, expected) {
				return fmt.Errorf("rows: got %v, want %v", actual, expected)
			}
		}
		return nil
	}
	if err = verify(db); err != nil {
		return err
	}
	if err = verify(second); err != nil {
		return err
	}
	if _, err = db.ExecContext(ctx, "CREATE TABLE public.t (id INTEGER)"); err != nil {
		return err
	}
	checkT := func(query string, wantCount int64) error {
		rows, e := db.QueryContext(ctx, query)
		if e != nil {
			return e
		}
		defer rows.Close()
		types, e := rows.ColumnTypes()
		if e != nil {
			return e
		}
		if len(types) != 1 || types[0].DatabaseTypeName() != "INT4" {
			return fmt.Errorf("qualified catalog metadata: %v", types)
		}
		var count int64
		for rows.Next() {
			var id int64
			if e = rows.Scan(&id); e != nil {
				return e
			}
			if id != 1 {
				return fmt.Errorf("qualified catalog row: %d", id)
			}
			count++
		}
		if e = rows.Err(); e != nil {
			return e
		}
		if count != wantCount {
			return fmt.Errorf("qualified catalog count: got %d, want %d", count, wantCount)
		}
		return nil
	}
	if err = checkT("SELECT id FROM public.t", 0); err != nil {
		return err
	}
	r, e := db.ExecContext(ctx, "INSERT INTO public.t VALUES (1)")
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return fmt.Errorf("qualified insert count: %d", n)
	}
	if err = checkT("SELECT id FROM t", 1); err != nil {
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
	if err = verify(db); err != nil {
		return err
	}
	if err = verify(second); err != nil {
		return err
	}
	return nil
}
