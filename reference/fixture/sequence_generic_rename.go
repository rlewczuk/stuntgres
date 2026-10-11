package fixture

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
)

// Native log_cnt is exactly zero; PostgreSQL's physical WAL field is
// nonnegative and unchanged across renames/rejected utilities, not backend parity.
func GenericRenameState(t *testing.T, p *Pair, c *sql.Conn, name string, value int64, called, pg bool) int64 {
	t.Helper()
	recorder := p.MemoryRecord
	if pg {
		recorder = p.PostgresRecord
	}
	query := "SELECT * FROM " + name
	recorder.Mu.Lock()
	firstOperation := len(recorder.Operations)
	recorder.Mu.Unlock()
	ctx := context.WithValue(p.Ctx, RecorderKey{}, recorder)
	rows, err := c.QueryContext(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	// Capture this newly opened cursor before consumption, not a prior same-SQL result.
	recorder.Mu.Lock()
	var recorded *Operation
	for _, op := range recorder.Operations[firstOperation:] {
		if op.SQL == query && op.Err == nil && op.Columns != nil {
			recorded = op
		}
	}
	recorder.Mu.Unlock()
	if recorded == nil {
		t.Fatal("sequence state query has no recorded cursor", query)
	}
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"last_value", "log_cnt", "is_called"}) {
		t.Fatal(columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 3 {
		t.Fatal(types, err)
	}
	for i, want := range []string{"INT8", "INT8", "BOOL"} {
		if types[i].DatabaseTypeName() != want {
			t.Fatal("type", i, types[i].DatabaseTypeName(), want)
		}
	}
	if !rows.Next() {
		t.Fatal("missing sequence row", rows.Err())
	}
	var rawValue, rawLog, rawCalled any
	if err = rows.Scan(&rawValue, &rawLog, &rawCalled); err != nil {
		t.Fatal("sequence state scan", err)
	}
	got, valueOK := rawValue.(int64)
	log, logOK := rawLog.(int64)
	isCalled, calledOK := rawCalled.(bool)
	if !valueOK || !logOK || !calledOK {
		t.Fatalf("raw sequence state types got %T/%T/%T want int64/int64/bool", rawValue, rawLog, rawCalled)
	}
	if got != value || isCalled != called || log < 0 {
		t.Fatal("sequence state", got, log, isCalled)
	}
	if !pg && log != 0 {
		t.Fatal("native log_cnt must be exactly zero", log)
	}
	var typedValue, typedLog int64
	var typedCalled bool
	if err := rows.Scan(&typedValue, &typedLog, &typedCalled); err != nil {
		t.Fatal("typed sequence state scan", err)
	}
	if typedValue != value || typedCalled != called || (!pg && typedLog != 0) ||
		(pg && (typedLog < 0 || typedLog != log)) {
		t.Fatal("typed sequence state", typedValue, typedLog, typedCalled)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("sequence row count", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal("sequence state close", err)
	}
	recorder.Mu.Lock()
	defer recorder.Mu.Unlock()
	if !recorded.Closed || recorded.CloseErr != nil || !reflect.DeepEqual(recorded.Columns, columns) ||
		len(recorded.Rows) != 1 || len(recorded.Rows[0]) != 3 {
		t.Fatal("consumed sequence state recording/closure", query, recorded)
	}
	recordedValue, valueOK := recorded.Rows[0][0].(int64)
	recordedLog, logOK := recorded.Rows[0][1].(int64)
	recordedCalled, calledOK := recorded.Rows[0][2].(bool)
	if !valueOK || !logOK || !calledOK {
		t.Fatalf("recorded sequence state types got %T/%T/%T want int64/int64/bool",
			recorded.Rows[0][0], recorded.Rows[0][1], recorded.Rows[0][2])
	}
	if recordedValue != value || recordedCalled != called || (!pg && recordedLog != 0) ||
		(pg && recordedLog != log) {
		t.Fatal("recorded sequence state", recorded.Rows[0])
	}
	return log
}
