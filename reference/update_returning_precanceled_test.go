package reference_test

import (
	"context"
	"errors"
	"testing"
)

func TestReferenceUpdateReturningPreCanceled(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		db := backend.db
		result, err := db.Exec("CREATE TABLE t(id INT)")
		deleteReferenceCount(t, result, err, 0)
		result, err = db.Exec("INSERT INTO t VALUES(1),(2)")
		deleteReferenceCount(t, result, err, 2)
		for _, parent := range []context.Context{context.Background(), p.ctx} {
			ctx, cancel := context.WithCancel(parent)
			cancel()
			rows, err := db.QueryContext(ctx, "UPDATE t SET id=id+1 RETURNING *")
			if rows != nil || !errors.Is(err, context.Canceled) {
				t.Fatal(rows, err)
			}
		}
		rows, err := db.Query("SELECT id FROM t ORDER BY id")
		deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"id"}, types: []string{"INT4"}, rows: [][]any{{int64(1)}, {int64(2)}}, bag: false})
		referenceRunStep(t, p.ctx, db, backend.recorder, referenceSelect("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
	}
}
