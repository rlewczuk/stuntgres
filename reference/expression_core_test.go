package reference_test

import (
	"testing"
)

func expressionReferenceSetup(t *testing.T) *referencePair {
	p := newReferenceScenario(t)
	scalarStep(t, p, referenceExec("CREATE TABLE t(id INTEGER,v INTEGER,b BIGINT,s TEXT,f BOOLEAN)", 0))
	scalarStep(t, p, referenceExec("INSERT INTO t VALUES(1,1,2147483648,'x',TRUE),(2,NULL,2,'x',NULL),(3,2,3,'y',FALSE),(4,1,4,'x',TRUE)", 4))
	return p
}
func TestReferenceExpressionCoreScalars(t *testing.T) {
	p := expressionReferenceSetup(t)
	all := [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}
	cases := []referenceStep{
		referenceSelect("SELECT id,1,2147483648,'x',TRUE,FALSE,NULL FROM t ORDER BY id", []string{"id", "?column?", "?column?", "?column?", "?column?", "?column?", "?column?"}, []string{"INT4", "INT4", "INT8", "TEXT", "BOOL", "BOOL", "TEXT"}, [][]any{{int64(1), int64(1), int64(2147483648), "x", true, false, nil}, {int64(2), int64(1), int64(2147483648), "x", true, false, nil}, {int64(3), int64(1), int64(2147483648), "x", true, false, nil}, {int64(4), int64(1), int64(2147483648), "x", true, false, nil}}),
		referenceSelect("SELECT v FROM t WHERE v=$1", []string{"v"}, []string{"INT4"}, nil, nil),
		referenceSelect("SELECT id FROM t WHERE v=2147483648", []string{"id"}, []string{"INT4"}, nil),
		referenceSelect("SELECT id FROM t WHERE v=$1 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(4)}}, 1),
		referenceSelect("SELECT id FROM t WHERE v=$1 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}}, 2),
		referenceSelect("SELECT $1,$1 FROM t ORDER BY id", []string{"?column?", "?column?"}, []string{"TEXT", "TEXT"}, [][]any{{"hi", "hi"}, {"hi", "hi"}, {"hi", "hi"}, {"hi", "hi"}}, "hi"),
		referenceSelect("SELECT $1,$1 FROM t ORDER BY id", []string{"?column?", "?column?"}, []string{"TEXT", "TEXT"}, [][]any{{nil, nil}, {nil, nil}, {nil, nil}, {nil, nil}}, nil),
		referenceSelect("SELECT $1 FROM t WHERE s=$1 ORDER BY id", []string{"?column?"}, []string{"TEXT"}, [][]any{{"x"}, {"x"}, {"x"}}, "x"),
		referenceSelect("SELECT id FROM t WHERE TRUE ORDER BY id", []string{"id"}, []string{"INT4"}, all),
		referenceSelect("SELECT id FROM t WHERE FALSE", []string{"id"}, []string{"INT4"}, nil),
		referenceSelect("SELECT id FROM t WHERE NULL", []string{"id"}, []string{"INT4"}, nil),
		referenceSelect("SELECT id FROM t WHERE f ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(4)}}),
		referenceSelect("SELECT id FROM t WHERE 'yes' ORDER BY id", []string{"id"}, []string{"INT4"}, all),
		referenceSelect("SELECT id FROM t WHERE 'no'", []string{"id"}, []string{"INT4"}, nil),
		referenceSelect("SELECT id FROM t WHERE $1 ORDER BY id", []string{"id"}, []string{"INT4"}, all, true),
		referenceSelect("SELECT id FROM t WHERE $1", []string{"id"}, []string{"INT4"}, nil, false),
		referenceSelect("SELECT id FROM t WHERE $1", []string{"id"}, []string{"INT4"}, nil, nil),
	}
	for _, step := range cases {
		scalarStep(t, p, step)
		scalarStep(t, p, referencePrepared(step))
	}
}
func TestReferenceExpressionCoreDiagnostics(t *testing.T) {
	p := expressionReferenceSetup(t)
	bad := referenceImplicitError("SELECT id FROM t WHERE $1", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type boolean: "truth"`, Context: "unnamed portal parameter $1 = '...'"}, "truth")
	bad.selectRows = true
	bad.errorStage = "execute"
	scalarStep(t, p, bad)
	scalarStep(t, p, referencePrepared(bad))
	scalarStep(t, p, referenceExec("CREATE TABLE atomic(i INT NOT NULL)", 0))
	scalarStep(t, p, referenceExec("INSERT INTO atomic VALUES(99)", 1))
	for _, step := range []referenceStep{
		referenceImplicitError("INSERT INTO atomic VALUES(1),('oops')", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "31"}),
		referenceImplicitError("INSERT INTO atomic VALUES(1),(2147483648)", referenceDiagnostic{Code: "22003", Message: "integer out of range"}),
		referenceImplicitError("INSERT INTO atomic VALUES(1),(NULL)", referenceDiagnostic{Code: "23502", Message: `null value in column "i" of relation "atomic" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "atomic", Column: "i"}),
	} {
		step.errorStage = "execute"
		scalarStep(t, p, step)
		if step.diagnostic.Code == "22P02" {
			step.errorStage = "prepare"
		}
		scalarStep(t, p, referencePrepared(step))
		scalarStep(t, p, referenceSelect("SELECT i,7 FROM atomic", []string{"i", "?column?"}, []string{"INT4", "INT4"}, [][]any{{int64(99), int64(7)}}))
	}
}
