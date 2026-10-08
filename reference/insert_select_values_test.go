package reference_test

import (
	"context"
	"testing"
)

func TestReferenceInsertSelectParameterOccurrences(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
			run := func(s referenceStep) { insertSelectRun(t, ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE s(i INT,b BOOL)", 0))
			run(referenceExec("INSERT INTO s VALUES(1,TRUE),(2,FALSE)", 2))
			run(referenceExec("CREATE TABLE t(i BIGINT,n NUMERIC(3,1),m NUMERIC(5,2))", 0))
			query := "INSERT INTO t(i) SELECT i FROM s WHERE i=$1"
			stmt, err := backend.db.PrepareContext(ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{backend.db, stmt, query}
			for _, arg := range []any{int64(1), int64(2)} {
				insertSelectRun(t, ctx, held, backend.recorder, referenceExec(query, 1, arg))
			}
			failure := referenceImplicitError(query, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Context: "unnamed portal parameter $1 = '...'"}, "bad")
			failure.errorStage = "execute"
			insertSelectRun(t, ctx, held, backend.recorder, failure)
			result, err := stmt.ExecContext(ctx)
			if result != nil || err == nil {
				t.Fatal("missing Go argument accepted", result, err)
			}
			if _, ok := err.(interface{ SQLState() string }); ok {
				t.Fatal("ordinary argument error gained SQLSTATE", err)
			}
			failure = referenceImplicitError("INSERT INTO t(i) SELECT $1 FROM s WHERE i=$1", referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus bigint", Position: "25"}, int64(1))
			failure.errorStage = "execute"
			run(failure)
			failure.prepared, failure.errorStage = true, "prepare"
			run(failure)
			query = "INSERT INTO t(n,m) SELECT $1,$1"
			numeric, err := backend.db.PrepareContext(ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer numeric.Close()
			insertSelectRun(t, ctx, referenceHeldStatement{backend.db, numeric, query}, backend.recorder, referenceExec(query, 1, "1.234"))
			run(referenceSelect("SELECT n,m FROM t WHERE i=1", []string{"n", "m"}, []string{"NUMERIC", "NUMERIC"}, [][]any{{nil, nil}}))
			run(referenceSelect("SELECT n,m FROM t ORDER BY n", []string{"n", "m"}, []string{"NUMERIC", "NUMERIC"}, [][]any{{"1.2", "1.23"}, {nil, nil}, {nil, nil}}))
		})
	}
}
