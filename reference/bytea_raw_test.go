package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"math"
	"reflect"
	"testing"
)

// BYTEA's raw checks precede normalization, including pq's escape empty slice.
func referenceByteaMetadata(t *testing.T, pg bool, columns []*sql.ColumnType, names []string, nullable []int) {
	t.Helper()
	if len(columns) != len(names) {
		t.Fatal(columns, names)
	}
	for i, c := range columns {
		if c.Name() != names[i] || c.DatabaseTypeName() != "BYTEA" || c.ScanType() != reflect.TypeFor[[]byte]() {
			t.Fatal(c)
		}
		length, known := c.Length()
		if pg {
			if !known || length != math.MaxInt64 {
				t.Fatal(length, known)
			}
		} else if known {
			t.Fatal(length, known)
		}
		n, known := c.Nullable()
		if pg || nullable[i] < 0 {
			if known {
				t.Fatal(n, known)
			}
		} else if !known || n != (nullable[i] == 1) {
			t.Fatal(n, known)
		}
		if _, _, known := c.DecimalSize(); known {
			t.Fatal("BYTEA DecimalSize")
		}
	}
}

func referenceByteaRaw(t *testing.T, ctx context.Context, x referenceExecutor, b referenceBackend, q string, prepared bool, names []string, nullable []int, want [][]any, mutate bool, args ...any) {
	t.Helper()
	ctx = context.WithValue(ctx, referenceRecorderKey{}, b.recorder)
	var stmt *sql.Stmt
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, err = x.PrepareContext(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := stmt.Close(); err != nil {
				t.Error(err)
			}
		}()
		rows, err = stmt.QueryContext(ctx, args...)
	} else {
		rows, err = x.QueryContext(ctx, q, args...)
	}
	if rows != nil {
		defer rows.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	cols, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(cols, names) {
		t.Fatal(cols, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	referenceByteaMetadata(t, b.pg, types, names, nullable)
	captured := make([][]driver.Value, 0)
	i := 0
	for rows.Next() {
		values := make([]any, len(names))
		dest := make([]any, len(names))
		for j := range dest {
			dest[j] = &values[j]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		if i >= len(want) || !reflect.DeepEqual(values, want[i]) {
			t.Fatalf("%s row %d = %#v, want %#v", q, i, values, want)
		}
		r := make([]driver.Value, len(names))
		for j := range r {
			r[j] = want[i][j]
			if v, ok := values[j].([]byte); mutate && ok && len(v) > 0 {
				v[0] ^= 255
			}
		}
		captured = append(captured, r)
		i++
	}
	if i != len(want) || rows.Err() != nil {
		t.Fatal(i, len(want), rows.Err())
	}
	referenceByteaMetadata(t, b.pg, types, names, nullable)
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	referenceByteaMetadata(t, b.pg, types, names, nullable)
	b.recorder.mu.Lock()
	var op *referenceOperation
	for _, candidate := range b.recorder.operations {
		if candidate.SQL == q && candidate.Columns != nil {
			op = candidate
		}
	}
	matched := op != nil && reflect.DeepEqual(op.Columns, names) && len(op.Rows) == len(captured)
	if matched {
		for j := range captured {
			if !reflect.DeepEqual(op.Rows[j], captured[j]) {
				matched = false
			}
		}
	}
	closed := op != nil && op.Closed && op.CloseErr == nil && op.Err == nil
	b.recorder.mu.Unlock()
	if !matched || !closed {
		t.Fatal("raw recording/ownership/closure", matched, closed)
	}
}

func referenceByteaExec(t *testing.T, ctx context.Context, x referenceExecutor, b referenceBackend, s referenceStep) {
	t.Helper()
	referenceRunStep(t, ctx, x, b.recorder, s)
	b.recorder.mu.Lock()
	var op *referenceOperation
	for _, candidate := range b.recorder.operations {
		if candidate.SQL == s.query {
			op = candidate
		}
	}
	matched := op != nil && op.Err == nil && op.Affected == s.affected
	b.recorder.mu.Unlock()
	if !matched {
		t.Fatal("recorded affected count", s.query, s.affected)
	}
}
