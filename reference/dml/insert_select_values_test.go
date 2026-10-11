package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInsertSelectParameterOccurrences(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
			run := func(s fixture.Step) { fixture.InsertSelectRun(t, ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE s(i INT,b BOOL)", 0))
			run(fixture.Exec("INSERT INTO s VALUES(1,TRUE),(2,FALSE)", 2))
			run(fixture.Exec("CREATE TABLE t(i BIGINT,n NUMERIC(3,1),m NUMERIC(5,2))", 0))
			query := "INSERT INTO t(i) SELECT i FROM s WHERE i=$1"
			stmt, err := backend.DB.PrepareContext(ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{backend.DB, stmt, query}
			for _, arg := range []any{int64(1), int64(2)} {
				fixture.InsertSelectRun(t, ctx, held, backend.Recorder, fixture.Exec(query, 1, arg))
			}
			failure := fixture.ImplicitError(query, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Context: "unnamed portal parameter $1 = '...'"}, "bad")
			failure.ErrorStage = "execute"
			fixture.InsertSelectRun(t, ctx, held, backend.Recorder, failure)
			result, err := stmt.ExecContext(ctx)
			if result != nil || err == nil {
				t.Fatal("missing Go argument accepted", result, err)
			}
			if _, ok := err.(interface{ SQLState() string }); ok {
				t.Fatal("ordinary argument error gained SQLSTATE", err)
			}
			failure = fixture.ImplicitError("INSERT INTO t(i) SELECT $1 FROM s WHERE i=$1", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus bigint", Position: "25"}, int64(1))
			failure.ErrorStage = "execute"
			run(failure)
			failure.Prepared, failure.ErrorStage = true, "prepare"
			run(failure)
			query = "INSERT INTO t(n,m) SELECT $1,$1"
			numeric, err := backend.DB.PrepareContext(ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer numeric.Close()
			fixture.InsertSelectRun(t, ctx, fixture.HeldStatement{backend.DB, numeric, query}, backend.Recorder, fixture.Exec(query, 1, "1.234"))
			run(fixture.Select("SELECT n,m FROM t WHERE i=1", []string{"n", "m"}, []string{"NUMERIC", "NUMERIC"}, [][]any{{nil, nil}}))
			run(fixture.Select("SELECT n,m FROM t ORDER BY n", []string{"n", "m"}, []string{"NUMERIC", "NUMERIC"}, [][]any{{"1.2", "1.23"}, {nil, nil}, {nil, nil}}))
		})
	}
}
