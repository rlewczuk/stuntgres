package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
)

func TestReferenceCharacterHeldPreparedCastsAndRightColumn(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE c(c CHAR(2),v VARCHAR(4),t TEXT)", 0))
	p.step(t, referenceExec("INSERT INTO c VALUES('a','a ','a '),('b','b ','b ')", 2))
	for _, predicate := range []string{"CAST('a ' AS bpchar)=c", "'a    '=c", "CAST('a' AS text)=c"} {
		s := referenceSelect("SELECT c FROM c WHERE "+predicate, []string{"c"}, []string{"BPCHAR"}, [][]any{{"a "}})
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
	for _, tc := range []struct {
		arg  any
		rows [][]any
	}{{"a   ", [][]any{{"a "}}}, {"b", [][]any{{"b "}}}, {nil, nil}} {
		s := referenceSelect("SELECT c FROM c WHERE $1=c", []string{"c"}, []string{"BPCHAR"}, tc.rows, tc.arg)
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
	s := referenceSelect("SELECT c::char(4)::text,CAST(v AS char(3)),CAST(t AS varchar(1)) FROM c", []string{"c", "v", "t"}, []string{"TEXT", "BPCHAR", "VARCHAR"}, [][]any{{"a", "a  ", "a"}, {"b", "b  ", "b"}})
	s.bag = true
	p.step(t, s)
	p.step(t, referencePrepared(s))
}

func TestReferenceCharacterRightColumnHeldEquality(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE c(c CHAR(2))", 0))
	p.step(t, referenceExec("INSERT INTO c VALUES('a'),('b'),(NULL)", 3))
	for _, predicate := range []string{"CAST('a ' AS bpchar)=c", "'a    '=c", "CAST('a' AS text)=c", "CAST(c AS text)='a' AND NOT CAST('a ' AS text)=c"} {
		s := referenceSelect("SELECT c FROM c WHERE "+predicate, []string{"c"}, []string{"BPCHAR"}, [][]any{{"a "}})
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
	for _, backend := range p.backends() {
		func() {
			q := "SELECT c FROM c WHERE $1=c"
			stmt, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: q}
			for _, tc := range []struct {
				arg  any
				rows [][]any
			}{{"a   ", [][]any{{"a "}}}, {"b", [][]any{{"b "}}}, {"missing", nil}, {nil, nil}} {
				referenceRunStep(t, p.ctx, held, backend.recorder, referenceSelect(q, []string{"c"}, []string{"BPCHAR"}, tc.rows, tc.arg))
			}
			ctx, cancel := context.WithCancel(p.ctx)
			cancel()
			rows, err := stmt.QueryContext(ctx, "a")
			if rows != nil {
				rows.Close()
				t.Fatal("canceled held Query exposed cursor")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		}()
	}
}

func TestReferenceCharacterHeldCastMetadata(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE seed(i INT)", 0))
	p.step(t, referenceExec("INSERT INTO seed VALUES(1)", 1))
	for _, pair := range []bool{false, true} {
		q := "SELECT CAST($1 AS varchar(2)) FROM seed"
		names, types := []string{"varchar"}, []string{"VARCHAR"}
		lengths := []int64{2}
		if pair {
			q = "SELECT CAST($1 AS varchar(2)),CAST($2 AS char(3)) FROM seed"
			names, types, lengths = []string{"varchar", "bpchar"}, []string{"VARCHAR", "BPCHAR"}, []int64{2, 3}
		}
		for _, backend := range p.backends() {
			func() {
				stmt, err := backend.db.PrepareContext(p.ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				for _, tc := range []struct{ arg, varying, fixed any }{{"é🙂x", "é🙂", "é🙂x"}, {"x", "x", "x  "}, {"a", "a", "a  "}, {"", "", "   "}, {nil, nil, nil}} {
					args, want := []any{tc.arg}, []any{tc.varying}
					if pair {
						args, want = []any{tc.arg, tc.arg}, []any{tc.varying, tc.fixed}
					}
					rows, err := stmt.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder), args...)
					if err != nil {
						t.Fatal(err)
					}
					columns, err := rows.ColumnTypes()
					if err != nil || len(columns) != len(names) {
						rows.Close()
						t.Fatal(columns, err)
					}
					check := func() {
						for i, column := range columns {
							length, known := column.Length()
							n, nk := column.Nullable()
							precision, scale, dk := column.DecimalSize()
							scan := reflect.TypeFor[string]()
							if backend.pg && types[i] == "BPCHAR" {
								scan = reflect.TypeFor[any]()
							}
							if column.Name() != names[i] || column.DatabaseTypeName() != types[i] || !known || length != lengths[i] || column.ScanType() != scan || n || nk || precision != 0 || scale != 0 || dk {
								t.Fatal(column)
							}
						}
					}
					check()
					values, dest := make([]any, len(names)), make([]any, len(names))
					for i := range values {
						dest[i] = &values[i]
					}
					if !rows.Next() {
						rows.Close()
						t.Fatal("missing cast row", rows.Err())
					}
					if err := rows.Scan(dest...); err != nil {
						t.Fatal(err)
					}
					for i, value := range values {
						referenceAssertRawValue(t, backend.pg, types[i], value)
						if referenceNormalize(value) != want[i] {
							t.Fatal(values, want)
						}
					}
					if rows.Next() || rows.Err() != nil {
						rows.Close()
						t.Fatal("extra cast row", rows.Err())
					}
					check()
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					check()
					backend.recorder.mu.Lock()
					var captured *referenceOperation
					for _, operation := range backend.recorder.operations {
						if operation.SQL == q && operation.Columns != nil {
							captured = operation
						}
					}
					backend.recorder.mu.Unlock()
					if captured == nil || len(captured.Rows) != 1 || !captured.Closed || captured.CloseErr != nil {
						t.Fatal("unconsumed held cast")
					}
					for i, value := range captured.Rows[0] {
						referenceAssertRawValue(t, backend.pg, types[i], value)
						if referenceNormalize(value) != want[i] {
							t.Fatal(captured.Rows, want)
						}
					}
					if !pair {
						var nullable sql.NullString
						if err := stmt.QueryRowContext(context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder), tc.arg).Scan(&nullable); err != nil || nullable.Valid != (tc.varying != nil) {
							t.Fatal(nullable, err)
						}
						if nullable.Valid && nullable.String != tc.varying {
							t.Fatal(nullable, tc)
						}
					}
				}
			}()
		}
	}
}

func TestReferenceCharacterScalarAssignmentIO(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE io(c CHAR(5),v VARCHAR(5))", 0))
	want := [][]any{{"true ", "123"}, {"false", "1.25"}}
	for _, prepared := range []bool{false, true} {
		s := referenceExec("INSERT INTO io VALUES(TRUE,123),(FALSE,1.25)", 2)
		s.prepared = prepared
		p.step(t, s)
		read := referenceSelect("SELECT * FROM io", []string{"c", "v"}, []string{"BPCHAR", "VARCHAR"}, want)
		read.bag = true
		p.step(t, read)
		want = append(want, []any{"true ", "123"}, []any{"false", "1.25"})
	}
}
