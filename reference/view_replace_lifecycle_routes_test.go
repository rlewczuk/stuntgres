package reference_test

import (
	"context"
	"testing"
)

func TestReferenceViewReplaceHeldRows(t *testing.T) {
	ctx := context.Background()
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		db := backend.db
		db.SetMaxOpenConns(2)
		for _, q := range []string{"CREATE TABLE t(id INT)", "INSERT INTO t VALUES(7)", "CREATE VIEW v AS SELECT id FROM t"} {
			updateReturningReferenceExec(t, db, q)
		}
		rows, err := db.QueryContext(ctx, "SELECT id FROM v")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		updateReturningReferenceExec(t, db, "CREATE OR REPLACE VIEW v AS SELECT id+10 AS id,TRUE AS extra FROM t")
		updateReturningReferenceExec(t, db, "DROP VIEW v")
		deleteReferenceConsume(t, backend.pg, rows, nil, referenceStep{columns: []string{"id"}, types: []string{"INT4"}, rows: [][]any{{int64(7)}}, bag: false})
	}
}
