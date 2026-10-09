package reference_test

import (
	"context"
	"errors"
	"testing"
)

func TestReferenceViewReplaceAtomicSuffixAndCancellation(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		db := backend.db
		for _, q := range []string{"CREATE TABLE t(id INT)", "INSERT INTO t VALUES(7)", "CREATE VIEW v AS SELECT id FROM t"} {
			updateReturningReferenceExec(t, db, q)
		}
		for _, q := range []string{"CREATE OR REPLACE VIEW v AS SELECT id+10 AS id FROM t; SELECT FROM", "ALTER VIEW v RENAME TO renamed; SELECT FROM", "DROP VIEW v; SELECT FROM"} {
			if _, err := db.Exec(q); err == nil {
				t.Fatal("invalid suffix accepted", q)
			}
		}
		for _, q := range []string{"CREATE OR REPLACE VIEW v AS SELECT id+10 AS id FROM t", "ALTER VIEW v RENAME TO renamed", "DROP VIEW v"} {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := db.ExecContext(ctx, q); !errors.Is(err, context.Canceled) {
				t.Fatal(q, err)
			}
		}
		rows, err := db.Query("SELECT id FROM v")
		deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"id"}, types: []string{"INT4"}, rows: [][]any{{int64(7)}}, bag: false})
	}
}
