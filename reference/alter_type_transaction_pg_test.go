//go:build reference

package reference_test

import (
	"testing"
)

func TestReferenceAlterTypeOuterTransactionAXPublication(t *testing.T) {
	p := newReferencePair(t)
	for _, commit := range []bool{false, true} {
		a, b := referenceLockConnections(t, p.ctx, p.postgres)
		referenceLockExec(t, p.ctx, a, "CREATE TABLE t(c INT)", 0)
		referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(7)", 1)
		var pid int
		if err := b.QueryRowContext(p.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
			t.Fatal(err)
		}
		tx, err := a.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		referenceLockExec(t, p.ctx, tx, "ALTER TABLE t ALTER c TYPE BIGINT USING c+1", 0)
		done := make(chan relationOutcome, 1)
		go func() {
			rows, err := b.QueryContext(p.ctx, "SELECT c FROM t")
			done <- relationOutcome{rows: rows, err: err}
		}()
		referenceLockWait(t, p.ctx, pid)
		if commit {
			err = tx.Commit()
		} else {
			err = tx.Rollback()
		}
		if err != nil {
			t.Fatal(err)
		}
		out := referenceLockResult(t, done)
		if out.err != nil {
			t.Fatal(out.err)
		}
		want, typ := int64(7), "INT4"
		if commit {
			want, typ = 8, "INT8"
		}
		columns, err := out.rows.Columns()
		if err != nil || len(columns) != 1 || columns[0] != "c" {
			t.Fatal(columns, err)
		}
		metadata, err := out.rows.ColumnTypes()
		if err != nil || len(metadata) != 1 || metadata[0].DatabaseTypeName() != typ {
			t.Fatal(metadata, err)
		}
		transactionRowValues(t, out.rows, want)
		out.rows.Close()
		referenceRunStep(t, p.ctx, b, p.postgresRecord, referenceSelect("SELECT c FROM t", []string{"c"}, []string{typ}, [][]any{{want}}))
		referenceLockExec(t, p.ctx, b, "DROP TABLE t", 0)
		a.Close()
		b.Close()
	}
}
