package fixture

import (
	"database/sql"
	"reflect"
	"testing"
)

func DeleteReferenceRows(t *testing.T, p *Pair, x Executor, r *Recorder, ids ...int64) {
	t.Helper()
	rows := make([][]any, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, []any{id})
	}
	RunStep(t, p.Ctx, x, r, Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, rows))
}

func DeleteReferenceExecuteStep(t *testing.T, p *Pair, x Executor, r *Recorder, step Step) {
	t.Helper()
	if step.Diagnostic == nil {
		RunStep(t, p.Ctx, x, r, step)
		return
	}
	var result sql.Result
	var err error
	if step.Prepared {
		held, prepareErr := x.PrepareContext(p.Ctx, step.Query)
		if prepareErr != nil {
			if held != nil {
				held.Close()
				t.Fatal("failed Prepare exposed statement")
			}
			CheckDiagnostic(t, prepareErr, step, "prepare")
			return
		}
		defer held.Close()
		result, err = held.ExecContext(p.Ctx, step.Args...)
	} else {
		result, err = x.ExecContext(p.Ctx, step.Query, step.Args...)
	}
	if result != nil {
		t.Fatal("failed execution exposed result", result)
	}
	CheckDiagnostic(t, err, step, "execute")
}
func DeleteReferenceConsume(t *testing.T, pg bool, rows *sql.Rows, err error, step Step) {
	t.Helper()
	if rows != nil {
		defer rows.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, step.Columns) {
		t.Fatal(names, err, step.Columns)
	}
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != len(step.Types) {
		t.Fatal(meta, err)
	}
	for i, m := range meta {
		if m.DatabaseTypeName() != step.Types[i] {
			t.Fatal(i, m.DatabaseTypeName(), step.Types[i])
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
			AssertRawValue(t, pg, step.Types[i], v)
			row[i] = Normalize(v)
		}
		values = append(values, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := CompareRows(values, step.Rows, step.Bag); !ok {
		t.Fatal(values, step.Rows)
	}
}
func DeleteReferenceCount(t *testing.T, result sql.Result, err error, count int64) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	LockAffected(t, result, count)
	if n, err := result.LastInsertId(); n != 0 || err == nil || err.Error() != "LastInsertId is not supported by this driver" {
		t.Fatal(n, err)
	}
}
func DeleteReferenceRecorded(t *testing.T, recorder *Recorder, s Step) {
	t.Helper()
	recorder.Mu.Lock()
	var found *Operation
	for _, op := range recorder.Operations {
		if op.SQL == s.Query && op.Err == nil && op.Columns != nil {
			found = op
		}
	}
	if found == nil {
		recorder.Mu.Unlock()
		t.Fatal("missing recorded result", s.Query)
	}
	// Appended cells and columns are owned and immutable; snapshot under the lock.
	recordedRows, columns := found.Rows, found.Columns
	closed, closeErr := found.Closed, found.CloseErr
	recorder.Mu.Unlock()
	rows := make([][]any, 0, len(recordedRows))
	for _, row := range recordedRows {
		v := make([]any, len(row))
		for i, x := range row {
			if recorder.RawBackend != RawLegacy {
				AssertRawValue(t, recorder.RawBackend == RawPostgres, s.Types[i], x)
			}
			v[i] = Normalize(x)
		}
		rows = append(rows, v)
	}
	if !reflect.DeepEqual(columns, s.Columns) || !RecordedRowsMatch(rows, s) || !closed || closeErr != nil {
		t.Fatal("recorded result", columns, rows, closed, closeErr)
	}
}
