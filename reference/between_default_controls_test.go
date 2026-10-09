package reference_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceBetweenDefaultCompositionControls(t *testing.T) {
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
				p := newReferenceScenario(t)
				db := p.native
				exec := func(query string, want int64) {
					step := referenceExec(query, want)
					step.prepared = prepared
					scalarRunStep(t, p.ctx, p.memory, p.memoryRecord, step)
				}
				definitions := []string{}
				for i, form := range betweenReferenceForms {
					definitions = append(definitions, fmt.Sprintf("b%d BOOL DEFAULT (%s %s %s AND %s)", i, family.a, form, family.l, family.u))
				}
				exec("CREATE TABLE d("+strings.Join(definitions, ",")+")", 0)
				exec("INSERT INTO d DEFAULT VALUES", 1)
				row := []any{true, false, true, false}
				if family.null {
					row = []any{nil, nil, nil, nil}
				}
				scalarNativeTuples(t, db, "SELECT * FROM d", [][]any{row})
				rows, err := db.Query("SELECT * FROM d")
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
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
				defer func() {
					if err := held.Close(); err != nil {
						t.Error(err)
					}
				}()
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
				scalarNativeTuples(t, db, "SELECT * FROM d", [][]any{row, row, row})
				if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE partial(i INT); CREATE TABLE bad(b BOOL DEFAULT (TRUE BETWEEN 1 AND TRUE));")); err == nil {
					t.Fatal("invalid restore accepted")
				}
				scalarNativeTuples(t, db, "SELECT * FROM d", [][]any{row, row, row})
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
				scalarNativeTuples(t, db, "SELECT * FROM d", [][]any{row, row, row, row})
				for _, backend := range p.backends() {
					if !backend.pg {
						continue
					}
					run := func(step referenceStep) {
						step.prepared = prepared
						scalarRunStep(t, p.ctx, backend.db, backend.recorder, step)
					}
					run(referenceExec("CREATE TABLE d("+strings.Join(definitions, ",")+")", 0))
					run(referenceExec("INSERT INTO d DEFAULT VALUES", 1))
					step := referenceSelect("SELECT * FROM d", []string{"b0", "b1", "b2", "b3"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{row})
					run(step)
					held, err := backend.db.PrepareContext(p.ctx, "INSERT INTO d DEFAULT VALUES")
					if err != nil {
						t.Fatal(err)
					}
					defer func() {
						if err := held.Close(); err != nil {
							t.Error(err)
						}
					}()
					for range 2 {
						scalarRunStep(t, p.ctx, referenceHeldStatement{backend.db, held, "INSERT INTO d DEFAULT VALUES"}, backend.recorder, referenceExec("INSERT INTO d DEFAULT VALUES", 1))
					}
					held.Close()
					step.rows = [][]any{row, row, row}
					run(step)
					run(referenceExec("INSERT INTO d DEFAULT VALUES", 1))
					step.rows = [][]any{row, row, row, row}
					run(step)
				}

			})
		}
	}
}
