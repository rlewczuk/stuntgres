//go:build reference

package reference_test

import "testing"

func TestReferenceBinarySliceBehavior(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(v BYTEA,s BYTEA)", 0))
	p.step(t, referenceExec(`INSERT INTO t VALUES('\x00c3a9ff00414200','\xa9ff')`, 1))
	for _, step := range []referenceStep{
		referenceSelect(`SELECT substring(v,2,1) AS a,substr(v,3,1) AS b,pg_catalog.position(v,s) AS p FROM t`, []string{"a", "b", "p"}, []string{"BYTEA", "BYTEA", "INT4"}, [][]any{{"\xc3", "\xa9", int64(3)}}),
		referenceSelect(`SELECT substring(v FROM 2 FOR 1) AS a,substring(v FOR 1 FROM 3) AS b,position(s IN v) AS p FROM t`, []string{"a", "b", "p"}, []string{"BYTEA", "BYTEA", "INT4"}, [][]any{{"\xc3", "\xa9", int64(3)}}),
		referenceSelect(`SELECT overlay('abcdef'::bytea,'XY'::bytea,3,-2) AS a,overlay('abcdef'::bytea PLACING 'XY'::bytea FROM 3) AS b,substring('abcdef'::bytea FOR 3) AS c`, []string{"a", "b", "c"}, []string{"BYTEA", "BYTEA", "BYTEA"}, [][]any{{"abXYabcdef", "abXYef", "abc"}}),
		referenceSelect(`SELECT trim('\xff8000'::bytea FROM '\x00ff8041008042ff00'::bytea) AS b,trim(LEADING '\xff8000'::bytea FROM '\x00ff8041008042ff00'::bytea) AS l,trim(TRAILING '\x00ff8041008042ff00'::bytea,'\xff8000'::bytea) AS r`, []string{"b", "l", "r"}, []string{"BYTEA", "BYTEA", "BYTEA"}, [][]any{{"A\x00\x80B", "A\x00\x80B\xff\x00", "\x00\xff\x80A\x00\x80B"}}),
	} {
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
}
