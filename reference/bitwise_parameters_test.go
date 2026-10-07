//go:build reference

package reference_test

import "testing"

func TestReferenceBitwiseParameterResolution(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(s SMALLINT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1)", 1))
	for _, q := range []string{"SELECT s & $1, CASE WHEN $1 THEN 1 ELSE 0 END FROM t"} {
		s := referenceImplicitError(q, referenceDiagnostic{Code: "42804", Message: "argument of CASE/WHEN must be type boolean, not type smallint", Position: "26"}, 1)
		s.selectRows = true
		p.step(t, s)
		s.errorStage = "prepare"
		p.step(t, referencePrepared(s))
	}
	for _, tc := range []struct {
		q, message, pos string
		args            []any
	}{
		{"SELECT ~$1 FROM t", "operator is not unique: ~ unknown", "8", []any{1}},
		{"SELECT $1 & $2 FROM t", "operator is not unique: unknown & unknown", "11", []any{1, 1}},
		{"SELECT $1 << 1::smallint FROM t", "operator is not unique: unknown << smallint", "11", []any{1}},
		{"SELECT $1 >> 1::smallint FROM t", "operator is not unique: unknown >> smallint", "11", []any{1}},
	} {
		s := referenceImplicitError(tc.q, referenceDiagnostic{Code: "42725", Message: tc.message, Position: tc.pos, Hint: "Could not choose a best candidate operator. You might need to add explicit type casts."}, tc.args...)
		s.selectRows = true
		p.step(t, s)
		s.errorStage = "prepare"
		p.step(t, referencePrepared(s))
	}
	for _, q := range []string{"SELECT s << 1::smallint FROM t", "SELECT s >> 1::smallint FROM t"} {
		want := int64(2)
		if q == "SELECT s >> 1::smallint FROM t" {
			want = 0
		}
		step := referenceSelect(q, []string{"?column?"}, []string{"INT2"}, [][]any{{want}})
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
}

func TestReferenceBitwiseScopedSmallintCasts(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE one(v INTEGER)", 0))
	p.step(t, referenceExec("INSERT INTO one VALUES(1)", 1))
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct{ query, position string }{
			{"SELECT 1::smallint FROM one", "9"},
			{"SELECT CAST(1 AS smallint) FROM one", "8"},
		} {
			native := referenceImplicitError(tc.query, referenceDiagnostic{Code: "0A000", Message: "explicit casts are supported only for int4, int8, text and bool", Position: tc.position})
			native.selectRows = true
			native.prepared = prepared
			if prepared {
				native.errorStage = "prepare"
			}
			referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, native)
			postgres := referenceSelect(native.query, []string{"int2"}, []string{"INT2"}, [][]any{{int64(1)}})
			postgres.prepared = prepared
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, postgres)
		}
		step := referenceSelect("SELECT ~CAST(1 AS smallint),CAST(32767 AS smallint) << CAST(1 AS smallint),CAST(32767::smallint << 1 AS bigint),~NULL::smallint FROM one", []string{"?column?", "?column?", "int8", "?column?"}, []string{"INT2", "INT2", "INT8", "INT2"}, [][]any{{int64(-2), int64(-2), int64(-2), nil}})
		step.prepared = prepared
		p.step(t, step)
	}
}
