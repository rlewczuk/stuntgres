package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateReturningPreCanceled(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		db := backend.DB
		result, err := db.Exec("CREATE TABLE t(id INT)")
		fixture.DeleteReferenceCount(t, result, err, 0)
		result, err = db.Exec("INSERT INTO t VALUES(1),(2)")
		fixture.DeleteReferenceCount(t, result, err, 2)
		for _, parent := range []context.Context{context.Background(), p.Ctx} {
			ctx, cancel := context.WithCancel(parent)
			cancel()
			rows, err := db.QueryContext(ctx, "UPDATE t SET id=id+1 RETURNING *")
			if rows != nil || !errors.Is(err, context.Canceled) {
				t.Fatal(rows, err)
			}
		}
		rows, err := db.Query("SELECT id FROM t ORDER BY id")
		fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"id"}, Types: []string{"INT4"}, Rows: [][]any{{int64(1)}, {int64(2)}}, Bag: false})
		fixture.RunStep(t, p.Ctx, db, backend.Recorder, fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
	}
}
