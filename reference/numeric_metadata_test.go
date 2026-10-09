package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
)

func TestReferenceNumericRawMetadataAndTypmods(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		p := newReferenceScenario(t)
		for _, step := range []referenceStep{
			referenceExec(`CREATE TABLE aliases(v NUMERIC,a DECIMAL,b DEC,c "numeric")`, 0),
			referenceExec("CREATE TABLE sized(a NUMERIC(3,1),b NUMERIC(2,-3),c NUMERIC(3,5),d NUMERIC)", 0),
			referenceExec("INSERT INTO sized VALUES(1.25,1500,.009994,NULL),(-1.25,-1500,.000004,1.2300)", 2),
		} {
			step.prepared = prepared
			p.step(t, step)
		}
		for _, backend := range p.backends() {
			for _, step := range []referenceStep{
				referenceSelect("SELECT * FROM aliases", []string{"v", "a", "b", "c"}, []string{"NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC"}, nil),
				referenceSelect("SELECT * FROM sized ORDER BY a", []string{"a", "b", "c", "d"}, []string{"NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC"}, [][]any{{"-1.3", "-2000", "0.00000", "1.2300"}, {"1.3", "2000", "0.00999", nil}}),
			} {
				func() {
					ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
					var executor referenceExecutor = backend.db
					if prepared {
						stmt, err := backend.db.PrepareContext(ctx, step.query)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						executor = referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: step.query}
					}
					rows, err := executor.QueryContext(ctx, step.query)
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					columns, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					for i, column := range columns {
						precision, scale, sized := int64(0), int64(0), false
						if step.query != "SELECT * FROM aliases" {
							precision = []int64{3, 2, 3, 0}[i]
							scale = []int64{1, -3, 5, 0}[i]
							sized = i != 3
						}
						scan := reflect.TypeFor[string]()
						if backend.pg {
							scan = reflect.TypeFor[any]()
							if sized && i == 1 {
								scale = 2045
							}
							if !sized {
								precision, scale, sized = 65535, 65531, true
							}
						}
						p, s, ok := column.DecimalSize()
						length, hasLength := column.Length()
						nullable, known := column.Nullable()
						if column.DatabaseTypeName() != "NUMERIC" || column.ScanType() != scan || p != precision || s != scale || ok != sized || length != 0 || hasLength || known != !backend.pg || nullable != !backend.pg {
							t.Fatal(column, p, s, ok, nullable, known)
						}
					}
					for rows.Next() {
						var a, b, c, d any
						if err := rows.Scan(&a, &b, &c, &d); err != nil {
							t.Fatal(err)
						}
						for _, value := range []any{a, b, c, d} {
							referenceAssertRawValue(t, backend.pg, "NUMERIC", value)
						}
					}
					if err := rows.Err(); err != nil {
						t.Fatal(err)
					}
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					referenceRunStep(t, p.ctx, executor, backend.recorder, step)
				}()
			}
		}
	}
}

func numericReferenceScanTypes(t *testing.T, p *referencePair, step referenceStep) {
	t.Helper()
	for _, backend := range p.backends() {
		func() {
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
			var executor referenceExecutor = backend.db
			if step.prepared {
				stmt, err := backend.db.PrepareContext(ctx, step.query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				executor = referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: step.query}
			}
			rows, err := executor.QueryContext(ctx, step.query, step.args...)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			columns, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			numericReferenceQueryRows(t, backend, rows, columns, step)
		}()
	}
}

func numericReferenceQueryRows(t *testing.T, backend referenceBackend, rows *sql.Rows, columns []*sql.ColumnType, step referenceStep) {
	t.Helper()
	defer rows.Close()
	if len(columns) != len(step.types) {
		t.Fatal(columns, step.types)
	}
	for i, column := range columns {
		scan := reflect.TypeFor[string]()
		switch step.types[i] {
		case "INT4":
			scan = reflect.TypeFor[int64]()
			if backend.pg {
				scan = reflect.TypeFor[int32]()
			}
		case "INT8":
			scan = reflect.TypeFor[int64]()
		case "FLOAT8":
			scan = reflect.TypeFor[float64]()
		case "NUMERIC":
			if backend.pg {
				scan = reflect.TypeFor[any]()
			}
		}
		if column.Name() != step.columns[i] || column.DatabaseTypeName() != step.types[i] || column.ScanType() != scan {
			t.Fatal(column, scan)
		}
	}
	values, destinations := make([]any, len(columns)), make([]any, len(columns))
	for i := range values {
		destinations[i] = &values[i]
	}
	got := make([][]any, 0)
	for rows.Next() {
		if err := rows.Scan(destinations...); err != nil {
			t.Fatal(err)
		}
		row := make([]any, len(values))
		for i, value := range values {
			referenceAssertRawValue(t, backend.pg, step.types[i], value)
			row[i] = referenceNormalize(value)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if _, matched := referenceCompareRows(got, step.rows, step.bag); !matched {
		t.Fatal(got, step.rows)
	}
	backend.recorder.mu.Lock()
	var captured *referenceOperation
	for _, operation := range backend.recorder.operations {
		if operation.SQL == step.query && operation.Columns != nil {
			captured = operation
		}
	}
	backend.recorder.mu.Unlock()
	if captured == nil || len(captured.Rows) != len(got) || !captured.Closed || captured.CloseErr != nil || captured.Err != nil {
		t.Fatal("unconsumed numeric metadata query")
	}
	for _, row := range captured.Rows {
		for i, value := range row {
			referenceAssertRawValue(t, backend.pg, step.types[i], value)
		}
	}
}
