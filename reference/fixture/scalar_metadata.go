package fixture

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
)

// Metadata is checked on the public cursor before the fixture consumes or
// normalizes cells, including empty results. The common runner remains the
// owner of raw-value, occurrence, middleware and closure assertions.
type ScalarMetadataExecutor struct {
	Executor
	T        *testing.T
	PG       bool
	Metadata []*sql.ColumnType
	// Named computed targets explicitly opt in; stored/identity columns do not.
	Computed      []int
	UnknownLength []int
}

func (e *ScalarMetadataExecutor) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	rows, err := e.Executor.QueryContext(ctx, q, args...)
	if err != nil || rows == nil {
		return rows, err
	}
	transferred := false
	defer func() {
		if !transferred {
			if err := rows.Close(); err != nil {
				e.T.Errorf("intercepted cursor close: %v", err)
			}
		}
	}()
	e.Metadata, err = rows.ColumnTypes()
	if err != nil {
		e.T.Fatal(err)
	}
	ScalarAssertMetadata(e.T, e.PG, e.Metadata, e.Computed...)
	ScalarAssertUnknownLength(e.T, e.Metadata, e.UnknownLength...)
	transferred = true
	return rows, nil
}

func ScalarAssertMetadata(t *testing.T, pg bool, metadata []*sql.ColumnType, computed ...int) {
	t.Helper()
	for index, c := range metadata {
		var want reflect.Type
		switch c.DatabaseTypeName() {
		case "BOOL":
			want = reflect.TypeFor[bool]()
		case "TEXT", "CHAR", "VARCHAR":
			want = reflect.TypeFor[string]()
		case "INT2":
			want = reflect.TypeFor[int64]()
			if pg {
				want = reflect.TypeFor[int16]()
			}
		case "INT4":
			want = reflect.TypeFor[int64]()
			if pg {
				want = reflect.TypeFor[int32]()
			}
		case "INT8":
			want = reflect.TypeFor[int64]()
		}
		if want != nil && c.ScanType() != want {
			t.Fatalf("%s %s ScanType=%v want %v", c.Name(), c.DatabaseTypeName(), c.ScanType(), want)
		}
		unknownNullable := c.Name() == "?column?"
		for _, computedIndex := range computed {
			unknownNullable = unknownNullable || index == computedIndex
		}
		if unknownNullable {
			if nullable, known := c.Nullable(); nullable || known {
				t.Fatalf("scalar Nullable=(%v,%v), want unknown", nullable, known)
			}
		}
	}
}

func ScalarRunStep(t *testing.T, ctx context.Context, db Executor, recorder *Recorder, s Step, computed ...int) Observed {
	t.Helper()
	if s.Prepared {
		statement, err := db.PrepareContext(context.WithValue(ctx, RecorderKey{}, recorder), s.Query)
		if err != nil {
			if statement != nil {
				statement.Close()
				t.Fatal("failed Prepare exposed a statement")
			}
			diagnostic := CheckDiagnostic(t, err, s, "prepare")
			return Observed{state: State(t, err, s.State), Diagnostic: diagnostic, ErrorStage: "prepare"}
		}
		defer func() {
			if err := statement.Close(); err != nil {
				t.Error(err)
			}
		}()
		db = HeldStatement{Executor: db, Statement: statement, SQL: s.Query}
		s.Prepared = false
	}
	executor := &ScalarMetadataExecutor{Executor: db, T: t, PG: recorder.RawBackend == RawPostgres, Computed: computed}
	observed := RunStep(t, ctx, executor, recorder, s)
	// ColumnType descriptions remain valid after the cursor has been closed.
	ScalarAssertMetadata(t, executor.PG, executor.Metadata, computed...)
	if executor.Metadata != nil {
		ScalarAssertDescription(t, executor.Metadata, s.Columns, s.Types)
	}
	return observed
}

func ScalarStep(t *testing.T, p *Pair, s Step, computed ...int) {
	t.Helper()
	observed := []Observed{}
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			observed = append(observed, ScalarRunStep(t, p.Ctx, backend.DB, backend.Recorder, s, computed...))
		})
	}
	if len(observed) == 2 && !t.Failed() && !reflect.DeepEqual(observed[0], observed[1]) {
		t.Fatalf("scalar backend parity: %#v", observed)
	}
}

func ScalarRows(t *testing.T, pg bool, rows *sql.Rows, names, types []string, want [][]any, bag bool) {
	t.Helper()
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, names) {
		t.Fatalf("columns=%v want=%v error=%v", columns, names, err)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil || len(metadata) != len(types) {
		t.Fatalf("metadata=%v error=%v", metadata, err)
	}
	for i, typ := range types {
		if metadata[i].DatabaseTypeName() != typ {
			t.Fatalf("column %d type=%s want=%s", i, metadata[i].DatabaseTypeName(), typ)
		}
	}
	ScalarAssertMetadata(t, pg, metadata)
	got := [][]any{}
	for rows.Next() {
		row := make([]any, len(names))
		dest := make([]any, len(names))
		for i := range row {
			dest[i] = &row[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i, value := range row {
			AssertRawValue(t, pg, types[i], value)
			row[i] = Normalize(value)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if _, ok := CompareRows(got, want, bag); !ok {
		t.Fatalf("rows=%#v want=%#v bag=%v", got, want, bag)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	ScalarAssertMetadata(t, pg, metadata)
	ScalarAssertDescription(t, metadata, names, types)
}

func ScalarAssertDescription(t *testing.T, metadata []*sql.ColumnType, names, types []string) {
	t.Helper()
	if len(metadata) != len(names) || len(metadata) != len(types) {
		t.Fatalf("retained metadata width=%d want=%d/%d", len(metadata), len(names), len(types))
	}
	for i, column := range metadata {
		if column.Name() != names[i] || column.DatabaseTypeName() != types[i] {
			t.Fatalf("retained metadata column%d=%s/%s want=%s/%s", i, column.Name(), column.DatabaseTypeName(), names[i], types[i])
		}
	}
}

func ScalarAssertUnknownLength(t *testing.T, metadata []*sql.ColumnType, indices ...int) {
	t.Helper()
	for _, index := range indices {
		if length, known := metadata[index].Length(); length != 0 || known {
			t.Fatalf("column %d Length=(%d,%v), want (0,false)", index, length, known)
		}
	}
}
