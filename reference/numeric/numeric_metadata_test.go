package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceNumericRawMetadataAndTypmods(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		p := fixture.NewScenario(t)
		for _, step := range []fixture.Step{
			fixture.Exec(`CREATE TABLE aliases(v NUMERIC,a DECIMAL,b DEC,c "numeric")`, 0),
			fixture.Exec("CREATE TABLE sized(a NUMERIC(3,1),b NUMERIC(2,-3),c NUMERIC(3,5),d NUMERIC)", 0),
			fixture.Exec("INSERT INTO sized VALUES(1.25,1500,.009994,NULL),(-1.25,-1500,.000004,1.2300)", 2),
		} {
			step.Prepared = prepared
			p.Step(t, step)
		}
		for _, backend := range p.Backends() {
			for _, step := range []fixture.Step{
				fixture.Select("SELECT * FROM aliases", []string{"v", "a", "b", "c"}, []string{"NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC"}, nil),
				fixture.Select("SELECT * FROM sized ORDER BY a", []string{"a", "b", "c", "d"}, []string{"NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC"}, [][]any{{"-1.3", "-2000", "0.00000", "1.2300"}, {"1.3", "2000", "0.00999", nil}}),
			} {
				func() {
					ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
					var executor fixture.Executor = backend.DB
					if prepared {
						stmt, err := backend.DB.PrepareContext(ctx, step.Query)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						executor = fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: step.Query}
					}
					rows, err := executor.QueryContext(ctx, step.Query)
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
						if step.Query != "SELECT * FROM aliases" {
							precision = []int64{3, 2, 3, 0}[i]
							scale = []int64{1, -3, 5, 0}[i]
							sized = i != 3
						}
						scan := reflect.TypeFor[string]()
						if backend.PG {
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
						if column.DatabaseTypeName() != "NUMERIC" || column.ScanType() != scan || p != precision || s != scale || ok != sized || length != 0 || hasLength || known != !backend.PG || nullable != !backend.PG {
							t.Fatal(column, p, s, ok, nullable, known)
						}
					}
					for rows.Next() {
						var a, b, c, d any
						if err := rows.Scan(&a, &b, &c, &d); err != nil {
							t.Fatal(err)
						}
						for _, value := range []any{a, b, c, d} {
							fixture.AssertRawValue(t, backend.PG, "NUMERIC", value)
						}
					}
					if err := rows.Err(); err != nil {
						t.Fatal(err)
					}
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					fixture.RunStep(t, p.Ctx, executor, backend.Recorder, step)
				}()
			}
		}
	}
}

func numericReferenceScanTypes(t *testing.T, p *fixture.Pair, step fixture.Step) {
	t.Helper()
	for _, backend := range p.Backends() {
		func() {
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
			var executor fixture.Executor = backend.DB
			if step.Prepared {
				stmt, err := backend.DB.PrepareContext(ctx, step.Query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				executor = fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: step.Query}
			}
			rows, err := executor.QueryContext(ctx, step.Query, step.Args...)
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

func numericReferenceQueryRows(t *testing.T, backend fixture.Backend, rows *sql.Rows, columns []*sql.ColumnType, step fixture.Step) {
	t.Helper()
	defer rows.Close()
	if len(columns) != len(step.Types) {
		t.Fatal(columns, step.Types)
	}
	for i, column := range columns {
		scan := reflect.TypeFor[string]()
		switch step.Types[i] {
		case "INT4":
			scan = reflect.TypeFor[int64]()
			if backend.PG {
				scan = reflect.TypeFor[int32]()
			}
		case "INT8":
			scan = reflect.TypeFor[int64]()
		case "FLOAT8":
			scan = reflect.TypeFor[float64]()
		case "NUMERIC":
			if backend.PG {
				scan = reflect.TypeFor[any]()
			}
		}
		if column.Name() != step.Columns[i] || column.DatabaseTypeName() != step.Types[i] || column.ScanType() != scan {
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
			fixture.AssertRawValue(t, backend.PG, step.Types[i], value)
			row[i] = fixture.Normalize(value)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if _, matched := fixture.CompareRows(got, step.Rows, step.Bag); !matched {
		t.Fatal(got, step.Rows)
	}
	backend.Recorder.Mu.Lock()
	var captured *fixture.Operation
	for _, operation := range backend.Recorder.Operations {
		if operation.SQL == step.Query && operation.Columns != nil {
			captured = operation
		}
	}
	backend.Recorder.Mu.Unlock()
	if captured == nil || len(captured.Rows) != len(got) || !captured.Closed || captured.CloseErr != nil || captured.Err != nil {
		t.Fatal("unconsumed numeric metadata query")
	}
	for _, row := range captured.Rows {
		for i, value := range row {
			fixture.AssertRawValue(t, backend.PG, step.Types[i], value)
		}
	}
}
