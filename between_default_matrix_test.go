package stuntgres_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
)

func TestBetweenDefaultCompositionMatrix(t *testing.T) {
	for _, family := range []struct {
		name, a, l, u string
		null          bool
	}{
		{"comparison", "TRUE", "(1<2)", "(2<3)", false},
		{"tested", "(1<2)", "FALSE", "TRUE", false},
		{"nested", "TRUE", "(1 BETWEEN 0 AND 2)", "(2 BETWEEN 1 AND 3)", false},
		{"null", "NULL", "(1<2)", "(2<3)", true},
		{"reversed-inner", "FALSE", "(2 BETWEEN 3 AND 1)", "(1 BETWEEN 0 AND 2)", false},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%v", family.name, prepared), func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				exec := func(query string, want int64) {
					t.Helper()
					var count int64
					if prepared {
						stmt, err := db.Prepare(query)
						if err != nil {
							t.Fatal(err)
						}
						result, err := stmt.Exec()
						stmt.Close()
						if err != nil {
							t.Fatal(err)
						}
						count, err = result.RowsAffected()
						if err != nil {
							t.Fatal(err)
						}
					} else {
						result, err := db.Exec(query)
						if err != nil {
							t.Fatal(err)
						}
						count, err = result.RowsAffected()
						if err != nil {
							t.Fatal(err)
						}
					}
					if count != want {
						t.Fatal(query, count, want)
					}
				}
				definitions := []string{}
				for i, form := range betweenForms {
					definitions = append(definitions, fmt.Sprintf("b%d BOOL DEFAULT (%s %s %s AND %s)", i, family.a, form, family.l, family.u))
				}
				exec("CREATE TABLE d("+strings.Join(definitions, ",")+")", 0)
				exec("INSERT INTO d DEFAULT VALUES", 1)
				row := []any{true, false, true, false}
				if family.null {
					row = []any{nil, nil, nil, nil}
				}
				wantTuples(t, db, "SELECT * FROM d", [][]any{row})
				rows, err := db.Query("SELECT * FROM d")
				if err != nil {
					t.Fatal(err)
				}
				meta, err := rows.ColumnTypes()
				rows.Close()
				if err != nil || len(meta) != 4 {
					t.Fatal(meta, err)
				}
				for i, column := range meta {
					if column.Name() != fmt.Sprintf("b%d", i) || column.DatabaseTypeName() != "BOOL" || column.ScanType() != reflect.TypeFor[bool]() {
						t.Fatal(column)
					}
				}
				ctx := context.Background()
				var dump bytes.Buffer
				if err := stuntgres.Dump(ctx, db, &dump); err != nil {
					t.Fatal(err)
				}
				var same bytes.Buffer
				if err := stuntgres.Dump(ctx, db, &same); err != nil || !bytes.Equal(dump.Bytes(), same.Bytes()) {
					t.Fatal("unstable dump", err)
				}
				if err := stuntgres.Reset(ctx, db); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				held, err := db.Prepare("INSERT INTO d DEFAULT VALUES")
				if err != nil {
					t.Fatal(err)
				}
				for range 2 {
					result, err := held.Exec()
					if err != nil {
						t.Fatal(err)
					}
					if count, err := result.RowsAffected(); err != nil || count != 1 {
						t.Fatal(count, err)
					}
				}
				held.Close()
				wantTuples(t, db, "SELECT * FROM d", [][]any{row, row, row})
				if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE partial(i INT); CREATE TABLE bad(b BOOL DEFAULT (TRUE BETWEEN 1 AND TRUE));")); err == nil {
					t.Fatal("invalid restore accepted")
				}
				wantTuples(t, db, "SELECT * FROM d", [][]any{row, row, row})
				partial, err := db.Query("SELECT i FROM partial")
				if partial != nil {
					partial.Close()
				}
				var missing *stuntgres.Error
				if !errors.As(err, &missing) || missing.Code != "42P01" {
					t.Fatal("failed restore published partial relation", err)
				}
				var replay bytes.Buffer
				if err := stuntgres.Dump(ctx, db, &replay); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(ctx, db); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(ctx, db, bytes.NewReader(replay.Bytes())); err != nil {
					t.Fatal(err)
				}
				exec("INSERT INTO d DEFAULT VALUES", 1)
				wantTuples(t, db, "SELECT * FROM d", [][]any{row, row, row, row})
			})
		}
	}
}
