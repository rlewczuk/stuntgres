package reference_test

import (
	"strconv"
	"strings"
	"testing"
)

func TestReferenceOIDConsumers(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE one(s SMALLINT,i INT,b BIGINT,v OID)", 0))
	p.step(t, referenceExec("INSERT INTO one VALUES(-1,-1,4294967295,'4294967295')", 1))
	p.step(t, referenceExec("CREATE TABLE dest(i INT,b BIGINT,v OID)", 0))
	p.step(t, referenceExec("INSERT INTO dest SELECT v,v,i FROM one", 1))
	p.step(t, referenceExec("CREATE TABLE grouped(v OID)", 0))
	p.step(t, referenceExec("INSERT INTO grouped VALUES('0'),('4294967295'),('4294967295'),(NULL),(NULL)", 5))
	for _, backend := range p.backends() {
		for _, q := range []string{"SELECT CAST(i AS oid),s::oid,b::oid,v::int4,v::int8,v::text,oid('0377') FROM one", "SELECT i::oid,s::oid,CAST(b AS oid),CAST(v AS int4),CAST(v AS int8),CAST(v AS text),oid('0377') FROM one"} {
			step := referenceSelect(q, []string{"i", "s", "b", "v", "v", "v", "oid"}, []string{"OID", "OID", "OID", "INT4", "INT8", "TEXT", "OID"}, [][]any{{int64(4294967295), int64(4294967295), int64(4294967295), int64(-1), int64(4294967295), "4294967295", int64(255)}})
			oidReferenceRows(t, p.ctx, backend, backend.db, step)
		}
		for _, step := range []referenceStep{
			referenceSelect("SELECT * FROM dest", []string{"i", "b", "v"}, []string{"INT4", "INT8", "OID"}, [][]any{{int64(-1), int64(4294967295), int64(4294967295)}}),
			referenceSelect("SELECT CASE WHEN TRUE THEN v ELSE i END,COALESCE(NULL::oid,v),NULLIF(v,i) FROM one", []string{"i", "coalesce", "nullif"}, []string{"OID", "OID", "OID"}, [][]any{{int64(4294967295), int64(4294967295), nil}}),
			referenceSelect("SELECT v,count(*) FROM grouped GROUP BY v ORDER BY v", []string{"v", "count"}, []string{"OID", "INT8"}, [][]any{{int64(0), int64(1)}, {int64(4294967295), int64(2)}, {nil, int64(2)}}),
		} {
			oidReferenceRows(t, p.ctx, backend, backend.db, step)
		}
	}
	for _, q := range []string{"SELECT v>0,0<v,v>=i,i<=v,v<>0,v!=0 FROM one", "SELECT v<v,v<=v,v=v,v<>v,v>v,v>=v,v=NULL FROM one"} {
		want := []any{true, true, true, true, true, true}
		if strings.Contains(q, "v<v") {
			want = []any{false, true, true, false, false, true, nil}
		}
		columns, types := make([]string, len(want)), make([]string, len(want))
		for i := range want {
			columns[i], types[i] = "?column?", "BOOL"
		}
		p.step(t, referenceSelect(q, columns, types, [][]any{want}))
	}
	p.step(t, referenceSelect("SELECT '2147483648'::oid::int4,'2147483648'::oid::int8,NULL::oid::int8 FROM one", []string{"int4", "int8", "int8"}, []string{"INT4", "INT8", "INT8"}, [][]any{{int64(-2147483648), int64(2147483648), nil}}))
	p.step(t, referenceSelect("SELECT v IS NULL,v IS DISTINCT FROM NULL FROM grouped ORDER BY v", []string{"?column?", "?column?"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}, {false, true}, {false, true}, {true, false}, {true, false}}))
	for _, q := range []string{"SELECT (-1)::int8::oid FROM one", "SELECT v=(-1)::int8 FROM one", "SELECT (-1)::int8=v FROM one", "INSERT INTO grouped VALUES(4294967296::int8)"} {
		p.step(t, referenceImplicitError(q, referenceDiagnostic{Code: "22003", Message: "OID out of range"}))
	}
	for _, q := range []string{"SELECT v::smallint FROM one", "SELECT NULL::oid::smallint FROM one"} {
		failure := referenceImplicitError(q, referenceDiagnostic{Code: "42846", Message: "cannot cast type oid to smallint", Position: strconv.Itoa(strings.LastIndex(q, "::") + 1)})
		failure.errorStage = "prepare"
		p.step(t, referencePrepared(failure))
	}
	for _, backend := range p.backends() {
		cell := any(int64(1))
		if backend.pg {
			cell = "1"
		} // pq owned decimal OID bytes normalize to string.
		success := referenceSelect("SELECT '1'::oid", []string{"oid"}, []string{"OID"}, [][]any{{cell}})
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, success)
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(success))
	}
}
