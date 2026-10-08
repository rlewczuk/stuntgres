//go:build reference

package reference_test

import "testing"

func TestReferenceBinarySliceHeterogeneousNullAndOptionalCounts(t *testing.T) {
	p := newReferencePair(t)
	for _, tc := range []struct {
		expression string
		want       any
	}{
		{"overlay(NULL::bytea,'X'::bytea,0,1)", nil},
		{"overlay('abc'::bytea,NULL::bytea,0,1)", nil},
		{"overlay('abc'::bytea,'X'::bytea,NULL::integer,1)", nil},
		{"overlay('abc'::bytea,'X'::bytea,0,NULL::integer)", nil},
		{"substring(NULL::bytea,1,-1)", nil},
		{"substring('abc'::bytea,NULL::integer,-1)", nil},
		{"substring('abc'::bytea,1,NULL::integer)", nil},
		{"substr(NULL::bytea,1,-1)", nil},
		{"substr('abc'::bytea,NULL::integer,-1)", nil},
		{"substr('abc'::bytea,1,NULL::integer)", nil},
		{`overlay('abcdef'::bytea PLACING '\xc3a9'::bytea FROM 3)`, "ab\xc3\xa9ef"},
		{"overlay('abcdef'::bytea,''::bytea,3)", "abcdef"},
		{"overlay('abcdef'::bytea,''::bytea,2147483647)", "abcdef"},
		{"overlay('abcdef'::bytea,'X'::bytea,2147483647,0)", "abcdefX"},
	} {
		step := referenceSelect("SELECT "+tc.expression+" AS v", []string{"v"}, []string{"BYTEA"}, [][]any{{tc.want}})
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	for _, q := range []string{"SELECT overlay('abcdef'::bytea,'X'::bytea,2147483647)", "SELECT overlay('abcdef'::bytea,''::bytea,2147483647,1)"} {
		step := referenceImplicitError(q, referenceDiagnostic{Code: "22003", Message: "integer out of range"})
		step.selectRows = true
		step.errorStage = "execute"
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
}

func TestReferenceBinarySliceForOnlyInputPhase(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(v BYTEA)", 0))
	for _, populated := range []bool{false, true} {
		if populated {
			p.step(t, referenceExec("INSERT INTO t VALUES(NULL),('abcdef')", 2))
		}
		step := referenceImplicitError("SELECT substring(v FOR '1.5') FROM t", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "1.5"`, Position: "24"})
		step.selectRows = true
		step.errorStage = "execute"
		p.step(t, step)
		step.errorStage = "prepare"
		p.step(t, referencePrepared(step))
	}
}

func TestReferenceBinarySliceLateOverlayAtomicity(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(id INTEGER,v BYTEA,s BYTEA,i INTEGER,n INTEGER)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,'abcdef','XY',1,2),(2,'abcdef','XY',2147483647,1)", 2))
	p.step(t, referenceExec("CREATE TABLE dst(v BYTEA)", 0))
	p.step(t, referenceExec("INSERT INTO dst VALUES('sentinel')", 1))
	for _, q := range []string{"INSERT INTO dst SELECT overlay(v,s,i,n) FROM t ORDER BY id", "UPDATE t SET v=overlay(v PLACING s FROM i FOR n) RETURNING v", "DELETE FROM t RETURNING overlay(v,s,i,n)"} {
		step := referenceImplicitError(q, referenceDiagnostic{Code: "22003", Message: "integer out of range"})
		step.selectRows = true
		step.errorStage = "execute"
		p.step(t, step)
		p.step(t, referencePrepared(step))
		p.step(t, referenceSelect("SELECT v FROM dst", []string{"v"}, []string{"BYTEA"}, [][]any{{"sentinel"}}))
		p.step(t, referenceSelect("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "BYTEA"}, [][]any{{int64(1), "abcdef"}, {int64(2), "abcdef"}}))
	}
}

func TestReferenceBinarySliceOrdinaryPositionDeparse(t *testing.T) {
	p := newReferencePair(t)
	for _, tc := range []struct{ name, callee string }{{"quoted", `"position"`}, {"qualified", "pg_catalog.position"}} {
		p.step(t, referenceExec("CREATE TABLE p_"+tc.name+"(v BYTEA,s BYTEA,CONSTRAINT ck_"+tc.name+" CHECK("+tc.callee+"(v,s)>0))", 0))
		for _, mode := range []struct{ flag, want string }{{"false", `("position"(v, s) > 0)`}, {"true", `"position"(v, s) > 0`}} {
			expr := referenceSelect("SELECT pg_get_expr(conbin,conrelid,"+mode.flag+") AS d FROM pg_constraint WHERE conname='ck_"+tc.name+"'", []string{"d"}, []string{"TEXT"}, [][]any{{mode.want}})
			def := referenceSelect("SELECT pg_get_constraintdef(oid,"+mode.flag+") AS d FROM pg_constraint WHERE conname='ck_"+tc.name+"'", []string{"d"}, []string{"TEXT"}, [][]any{{"CHECK (" + mode.want + ")"}})
			p.step(t, expr)
			p.step(t, referencePrepared(expr))
			p.step(t, def)
			p.step(t, referencePrepared(def))
		}
		p.step(t, referenceExec("INSERT INTO p_"+tc.name+" VALUES('abcdef','cd')", 1))
		step := referenceSelect("SELECT "+tc.callee+"(v,s) AS p FROM p_"+tc.name+" WHERE "+tc.callee+"(v,s)>0", []string{"p"}, []string{"INT4"}, [][]any{{int64(3)}})
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
}
