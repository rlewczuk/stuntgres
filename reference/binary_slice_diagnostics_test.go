//go:build reference

package reference_test

import (
	"strconv"
	"testing"
)

func TestReferenceBinarySliceDiagnostics(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(v BYTEA,s BYTEA)", 0))
	hint := "No function matches the given name and argument types. You might need to add explicit type casts."
	for _, populated := range []bool{false, true} {
		if populated {
			p.step(t, referenceExec("INSERT INTO t VALUES(NULL,NULL)", 1))
		}
		for _, tc := range []struct {
			q, code, msg string
			pos          int
			hint         string
		}{
			{"SELECT substring(v FROM TRUE) FROM t", "42883", "function pg_catalog.substring(bytea, boolean) does not exist", 8, hint},
			{"SELECT substring(v,TRUE) FROM t", "42883", "function substring(bytea, boolean) does not exist", 8, hint},
			{"SELECT trim(v) FROM t", "42883", "function pg_catalog.btrim(bytea) does not exist", 8, hint},
			{"SELECT substr(v,'bad') FROM t", "22P02", `invalid input syntax for type integer: "bad"`, 17, ""},
			{"SELECT substring(v FROM 'bad') FROM t", "22P02", `invalid input syntax for type integer: "bad"`, 25, ""},
			{`SELECT overlay(v PLACING '\q' FROM 1) FROM t`, "22P02", "invalid input syntax for type bytea", 26, ""},
			{"SELECT position(v,s) FROM t", "42601", `syntax error at or near ","`, 18, ""},
			{"SELECT substr(v,1::bigint) FROM t", "42883", "function substr(bytea, bigint) does not exist", 8, hint},
			{"SELECT pg_catalog.trim(v,s) FROM t", "42883", "function pg_catalog.trim(bytea, bytea) does not exist", 8, hint},
		} {
			for _, prefix := range []string{"", "/*é*/ "} {
				pos := tc.pos
				if prefix != "" {
					pos += 6
				}
				step := referenceImplicitError(prefix+tc.q, referenceDiagnostic{Code: tc.code, Message: tc.msg, Position: strconv.Itoa(pos), Hint: tc.hint})
				step.errorStage = "execute"
				p.step(t, step)
				step.selectRows = true
				p.step(t, step)
				step.errorStage = "prepare"
				p.step(t, referencePrepared(step))
			}
		}
	}
}

func TestReferenceBinarySliceAtomicity(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(id INTEGER,v BYTEA,s BYTEA,i INTEGER,n INTEGER)", 0))
	step := referenceSelect("SELECT substr(v,i,n) AS v FROM t", []string{"v"}, []string{"BYTEA"}, [][]any{})
	p.step(t, step)
	p.step(t, referencePrepared(step))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,'abcdef','XY',1,2),(2,'abcdef','XY',2,-1)", 2))
	p.step(t, referenceExec("CREATE TABLE dst(v BYTEA)", 0))
	p.step(t, referenceExec("INSERT INTO dst VALUES('sentinel')", 1))
	for _, q := range []string{"INSERT INTO dst SELECT substr(v,i,n) FROM t ORDER BY id", "UPDATE t SET v=substring(v FROM i FOR n) RETURNING v", "DELETE FROM t RETURNING substr(v,i,n)"} {
		step := referenceImplicitError(q, referenceDiagnostic{Code: "22011", Message: "negative substring length not allowed"})
		step.selectRows = true
		step.errorStage = "execute"
		p.step(t, step)
		p.step(t, referencePrepared(step))
		p.step(t, referenceSelect("SELECT v FROM dst", []string{"v"}, []string{"BYTEA"}, [][]any{{"sentinel"}}))
		p.step(t, referenceSelect("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "BYTEA"}, [][]any{{int64(1), "abcdef"}, {int64(2), "abcdef"}}))
	}
	for _, tc := range []struct{ q, code, msg string }{{"SELECT overlay(v PLACING s FROM 0) FROM t", "22011", "negative substring length not allowed"}, {"SELECT overlay(v,s,2147483647,1) FROM t", "22003", "integer out of range"}} {
		step := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.msg})
		step.selectRows = true
		step.errorStage = "execute"
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
}
