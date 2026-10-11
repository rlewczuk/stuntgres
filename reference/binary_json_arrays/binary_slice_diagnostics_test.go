package reference_test

import (
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBinarySliceDiagnostics(t *testing.T) {
	for route, nonContext := range []bool{false, true} {
		t.Run([]string{"context", "consumer"}[route], func(t *testing.T) {
			p := fixture.NewScenario(t)
			apply := func(t *testing.T, s fixture.Step) { s.NonContext = nonContext; p.Step(t, s) }
			apply(t, fixture.Exec("CREATE TABLE t(v BYTEA,s BYTEA)", 0))
			hint := "No function matches the given name and argument types. You might need to add explicit type casts."
			for _, populated := range []bool{false, true} {
				if populated {
					apply(t, fixture.Exec("INSERT INTO t VALUES(NULL,NULL)", 1))
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
						step := fixture.ImplicitError(prefix+tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.msg, Position: strconv.Itoa(pos), Hint: tc.hint})
						step.ErrorStage = "execute"
						apply(t, step)
						step.SelectRows = true
						apply(t, step)
						step.ErrorStage = "prepare"
						apply(t, fixture.Prepared(step))
					}
				}
			}
		})
	}
}

func TestReferenceBinarySliceAtomicity(t *testing.T) {
	for route, nonContext := range []bool{false, true} {
		t.Run([]string{"context", "consumer"}[route], func(t *testing.T) {
			p := fixture.NewScenario(t)
			apply := func(t *testing.T, s fixture.Step) { s.NonContext = nonContext; p.Step(t, s) }
			apply(t, fixture.Exec("CREATE TABLE t(id INTEGER,v BYTEA,s BYTEA,i INTEGER,n INTEGER)", 0))
			step := fixture.Select("SELECT substr(v,i,n) AS v FROM t", []string{"v"}, []string{"BYTEA"}, [][]any{})
			apply(t, step)
			apply(t, fixture.Prepared(step))
			emptyOverlay := fixture.Select("SELECT overlay(v PLACING s FROM i FOR n) AS v FROM t", []string{"v"}, []string{"BYTEA"}, nil)
			apply(t, emptyOverlay)
			apply(t, fixture.Prepared(emptyOverlay))
			apply(t, fixture.Exec("INSERT INTO t VALUES(1,'abcdef','XY',1,2),(2,'abcdef','XY',2,-1)", 2))
			apply(t, fixture.Exec("CREATE TABLE dst(v BYTEA)", 0))
			apply(t, fixture.Exec("INSERT INTO dst VALUES('sentinel')", 1))
			for _, q := range []string{"INSERT INTO dst SELECT substr(v,i,n) FROM t ORDER BY id", "UPDATE t SET v=substring(v FROM i FOR n) RETURNING v", "DELETE FROM t RETURNING substr(v,i,n)"} {
				step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22011", Message: "negative substring length not allowed"})
				step.SelectRows = true
				step.ErrorStage = "execute"
				apply(t, step)
				apply(t, fixture.Prepared(step))
				apply(t, fixture.Select("SELECT v FROM dst", []string{"v"}, []string{"BYTEA"}, [][]any{{"sentinel"}}))
				apply(t, fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "BYTEA"}, [][]any{{int64(1), "abcdef"}, {int64(2), "abcdef"}}))
			}
			for _, tc := range []struct{ q, code, msg string }{{"SELECT overlay(v PLACING s FROM 0) FROM t", "22011", "negative substring length not allowed"}, {"SELECT overlay(v,s,2147483647,1) FROM t", "22003", "integer out of range"}} {
				step := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.msg})
				step.SelectRows = true
				step.ErrorStage = "execute"
				apply(t, step)
				apply(t, fixture.Prepared(step))
			}
		})
	}
}
