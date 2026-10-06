//go:build reference

package reference_test

import "testing"

func TestReferenceArithmeticBoundaries(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE x(s SMALLINT,s2 SMALLINT,n NUMERIC,n2 NUMERIC,r REAL,r2 REAL,d DOUBLE PRECISION,d2 DOUBLE PRECISION)", 0))
	p.step(t, referenceExec("INSERT INTO x VALUES(7,2,7,2,7,2,7,2)", 1))
	for _, tc := range []struct {
		expr, typ, diagnostic string
		value                 any
		pos                   string
	}{{"s+s2", "INT2", "smallint", int64(9), "9"}, {"s/s2", "INT2", "smallint", int64(3), "9"}, {"s%s2", "INT2", "smallint", int64(1), "9"}, {"s+2", "INT4", "smallint", int64(9), "9"}, {"+s", "INT2", "smallint", int64(7), "8"}, {"-s", "INT2", "smallint", int64(-7), "8"}, {"n+n2", "NUMERIC", "numeric", "9", "9"}, {"n*n2", "NUMERIC", "numeric", "14", "9"}, {"n%n2", "NUMERIC", "numeric", "1", "9"}, {"r/r2", "FLOAT4", "real", float64(3.5), "9"}, {"d/d2", "FLOAT8", "double precision", float64(3.5), "9"}, {"+r", "FLOAT4", "real", float64(7), "8"}, {"-d", "FLOAT8", "double precision", float64(-7), "8"}} {
		q := "SELECT " + tc.expr + " FROM x"
		success := referenceSelect(q, []string{"?column?"}, []string{tc.typ}, [][]any{{tc.value}})
		failure := referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: "arithmetic for " + tc.diagnostic + " operands is unsupported", Position: tc.pos})
		failure.selectRows = true
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, success)
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referencePrepared(success))
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, failure)
		failure.errorStage = "prepare"
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referencePrepared(failure))
	}
	for _, tc := range []struct {
		q, signature, pos string
		args              []any
	}{{"SELECT -$1 FROM x", "- unknown", "8", []any{7}}, {"SELECT $1+$2 FROM x", "unknown + unknown", "10", []any{7, 2}}, {"SELECT NULL+NULL FROM x", "unknown + unknown", "12", nil}, {"SELECT $1%$2 FROM x", "unknown % unknown", "10", []any{7, 2}}} {
		pg := referenceImplicitError(tc.q, referenceDiagnostic{Code: "42725", Message: "operator is not unique: " + tc.signature, Position: tc.pos, Hint: "Could not choose a best candidate operator. You might need to add explicit type casts."}, tc.args...)
		pg.selectRows = true
		native := referenceImplicitError(tc.q, referenceDiagnostic{Code: "0A000", Message: "integer arithmetic requires known integer or bigint operands", Position: tc.pos}, tc.args...)
		native.selectRows = true
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, pg)
		pg.errorStage = "prepare"
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referencePrepared(pg))
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, native)
		native.errorStage = "prepare"
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referencePrepared(native))
	}
	for _, arg := range []any{7, nil} {
		want := any(float64(7))
		if arg == nil {
			want = nil
		}
		pg := referencePrepared(referenceSelect("SELECT +$1 FROM x", []string{"?column?"}, []string{"FLOAT8"}, [][]any{{want}}, arg))
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, pg)
		native := referencePrepared(referenceImplicitError("SELECT +$1 FROM x", referenceDiagnostic{Code: "0A000", Message: "integer arithmetic requires known integer or bigint operands", Position: "8"}, arg))
		native.selectRows = true
		native.errorStage = "prepare"
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, native)
	}
}
