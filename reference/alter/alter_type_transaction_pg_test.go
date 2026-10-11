//go:build reference

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterTypeOuterTransactionAXPublication(t *testing.T) {
	p := fixture.NewPair(t)
	for _, commit := range []bool{false, true} {
		a, b := fixture.LockConnections(t, p.Ctx, p.Postgres)
		fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(c INT)", 0)
		fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(7)", 1)
		var pid int
		if err := b.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
			t.Fatal(err)
		}
		tx, err := a.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		fixture.LockExec(t, p.Ctx, tx, "ALTER TABLE t ALTER c TYPE BIGINT USING c+1", 0)
		done := make(chan fixture.RelationOutcome, 1)
		go func() {
			rows, err := b.QueryContext(p.Ctx, "SELECT c FROM t")
			done <- fixture.RelationOutcome{Rows: rows, Err: err}
		}()
		fixture.LockWait(t, p.Ctx, pid)
		if commit {
			err = tx.Commit()
		} else {
			err = tx.Rollback()
		}
		if err != nil {
			t.Fatal(err)
		}
		out := fixture.LockResult(t, done)
		if out.Err != nil {
			t.Fatal(out.Err)
		}
		want, typ := int64(7), "INT4"
		if commit {
			want, typ = 8, "INT8"
		}
		columns, err := out.Rows.Columns()
		if err != nil || len(columns) != 1 || columns[0] != "c" {
			t.Fatal(columns, err)
		}
		metadata, err := out.Rows.ColumnTypes()
		if err != nil || len(metadata) != 1 || metadata[0].DatabaseTypeName() != typ {
			t.Fatal(metadata, err)
		}
		fixture.TransactionRowValues(t, out.Rows, want)
		out.Rows.Close()
		fixture.RunStep(t, p.Ctx, b, p.PostgresRecord, fixture.Select("SELECT c FROM t", []string{"c"}, []string{typ}, [][]any{{want}}))
		fixture.LockExec(t, p.Ctx, b, "DROP TABLE t", 0)
		a.Close()
		b.Close()
	}
}
