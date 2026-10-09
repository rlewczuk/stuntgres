package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
)

func queryLockScenario(t *testing.T, scenario func(*testing.T, *referencePair, referenceBackend, *sql.Conn, *sql.Conn)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			a, b := referenceLockConnections(t, p.ctx, backend.db)
			referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT,v INT)", 0)
			referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1,10),(2,20),(3,30),(4,40)", 4)
			referenceLockExec(t, p.ctx, a, "CREATE TABLE u(id INT,v INT)", 0)
			referenceLockExec(t, p.ctx, a, "INSERT INTO u VALUES(1,10),(2,20),(3,30),(4,40)", 4)
			scenario(t, p, backend, a, b)
		})
	}
}
func queryLockBegin(t *testing.T, ctx context.Context, c *sql.Conn) *sql.Tx {
	t.Helper()
	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	queryLockRollbackCleanup(t, tx)
	return tx
}

// Register on the backend child's T, after its connection cleanup. LIFO then
// retires the transaction before Conn.Close; cursor consumers close first.
func queryLockRollbackCleanup(t *testing.T, tx *sql.Tx) {
	t.Helper()
	t.Cleanup(func() { tx.Rollback() })
}
func queryLockRows(t *testing.T, p *referencePair, backend referenceBackend, x referenceExecutor, q string, prepared bool, name string, values ...int64) {
	t.Helper()
	want := make([][]any, 0, len(values))
	for _, v := range values {
		want = append(want, []any{v})
	}
	step := referenceSelect(q, []string{name}, []string{"INT4"}, want)
	step.prepared = prepared
	referenceRunStep(t, p.ctx, x, backend.recorder, step)
}
func queryLockFailure(t *testing.T, p *referencePair, b referenceBackend, x referenceExecutor, q string, prepared bool, want referenceDiagnostic, stage string) {
	t.Helper()
	out := referenceRowQuery(p.ctx, x, q, prepared, b.recorder.rawBackend)
	actualStage := "execute"
	if prepared && out.stmt == nil {
		actualStage = "prepare"
	}
	var prefix []int64
	if q == "SELECT id FROM t ORDER BY id FOR UPDATE NOWAIT" || q == "SELECT id FROM t WHERE id<3 ORDER BY id FOR UPDATE NOWAIT" {
		prefix = []int64{1}
	}
	err := queryLockErrorOutcome(t, out, b.pg, "id", prefix)
	want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
	referenceAssertDiagnostic(t, err, &want)
	if actualStage != stage {
		t.Fatalf("error stage got %s want %s", actualStage, stage)
	}
}
func queryLockNowait(t *testing.T, p *referencePair, b referenceBackend, x referenceExecutor, q string, prepared bool, relation string) {
	queryLockFailure(t, p, b, x, q, prepared, referenceDiagnostic{Code: "55P03", Message: `could not obtain lock on row in relation "` + relation + `"`}, "execute")
}

// pq may publish a streaming cursor before a later row fails. Close it before
// asserting the terminal error, so any Fatal exit precedes owner rollback.
// prefix is the permitted already-produced row prefix, not a success result.
func queryLockErrorOutcome(t *testing.T, out relationOutcome, pg bool, column string, prefix []int64) error {
	t.Helper()
	if out.stmt != nil {
		defer out.stmt.Close()
	}
	if out.rows != nil {
		defer out.rows.Close()
	}
	if !pg {
		if out.rows != nil {
			t.Fatal("native failed query published cursor")
		}
		return out.err
	}
	if out.err != nil {
		if out.rows != nil {
			t.Fatal("failed Query returned both error and cursor")
		}
		return out.err
	}
	if out.rows == nil {
		t.Fatal("query returned neither cursor nor error")
	}
	names, err := out.rows.Columns()
	if err != nil || !reflect.DeepEqual(names, []string{column}) {
		t.Fatal(names, err)
	}
	types, err := out.rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" || types[0].ScanType() != reflect.TypeFor[int32]() {
		t.Fatal(types, err)
	}
	n := 0
	for out.rows.Next() {
		var raw any
		if err := out.rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		value, ok := raw.(int64)
		if !ok || n >= len(prefix) || value != prefix[n] {
			t.Fatalf("unexpected failed-query prefix row %d: %T %v", n, raw, raw)
		}
		n++
	}
	terminal := out.rows.Err()
	if err := out.rows.Close(); err != nil && terminal == nil {
		terminal = err
	}
	return terminal
}
