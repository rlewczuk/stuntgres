package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
)

// Metadata is checked on the public cursor before the fixture consumes or
// normalizes cells, including empty results. The common runner remains the
// owner of raw-value, occurrence, middleware and closure assertions.
type scalarMetadataExecutor struct {
	referenceExecutor
	t        *testing.T
	pg       bool
	metadata []*sql.ColumnType
	// Named computed targets explicitly opt in; stored/identity columns do not.
	computed      []int
	unknownLength []int
}

func (e *scalarMetadataExecutor) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	rows, err := e.referenceExecutor.QueryContext(ctx, q, args...)
	if err != nil || rows == nil {
		return rows, err
	}
	transferred := false
	defer func() {
		if !transferred {
			if err := rows.Close(); err != nil {
				e.t.Errorf("intercepted cursor close: %v", err)
			}
		}
	}()
	e.metadata, err = rows.ColumnTypes()
	if err != nil {
		e.t.Fatal(err)
	}
	scalarAssertMetadata(e.t, e.pg, e.metadata, e.computed...)
	scalarAssertUnknownLength(e.t, e.metadata, e.unknownLength...)
	transferred = true
	return rows, nil
}

func scalarAssertMetadata(t *testing.T, pg bool, metadata []*sql.ColumnType, computed ...int) {
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

func scalarRunStep(t *testing.T, ctx context.Context, db referenceExecutor, recorder *referenceRecorder, s referenceStep, computed ...int) referenceObserved {
	t.Helper()
	if s.prepared {
		statement, err := db.PrepareContext(context.WithValue(ctx, referenceRecorderKey{}, recorder), s.query)
		if err != nil {
			if statement != nil {
				statement.Close()
				t.Fatal("failed Prepare exposed a statement")
			}
			diagnostic := referenceCheckDiagnostic(t, err, s, "prepare")
			return referenceObserved{state: referenceState(t, err, s.state), diagnostic: diagnostic, errorStage: "prepare"}
		}
		defer func() {
			if err := statement.Close(); err != nil {
				t.Error(err)
			}
		}()
		db = referenceHeldStatement{referenceExecutor: db, statement: statement, query: s.query}
		s.prepared = false
	}
	executor := &scalarMetadataExecutor{referenceExecutor: db, t: t, pg: recorder.rawBackend == referenceRawPostgres, computed: computed}
	observed := referenceRunStep(t, ctx, executor, recorder, s)
	// ColumnType descriptions remain valid after the cursor has been closed.
	scalarAssertMetadata(t, executor.pg, executor.metadata, computed...)
	if executor.metadata != nil {
		scalarAssertDescription(t, executor.metadata, s.columns, s.types)
	}
	return observed
}

func scalarStep(t *testing.T, p *referencePair, s referenceStep, computed ...int) {
	t.Helper()
	observed := []referenceObserved{}
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			observed = append(observed, scalarRunStep(t, p.ctx, backend.db, backend.recorder, s, computed...))
		})
	}
	if len(observed) == 2 && !t.Failed() && !reflect.DeepEqual(observed[0], observed[1]) {
		t.Fatalf("scalar backend parity: %#v", observed)
	}
}

func scalarRows(t *testing.T, pg bool, rows *sql.Rows, names, types []string, want [][]any, bag bool) {
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
	scalarAssertMetadata(t, pg, metadata)
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
			referenceAssertRawValue(t, pg, types[i], value)
			row[i] = referenceNormalize(value)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if _, ok := referenceCompareRows(got, want, bag); !ok {
		t.Fatalf("rows=%#v want=%#v bag=%v", got, want, bag)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	scalarAssertMetadata(t, pg, metadata)
	scalarAssertDescription(t, metadata, names, types)
}

func scalarAssertDescription(t *testing.T, metadata []*sql.ColumnType, names, types []string) {
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

func scalarAssertUnknownLength(t *testing.T, metadata []*sql.ColumnType, indices ...int) {
	t.Helper()
	for _, index := range indices {
		if length, known := metadata[index].Length(); length != 0 || known {
			t.Fatalf("column %d Length=(%d,%v), want (0,false)", index, length, known)
		}
	}
}
