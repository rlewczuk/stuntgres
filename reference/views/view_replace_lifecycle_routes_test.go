package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewReplaceHeldRows(t *testing.T) {
	ctx := context.Background()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		db := backend.DB
		db.SetMaxOpenConns(2)
		for _, q := range []string{"CREATE TABLE t(id INT)", "INSERT INTO t VALUES(7)", "CREATE VIEW v AS SELECT id FROM t"} {
			fixture.UpdateReturningReferenceExec(t, db, q)
		}
		rows, err := db.QueryContext(ctx, "SELECT id FROM v")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		fixture.UpdateReturningReferenceExec(t, db, "CREATE OR REPLACE VIEW v AS SELECT id+10 AS id,TRUE AS extra FROM t")
		fixture.UpdateReturningReferenceExec(t, db, "DROP VIEW v")
		fixture.DeleteReferenceConsume(t, backend.PG, rows, nil, fixture.Step{Columns: []string{"id"}, Types: []string{"INT4"}, Rows: [][]any{{int64(7)}}, Bag: false})
	}
}
