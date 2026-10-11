package reference_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBinarySliceReplayAndDeparse(t *testing.T) {
	for route, nonContext := range []bool{false, true} {
		t.Run([]string{"context", "consumer"}[route], func(t *testing.T) {
			p := fixture.NewScenario(t)
			apply := func(t *testing.T, s fixture.Step) { s.NonContext = nonContext; p.Step(t, s) }
			run := func(t *testing.T, ctx context.Context, x fixture.Executor, r *fixture.Recorder, s fixture.Step) {
				s.NonContext = nonContext
				fixture.RunStep(t, ctx, x, r, s)
			}
			for _, s := range []fixture.Step{
				fixture.Exec("CREATE TABLE t(id INTEGER,v BYTEA,s BYTEA,i INTEGER,n INTEGER,e BYTEA,CONSTRAINT valid CHECK(overlay(v PLACING s FROM i FOR n)=e))", 0),
				fixture.Exec("INSERT INTO t VALUES(1,'abcdef','XY',3,2,'abXYef')", 1),
				fixture.Exec(`CREATE VIEW sliced AS SELECT overlay(v PLACING '\x0080ff' FROM i FOR n) AS o,substring(v FROM i FOR n) AS b,position(s IN v) AS p,trim('\x00' FROM v) AS t,pg_catalog.position(v,'\x63') AS c FROM t`, 0),
			} {
				apply(t, s)
			}
			expected := fixture.Select("SELECT * FROM sliced", []string{"o", "b", "p", "t", "c"}, []string{"BYTEA", "BYTEA", "INT4", "BYTEA", "INT4"}, [][]any{{"ab\x00\x80\xffef", "cd", int64(0), "abcdef", int64(3)}})
			apply(t, expected)
			apply(t, fixture.Select("SELECT pg_get_expr(conbin,conrelid),pg_get_expr(conbin,conrelid,true),pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='valid'", []string{"pg_get_expr", "pg_get_expr", "pg_get_constraintdef"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"(OVERLAY(v PLACING s FROM i FOR n) = e)", "OVERLAY(v PLACING s FROM i FOR n) = e", "CHECK ((OVERLAY(v PLACING s FROM i FOR n) = e))"}}))
			held, err := p.Native.Query("SELECT * FROM sliced")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			run(t, p.Ctx, p.Memory, p.MemoryRecord, expected)
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
			run(t, p.Ctx, p.Memory, p.MemoryRecord, expected)
			_, err = p.Native.Exec("INSERT INTO t VALUES(2,'abcdef','XY',3,2,'wrong')")
			fixture.State(t, err, "23514")
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString("CREATE TABLE sentinel(v BYTEA); INSERT INTO sentinel VALUES('x'); SELECT substr('abc'::bytea,1,-1);")); err == nil {
				t.Fatal("failed Restore accepted")
			}
			run(t, p.Ctx, p.Memory, p.MemoryRecord, expected)
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || after.String() != dump.String() {
				t.Fatal("failed Restore changed catalog", err)
			}
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := p.Native.QueryContext(canceled, "SELECT substr(v,i,n) FROM t")
			if rows != nil {
				rows.Close()
				t.Fatal("canceled query published cursor")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			run(t, p.Ctx, p.Memory, p.MemoryRecord, expected)
		})
	}
}
