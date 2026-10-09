package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

// Assert the scalar on its actual cursor, including legacy notice-pool raw cells.
func sequenceRestartValue(t *testing.T, p *referencePair, c *sql.Conn, recorder *referenceRecorder, query, label string, expected int64) {
	t.Helper()
	step := referenceSelect(query, []string{label}, []string{"INT8"}, [][]any{{expected}})
	step.checkScan = func(t *testing.T, rows *sql.Rows, rowIndex int) {
		if recorder.rawBackend == referenceRawLegacy {
			var raw any
			if err := rows.Scan(&raw); err != nil {
				t.Fatal(err)
			}
			if value, ok := raw.(int64); !ok || value != expected {
				t.Fatalf("%s raw scalar=%#v (%T), want int64(%d)", query, raw, raw, expected)
			}
		}
		var value int64
		if err := rows.Scan(&value); err != nil || rowIndex != 0 || value != expected {
			t.Fatalf("%s scalar row=%d value=%d want=%d err=%v", query, rowIndex, value, expected, err)
		}
	}
	referenceRunStep(t, p.ctx, c, recorder, step)
	if recorder.rawBackend == referenceRawLegacy {
		recorder.mu.Lock()
		defer recorder.mu.Unlock()
		var recorded *referenceOperation
		for _, op := range recorder.operations {
			if op.SQL == query && op.Err == nil && op.Columns != nil {
				recorded = op
			}
		}
		if recorded == nil || len(recorded.Rows) != 1 || len(recorded.Rows[0]) != 1 {
			t.Fatal("missing recorded scalar row", query)
		}
		raw := recorded.Rows[0][0]
		if value, ok := raw.(int64); !ok || value != expected {
			t.Fatalf("%s recorded raw scalar=%#v (%T), want int64(%d)", query, raw, raw, expected)
		}
	}
}

func sequenceRestartState(t *testing.T, p *referencePair, c *sql.Conn, recorder *referenceRecorder, pg bool, name string, last int64, called bool) {
	t.Helper()
	referenceRunStep(t, p.ctx, c, recorder, referenceStep{query: "SELECT last_value,is_called FROM " + name, selectRows: true, columns: []string{"last_value", "is_called"}, types: []string{"INT8", "BOOL"}, rows: [][]any{{last, called}}})
	rows, err := c.QueryContext(p.ctx, "SELECT * FROM "+name)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(cols, []string{"last_value", "log_cnt", "is_called"}) {
		t.Fatal(cols, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 3 {
		t.Fatal(types, err)
	}
	for i, want := range []string{"INT8", "INT8", "BOOL"} {
		if types[i].DatabaseTypeName() != want {
			t.Fatal(types[i])
		}
	}
	var n, log any
	var b any
	if !rows.Next() {
		t.Fatal("missing sequence row", rows.Err())
	}
	if err = rows.Scan(&n, &log, &b); err != nil {
		t.Fatal(err)
	}
	logValue, ok := log.(int64)
	if n != last || b != called || !ok || logValue < 0 || (!pg && logValue != 0) {
		t.Fatal(name, n, log, b)
	}
	var typedLast, typedLog int64
	var typedCalled bool
	if err = rows.Scan(&typedLast, &typedLog, &typedCalled); err != nil || typedLast != last || typedLog != logValue || typedCalled != called {
		t.Fatal(name, typedLast, typedLog, typedCalled, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("extra/error sequence row", rows.Err())
	}
	if err = rows.Close(); err != nil {
		t.Fatal(err)
	}
}
