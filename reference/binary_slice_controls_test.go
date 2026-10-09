package reference_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"stuntgres"
	"testing"
)

func TestReferenceBinarySliceReplayAndDeparse(t *testing.T) {
	for route, nonContext := range []bool{false, true} {
		t.Run([]string{"context", "consumer"}[route], func(t *testing.T) {
			p := newReferenceScenario(t)
			apply := func(t *testing.T, s referenceStep) { s.nonContext = nonContext; p.step(t, s) }
			run := func(t *testing.T, ctx context.Context, x referenceExecutor, r *referenceRecorder, s referenceStep) {
				s.nonContext = nonContext
				referenceRunStep(t, ctx, x, r, s)
			}
			for _, s := range []referenceStep{
				referenceExec("CREATE TABLE t(id INTEGER,v BYTEA,s BYTEA,i INTEGER,n INTEGER,e BYTEA,CONSTRAINT valid CHECK(overlay(v PLACING s FROM i FOR n)=e))", 0),
				referenceExec("INSERT INTO t VALUES(1,'abcdef','XY',3,2,'abXYef')", 1),
				referenceExec(`CREATE VIEW sliced AS SELECT overlay(v PLACING '\x0080ff' FROM i FOR n) AS o,substring(v FROM i FOR n) AS b,position(s IN v) AS p,trim('\x00' FROM v) AS t,pg_catalog.position(v,'\x63') AS c FROM t`, 0),
			} {
				apply(t, s)
			}
			expected := referenceSelect("SELECT * FROM sliced", []string{"o", "b", "p", "t", "c"}, []string{"BYTEA", "BYTEA", "INT4", "BYTEA", "INT4"}, [][]any{{"ab\x00\x80\xffef", "cd", int64(0), "abcdef", int64(3)}})
			apply(t, expected)
			apply(t, referenceSelect("SELECT pg_get_expr(conbin,conrelid),pg_get_expr(conbin,conrelid,true),pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='valid'", []string{"pg_get_expr", "pg_get_expr", "pg_get_constraintdef"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"(OVERLAY(v PLACING s FROM i FOR n) = e)", "OVERLAY(v PLACING s FROM i FOR n) = e", "CHECK ((OVERLAY(v PLACING s FROM i FOR n) = e))"}}))
			held, err := p.native.Query("SELECT * FROM sliced")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			run(t, p.ctx, p.memory, p.memoryRecord, expected)
			if !held.Next() {
				t.Fatal(held.Err())
			}
			got := make([]any, 5)
			dest := make([]any, 5)
			for i := range got {
				dest[i] = &got[i]
			}
			raw := []any{[]byte{'a', 'b', 0, 128, 255, 'e', 'f'}, []byte("cd"), int64(0), []byte("abcdef"), int64(3)}
			if err := held.Scan(dest...); err != nil || !reflect.DeepEqual(got, raw) {
				t.Fatal(got, err)
			}
			got[0].([]byte)[0] = 99
			if held.Next() || held.Err() != nil {
				t.Fatal(held.Err())
			}
			if err := held.Close(); err != nil {
				t.Fatal(err)
			}
			run(t, p.ctx, p.memory, p.memoryRecord, expected)
			_, err = p.native.Exec("INSERT INTO t VALUES(2,'abcdef','XY',3,2,'wrong')")
			referenceState(t, err, "23514")
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewBufferString("CREATE TABLE sentinel(v BYTEA); INSERT INTO sentinel VALUES('x'); SELECT substr('abc'::bytea,1,-1);")); err == nil {
				t.Fatal("failed Restore accepted")
			}
			run(t, p.ctx, p.memory, p.memoryRecord, expected)
			var after bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil || after.String() != dump.String() {
				t.Fatal("failed Restore changed catalog", err)
			}
			canceled, cancel := context.WithCancel(p.ctx)
			cancel()
			rows, err := p.native.QueryContext(canceled, "SELECT substr(v,i,n) FROM t")
			if rows != nil {
				rows.Close()
				t.Fatal("canceled query published cursor")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			run(t, p.ctx, p.memory, p.memoryRecord, expected)
		})
	}
}
