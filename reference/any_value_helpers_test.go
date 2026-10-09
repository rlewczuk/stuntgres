package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"slices"
	"testing"
)

func anyValueRoutes(t *testing.T, p *referencePair, backend referenceBackend, steps ...referenceStep) {
	t.Helper()
	for _, step := range steps {
		for _, prepared := range []bool{false, true} {
			s := step
			s.prepared = prepared
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
		}
	}
}

// Consume an already open public cursor, checking its backend's own middleware
// record. This does not compare arbitrary winners across backends or executions.
func anyValueConsume(t *testing.T, rows *sql.Rows, recorder *referenceRecorder, query string, columns, types []string, heldRecord ...*referenceOperation) [][]any {
	t.Helper()
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil || !slices.Equal(names, columns) {
		t.Fatalf("columns %v: %v, want %v", names, err, columns)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil || len(metadata) != len(types) {
		t.Fatalf("metadata %v: %v", metadata, err)
	}
	for i, typ := range metadata {
		if typ.DatabaseTypeName() != types[i] {
			t.Fatalf("type %d = %s, want %s", i, typ.DatabaseTypeName(), types[i])
		}
	}
	values := make([][]any, 0)
	for rows.Next() {
		row, dest := make([]any, len(columns)), make([]any, len(columns))
		for i := range row {
			dest[i] = &row[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i, value := range row {
			if types[i] == "_NAME" || types[i] == "PG_NODE_TREE" {
				referenceAssertRawValue(t, recorder.rawBackend == referenceRawPostgres, "NAME", value)
			} else {
				referenceAssertRawValue(t, recorder.rawBackend == referenceRawPostgres, types[i], value)
			}
			row[i] = referenceNormalize(value)
		}
		values = append(values, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	recorder.mu.Lock()
	var op *referenceOperation
	for _, candidate := range recorder.operations {
		if candidate.SQL == query && candidate.Err == nil && candidate.Columns != nil {
			op = candidate
		}
	}
	if len(heldRecord) != 0 {
		op = heldRecord[0]
	}
	if op == nil {
		recorder.mu.Unlock()
		t.Fatal("no middleware result")
	}
	closed, closeErr := op.Closed, op.CloseErr
	recorded := make([][]any, 0, len(op.Rows))
	for _, row := range op.Rows {
		copyRow := make([]any, len(row))
		for i, value := range row {
			copyRow[i] = referenceNormalize(value)
		}
		recorded = append(recorded, copyRow)
	}
	recordedColumns := slices.Clone(op.Columns)
	recorder.mu.Unlock()
	if !closed || closeErr != nil || !slices.Equal(recordedColumns, columns) || !anyValueRowsEqual(recorded, values) {
		t.Fatalf("middleware result columns=%v rows=%#v closed=%v err=%v; consumed=%#v", recordedColumns, recorded, closed, closeErr, values)
	}
	return values
}

func anyValueMembership(t *testing.T, p *referencePair, backend referenceBackend, query string, prepared bool, columns, types []string, candidates map[any][]any) {
	t.Helper()
	ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, e := backend.db.PrepareContext(ctx, query)
		if e != nil {
			t.Fatal(e)
		}
		defer stmt.Close()
		rows, err = stmt.QueryContext(ctx)
	} else {
		rows, err = backend.db.QueryContext(ctx, query)
	}
	if err != nil {
		t.Fatal(err)
	}
	values := anyValueConsume(t, rows, backend.recorder, query, columns, types)
	if len(values) != len(candidates) {
		t.Fatalf("group cardinality %#v, want %d", values, len(candidates))
	}
	seen := make(map[any]bool)
	for _, row := range values {
		var key any
		value := row[0]
		if len(row) == 2 {
			key, value = row[0], row[1]
		}
		eligible, exists := candidates[key]
		if !exists || seen[key] {
			t.Fatalf("unexpected/duplicate group %#v", row)
		}
		seen[key] = true
		if len(eligible) == 0 {
			if value != nil {
				t.Fatalf("all-NULL group %#v", row)
			}
			continue
		}
		member := false
		for _, candidate := range eligible {
			member = member || reflect.DeepEqual(value, candidate)
		}
		if value == nil || !member {
			t.Fatalf("result %#v is not eligible in %#v", row, eligible)
		}
	}
}
