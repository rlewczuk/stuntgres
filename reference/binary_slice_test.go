package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
)

func TestReferenceBinarySliceBehavior(t *testing.T) {
	for route, nonContext := range []bool{false, true} {
		t.Run([]string{"context", "consumer"}[route], func(t *testing.T) {
			p := newReferenceScenario(t)
			apply := func(t *testing.T, s referenceStep) { s.nonContext = nonContext; p.step(t, s) }
			run := func(t *testing.T, ctx context.Context, x referenceExecutor, r *referenceRecorder, s referenceStep) {
				s.nonContext = nonContext
				referenceRunStep(t, ctx, x, r, s)
			}
			apply(t, referenceExec("CREATE TABLE t(v BYTEA,s BYTEA,i INTEGER,n INTEGER)", 0))
			apply(t, referenceExec(`INSERT INTO t VALUES('\x00c3a9ff00414200','\xa9ff',2,1)`, 1))
			for _, step := range []referenceStep{
				referenceSelect(`SELECT substring(v FROM i FOR n),substr(v,3,1),position(s IN v),pg_catalog.position(v,''),pg_catalog.position(v,'zz') FROM t`, []string{"substring", "substr", "position", "position", "position"}, []string{"BYTEA", "BYTEA", "INT4", "INT4", "INT4"}, [][]any{{"\xc3", "\xa9", int64(3), int64(1), int64(0)}}),
				referenceSelect(`SELECT substring(v,2,1) AS a,substr(v,3,1) AS b,pg_catalog.position(v,s) AS p FROM t`, []string{"a", "b", "p"}, []string{"BYTEA", "BYTEA", "INT4"}, [][]any{{"\xc3", "\xa9", int64(3)}}),
				referenceSelect(`SELECT substring(v FROM 2 FOR 1) AS a,substring(v FOR 1 FROM 3) AS b,position(s IN v) AS p FROM t`, []string{"a", "b", "p"}, []string{"BYTEA", "BYTEA", "INT4"}, [][]any{{"\xc3", "\xa9", int64(3)}}),
				referenceSelect(`SELECT overlay('abcdef'::bytea,'XY'::bytea,3,-2) AS a,overlay('abcdef'::bytea PLACING 'XY'::bytea FROM 3) AS b,substring('abcdef'::bytea FOR 3) AS c`, []string{"a", "b", "c"}, []string{"BYTEA", "BYTEA", "BYTEA"}, [][]any{{"abXYabcdef", "abXYef", "abc"}}),
				referenceSelect(`SELECT trim('\xff8000'::bytea FROM '\x00ff8041008042ff00'::bytea) AS b,trim(LEADING '\xff8000'::bytea FROM '\x00ff8041008042ff00'::bytea) AS l,trim(TRAILING '\x00ff8041008042ff00'::bytea,'\xff8000'::bytea) AS r`, []string{"b", "l", "r"}, []string{"BYTEA", "BYTEA", "BYTEA"}, [][]any{{"A\x00\x80B", "A\x00\x80B\xff\x00", "\x00\xff\x80A\x00\x80B"}}),
				referenceSelect(`SELECT pg_catalog.position(''::bytea,''::bytea) AS a,pg_catalog.position('abc'::bytea,'z'::bytea) AS b,substr('abcdef'::bytea,0,3) AS c,substring('abcdef'::bytea,-2,4) AS d`, []string{"a", "b", "c", "d"}, []string{"INT4", "INT4", "BYTEA", "BYTEA"}, [][]any{{int64(1), int64(0), "ab", "a"}}),
				referenceSelect(`SELECT overlay(NULL::bytea,'XY'::bytea,3,2) AS a,substring('abc'::bytea,1,NULL::integer) AS b,pg_catalog.position('abc'::bytea,NULL::bytea) AS c,trim(NULL::bytea FROM 'abc'::bytea) AS d`, []string{"a", "b", "c", "d"}, []string{"BYTEA", "BYTEA", "INT4", "BYTEA"}, [][]any{{nil, nil, nil, nil}}),
			} {
				for _, backend := range p.backends() {
					step.checkMetadata = func(t *testing.T, rows *sql.Rows) {
						metadata, err := rows.ColumnTypes()
						if err != nil || len(metadata) != len(step.types) {
							t.Fatal(metadata, err)
						}
						for i, typ := range metadata {
							scan := reflect.TypeFor[[]byte]()
							if step.types[i] == "INT4" {
								scan = reflect.TypeFor[int64]()
								if backend.pg {
									scan = reflect.TypeFor[int32]()
								}
							}
							if typ.DatabaseTypeName() != step.types[i] || typ.ScanType() != scan {
								t.Fatal(i, typ)
							}
						}
					}
					run(t, p.ctx, backend.db, backend.recorder, step)
					run(t, p.ctx, backend.db, backend.recorder, referencePrepared(step))
				}
			}
		})
	}
}
