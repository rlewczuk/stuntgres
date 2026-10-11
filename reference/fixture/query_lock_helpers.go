package fixture

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
)

func QueryLockScenario(t *testing.T, scenario func(*testing.T, *Pair, Backend, *sql.Conn, *sql.Conn)) {
	t.Helper()
	p := NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			a, b := LockConnections(t, p.Ctx, backend.DB)
			LockExec(t, p.Ctx, a, "CREATE TABLE t(id INT,v INT)", 0)
			LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1,10),(2,20),(3,30),(4,40)", 4)
			LockExec(t, p.Ctx, a, "CREATE TABLE u(id INT,v INT)", 0)
			LockExec(t, p.Ctx, a, "INSERT INTO u VALUES(1,10),(2,20),(3,30),(4,40)", 4)
			scenario(t, p, backend, a, b)
		})
	}
}
func QueryLockBegin(t *testing.T, ctx context.Context, c *sql.Conn) *sql.Tx {
	t.Helper()
	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	QueryLockRollbackCleanup(t, tx)
	return tx
}

// Register on the backend child's T, after its connection cleanup. LIFO then
// retires the transaction before Conn.Close; cursor consumers close first.
func QueryLockRollbackCleanup(t *testing.T, tx *sql.Tx) {
	t.Helper()
	t.Cleanup(func() { tx.Rollback() })
}
func QueryLockRows(t *testing.T, p *Pair, backend Backend, x Executor, q string, prepared bool, name string, values ...int64) {
	t.Helper()
	want := make([][]any, 0, len(values))
	for _, v := range values {
		want = append(want, []any{v})
	}
	step := Select(q, []string{name}, []string{"INT4"}, want)
	step.Prepared = prepared
	RunStep(t, p.Ctx, x, backend.Recorder, step)
}
func QueryLockFailure(t *testing.T, p *Pair, b Backend, x Executor, q string, prepared bool, want Diagnostic, stage string) {
	t.Helper()
	out := RowQuery(p.Ctx, x, q, prepared, b.Recorder.RawBackend)
	actualStage := "execute"
	if prepared && out.Stmt == nil {
		actualStage = "prepare"
	}
	var prefix []int64
	if q == "SELECT id FROM t ORDER BY id FOR UPDATE NOWAIT" || q == "SELECT id FROM t WHERE id<3 ORDER BY id FOR UPDATE NOWAIT" {
		prefix = []int64{1}
	}
	err := QueryLockErrorOutcome(t, out, b.PG, "id", prefix)
	want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
	AssertDiagnostic(t, err, &want)
	if actualStage != stage {
		t.Fatalf("error stage got %s want %s", actualStage, stage)
	}
}
func QueryLockNowait(t *testing.T, p *Pair, b Backend, x Executor, q string, prepared bool, relation string) {
	QueryLockFailure(t, p, b, x, q, prepared, Diagnostic{Code: "55P03", Message: `could not obtain lock on row in relation "` + relation + `"`}, "execute")
}

// pq may publish a streaming cursor before a later row fails. Close it before
// asserting the terminal error, so any Fatal exit precedes owner rollback.
// prefix is the permitted already-produced row prefix, not a success result.
func QueryLockErrorOutcome(t *testing.T, out RelationOutcome, pg bool, column string, prefix []int64) error {
	t.Helper()
	if out.Stmt != nil {
		defer out.Stmt.Close()
	}
	if out.Rows != nil {
		defer out.Rows.Close()
	}
	if !pg {
		if out.Rows != nil {
			t.Fatal("native failed query published cursor")
		}
		return out.Err
	}
	if out.Err != nil {
		if out.Rows != nil {
			t.Fatal("failed Query returned both error and cursor")
		}
		return out.Err
	}
	if out.Rows == nil {
		t.Fatal("query returned neither cursor nor error")
	}
	names, err := out.Rows.Columns()
	if err != nil || !reflect.DeepEqual(names, []string{column}) {
		t.Fatal(names, err)
	}
	types, err := out.Rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" || types[0].ScanType() != reflect.TypeFor[int32]() {
		t.Fatal(types, err)
	}
	n := 0
	for out.Rows.Next() {
		var raw any
		if err := out.Rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		value, ok := raw.(int64)
		if !ok || n >= len(prefix) || value != prefix[n] {
			t.Fatalf("unexpected failed-query prefix row %d: %T %v", n, raw, raw)
		}
		n++
	}
	terminal := out.Rows.Err()
	if err := out.Rows.Close(); err != nil && terminal == nil {
		terminal = err
	}
	return terminal
}
