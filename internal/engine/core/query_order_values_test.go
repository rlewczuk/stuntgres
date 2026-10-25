package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqltype"
)

func TestQueryOrderValuesSourceOrder(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE d(id INT,a BIGINT)")
	ref := enginetest.RoleTableRef(t, e, "d")
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	for _, key := range []string{"column2 DESC NULLS LAST,column1", "column2+0 DESC NULLS LAST,column1", "2 DESC NULLS LAST,1"} {
		ast := enginetest.Statement(t, "INSERT INTO d VALUES(1,2),(2,1),(3,NULL),(4,2) ORDER BY "+key)
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			e.Mu.Unlock()
			t.Fatal(err)
		}
		bound, err := engine.Describe(op.Ctx, e.AnalysisHost(op), ast, auth, ref, sqltype.InputEnvironment{}, nil, nil)
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		rows, err := dml.EvaluateInsertQuery(context.Background(), bound, &exec.Source{}, nil, "public", sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, nil)
		if err != nil || len(rows.Values) != 4 {
			t.Fatal(rows, err)
		}
		for i, want := range []int64{1, 4, 2, 3} {
			id, err := rows.Values[i][0].Integer()
			if err != nil || id != want {
				t.Fatal(key, i, id, want, err)
			}
			if rows.Values[i][1].Type().ID() != sqltype.Int8 {
				t.Fatal("destination assignment type lost", rows.Values[i][1])
			}
		}
		if !rows.Values[3][1].IsNull() {
			t.Fatal("NULL placement lost", rows)
		}
	}
}
