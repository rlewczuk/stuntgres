package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func deleteReferenceRows(t *testing.T, p *referencePair, x referenceExecutor, r *referenceRecorder, ids ...int64) {
	t.Helper()
	rows := make([][]any, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, []any{id})
	}
	referenceRunStep(t, p.ctx, x, r, referenceSelect("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, rows))
}

func deleteReferenceExecuteStep(t *testing.T, p *referencePair, x referenceExecutor, r *referenceRecorder, step referenceStep) {
	t.Helper()
	if step.diagnostic == nil {
		referenceRunStep(t, p.ctx, x, r, step)
		return
	}
	var result sql.Result
	var err error
	if step.prepared {
		held, prepareErr := x.PrepareContext(p.ctx, step.query)
		if prepareErr != nil {
			if held != nil {
				held.Close()
				t.Fatal("failed Prepare exposed statement")
			}
			referenceCheckDiagnostic(t, prepareErr, step, "prepare")
			return
		}
		defer held.Close()
		result, err = held.ExecContext(p.ctx, step.args...)
	} else {
		result, err = x.ExecContext(p.ctx, step.query, step.args...)
	}
	if result != nil {
		t.Fatal("failed execution exposed result", result)
	}
	referenceCheckDiagnostic(t, err, step, "execute")
}
func deleteReferenceConsume(t *testing.T, pg bool, rows *sql.Rows, err error, step referenceStep) {
	t.Helper()
	if rows != nil {
		defer rows.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, step.columns) {
		t.Fatal(names, err, step.columns)
	}
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != len(step.types) {
		t.Fatal(meta, err)
	}
	for i, m := range meta {
		if m.DatabaseTypeName() != step.types[i] {
			t.Fatal(i, m.DatabaseTypeName(), step.types[i])
		}
	}
	values := make([][]any, 0)
	for rows.Next() {
		row := make([]any, len(names))
		dst := make([]any, len(names))
		for i := range row {
			dst[i] = &row[i]
		}
		if err := rows.Scan(dst...); err != nil {
			t.Fatal(err)
		}
		for i, v := range row {
			referenceAssertRawValue(t, pg, step.types[i], v)
			row[i] = referenceNormalize(v)
		}
		values = append(values, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := referenceCompareRows(values, step.rows, step.bag); !ok {
		t.Fatal(values, step.rows)
	}
}
func deleteReferenceCount(t *testing.T, result sql.Result, err error, count int64) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	referenceLockAffected(t, result, count)
	if n, err := result.LastInsertId(); n != 0 || err == nil || err.Error() != "LastInsertId is not supported by this driver" {
		t.Fatal(n, err)
	}
}
func deleteReferenceRecorded(t *testing.T, recorder *referenceRecorder, s referenceStep) {
	t.Helper()
	recorder.mu.Lock()
	var found *referenceOperation
	for _, op := range recorder.operations {
		if op.SQL == s.query && op.Err == nil && op.Columns != nil {
			found = op
		}
	}
	if found == nil {
		recorder.mu.Unlock()
		t.Fatal("missing recorded result", s.query)
	}
	// Appended cells and columns are owned and immutable; snapshot under the lock.
	recordedRows, columns := found.Rows, found.Columns
	closed, closeErr := found.Closed, found.CloseErr
	recorder.mu.Unlock()
	rows := make([][]any, 0, len(recordedRows))
	for _, row := range recordedRows {
		v := make([]any, len(row))
		for i, x := range row {
			if recorder.rawBackend != referenceRawLegacy {
				referenceAssertRawValue(t, recorder.rawBackend == referenceRawPostgres, s.types[i], x)
			}
			v[i] = referenceNormalize(x)
		}
		rows = append(rows, v)
	}
	if !reflect.DeepEqual(columns, s.columns) || !referenceRecordedRowsMatch(rows, s) || !closed || closeErr != nil {
		t.Fatal("recorded result", columns, rows, closed, closeErr)
	}
}
