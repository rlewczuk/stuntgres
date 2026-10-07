package engine

import (
	"context"
	"testing"

	"stuntgres/internal/sqltype"
)

func TestQueryOrderValuesSourceOrder(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE d(id INT,a BIGINT)")
	ref := roleTableRef(t, e, "d")
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	for _, key := range []string{"column2 DESC NULLS LAST,column1", "column2+0 DESC NULLS LAST,column1", "2 DESC NULLS LAST,1"} {
		ast := statement(t, "INSERT INTO d VALUES(1,2),(2,1),(3,NULL),(4,2) ORDER BY "+key)
		e.mu.Lock()
		auth, err := e.authorizationLocked(op)
		if err != nil {
			e.mu.Unlock()
			t.Fatal(err)
		}
		bound, err := e.describe(op.ctx, ast, auth, ref, sqltype.InputEnvironment{}, op, nil)
		e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		rows, err := evaluateInsertQuery(context.Background(), bound, &querySource{}, nil, "public", sqltype.OutputEnvironment{})
		if err != nil || len(rows) != 4 {
			t.Fatal(rows, err)
		}
		for i, want := range []int64{1, 4, 2, 3} {
			id, err := rows[i][0].Integer()
			if err != nil || id != want {
				t.Fatal(key, i, id, want, err)
			}
			if rows[i][1].Type().ID() != sqltype.Int8 {
				t.Fatal("destination assignment type lost", rows[i][1])
			}
		}
		if !rows[3][1].IsNull() {
			t.Fatal("NULL placement lost", rows)
		}
	}
}
