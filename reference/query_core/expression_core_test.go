package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceExpressionCoreScalars(t *testing.T) {
	p := fixture.ExpressionReferenceSetup(t)
	all := [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}
	cases := []fixture.Step{
		fixture.Select("SELECT id,1,2147483648,'x',TRUE,FALSE,NULL FROM t ORDER BY id", []string{"id", "?column?", "?column?", "?column?", "?column?", "?column?", "?column?"}, []string{"INT4", "INT4", "INT8", "TEXT", "BOOL", "BOOL", "TEXT"}, [][]any{{int64(1), int64(1), int64(2147483648), "x", true, false, nil}, {int64(2), int64(1), int64(2147483648), "x", true, false, nil}, {int64(3), int64(1), int64(2147483648), "x", true, false, nil}, {int64(4), int64(1), int64(2147483648), "x", true, false, nil}}),
		fixture.Select("SELECT v FROM t WHERE v=$1", []string{"v"}, []string{"INT4"}, nil, nil),
		fixture.Select("SELECT id FROM t WHERE v=2147483648", []string{"id"}, []string{"INT4"}, nil),
		fixture.Select("SELECT id FROM t WHERE v=$1 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(4)}}, 1),
		fixture.Select("SELECT id FROM t WHERE v=$1 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}}, 2),
		fixture.Select("SELECT $1,$1 FROM t ORDER BY id", []string{"?column?", "?column?"}, []string{"TEXT", "TEXT"}, [][]any{{"hi", "hi"}, {"hi", "hi"}, {"hi", "hi"}, {"hi", "hi"}}, "hi"),
		fixture.Select("SELECT $1,$1 FROM t ORDER BY id", []string{"?column?", "?column?"}, []string{"TEXT", "TEXT"}, [][]any{{nil, nil}, {nil, nil}, {nil, nil}, {nil, nil}}, nil),
		fixture.Select("SELECT $1 FROM t WHERE s=$1 ORDER BY id", []string{"?column?"}, []string{"TEXT"}, [][]any{{"x"}, {"x"}, {"x"}}, "x"),
		fixture.Select("SELECT id FROM t WHERE TRUE ORDER BY id", []string{"id"}, []string{"INT4"}, all),
		fixture.Select("SELECT id FROM t WHERE FALSE", []string{"id"}, []string{"INT4"}, nil),
		fixture.Select("SELECT id FROM t WHERE NULL", []string{"id"}, []string{"INT4"}, nil),
		fixture.Select("SELECT id FROM t WHERE f ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(4)}}),
		fixture.Select("SELECT id FROM t WHERE 'yes' ORDER BY id", []string{"id"}, []string{"INT4"}, all),
		fixture.Select("SELECT id FROM t WHERE 'no'", []string{"id"}, []string{"INT4"}, nil),
		fixture.Select("SELECT id FROM t WHERE $1 ORDER BY id", []string{"id"}, []string{"INT4"}, all, true),
		fixture.Select("SELECT id FROM t WHERE $1", []string{"id"}, []string{"INT4"}, nil, false),
		fixture.Select("SELECT id FROM t WHERE $1", []string{"id"}, []string{"INT4"}, nil, nil),
	}
	for _, step := range cases {
		fixture.ScalarStep(t, p, step)
		fixture.ScalarStep(t, p, fixture.Prepared(step))
	}
}
func TestReferenceExpressionCoreDiagnostics(t *testing.T) {
	p := fixture.ExpressionReferenceSetup(t)
	bad := fixture.ImplicitError("SELECT id FROM t WHERE $1", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type boolean: "truth"`, Context: "unnamed portal parameter $1 = '...'"}, "truth")
	bad.SelectRows = true
	bad.ErrorStage = "execute"
	fixture.ScalarStep(t, p, bad)
	fixture.ScalarStep(t, p, fixture.Prepared(bad))
	fixture.ScalarStep(t, p, fixture.Exec("CREATE TABLE atomic(i INT NOT NULL)", 0))
	fixture.ScalarStep(t, p, fixture.Exec("INSERT INTO atomic VALUES(99)", 1))
	for _, step := range []fixture.Step{
		fixture.ImplicitError("INSERT INTO atomic VALUES(1),('oops')", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "31"}),
		fixture.ImplicitError("INSERT INTO atomic VALUES(1),(2147483648)", fixture.Diagnostic{Code: "22003", Message: "integer out of range"}),
		fixture.ImplicitError("INSERT INTO atomic VALUES(1),(NULL)", fixture.Diagnostic{Code: "23502", Message: `null value in column "i" of relation "atomic" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "atomic", Column: "i"}),
	} {
		step.ErrorStage = "execute"
		fixture.ScalarStep(t, p, step)
		if step.Diagnostic.Code == "22P02" {
			step.ErrorStage = "prepare"
		}
		fixture.ScalarStep(t, p, fixture.Prepared(step))
		fixture.ScalarStep(t, p, fixture.Select("SELECT i,7 FROM atomic", []string{"i", "?column?"}, []string{"INT4", "INT4"}, [][]any{{int64(99), int64(7)}}))
	}
}
