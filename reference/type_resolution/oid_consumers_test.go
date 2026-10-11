package reference_test

import (
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceOIDConsumers(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE one(s SMALLINT,i INT,b BIGINT,v OID)", 0))
	p.Step(t, fixture.Exec("INSERT INTO one VALUES(-1,-1,4294967295,'4294967295')", 1))
	p.Step(t, fixture.Exec("CREATE TABLE dest(i INT,b BIGINT,v OID)", 0))
	p.Step(t, fixture.Exec("INSERT INTO dest SELECT v,v,i FROM one", 1))
	p.Step(t, fixture.Exec("CREATE TABLE grouped(v OID)", 0))
	p.Step(t, fixture.Exec("INSERT INTO grouped VALUES('0'),('4294967295'),('4294967295'),(NULL),(NULL)", 5))
	for _, backend := range p.Backends() {
		for _, q := range []string{"SELECT CAST(i AS oid),s::oid,b::oid,v::int4,v::int8,v::text,oid('0377') FROM one", "SELECT i::oid,s::oid,CAST(b AS oid),CAST(v AS int4),CAST(v AS int8),CAST(v AS text),oid('0377') FROM one"} {
			step := fixture.Select(q, []string{"i", "s", "b", "v", "v", "v", "oid"}, []string{"OID", "OID", "OID", "INT4", "INT8", "TEXT", "OID"}, [][]any{{int64(4294967295), int64(4294967295), int64(4294967295), int64(-1), int64(4294967295), "4294967295", int64(255)}})
			fixture.OidReferenceRows(t, p.Ctx, backend, backend.DB, step)
		}
		for _, step := range []fixture.Step{
			fixture.Select("SELECT * FROM dest", []string{"i", "b", "v"}, []string{"INT4", "INT8", "OID"}, [][]any{{int64(-1), int64(4294967295), int64(4294967295)}}),
			fixture.Select("SELECT CASE WHEN TRUE THEN v ELSE i END,COALESCE(NULL::oid,v),NULLIF(v,i) FROM one", []string{"i", "coalesce", "nullif"}, []string{"OID", "OID", "OID"}, [][]any{{int64(4294967295), int64(4294967295), nil}}),
			fixture.Select("SELECT v,count(*) FROM grouped GROUP BY v ORDER BY v", []string{"v", "count"}, []string{"OID", "INT8"}, [][]any{{int64(0), int64(1)}, {int64(4294967295), int64(2)}, {nil, int64(2)}}),
		} {
			fixture.OidReferenceRows(t, p.Ctx, backend, backend.DB, step)
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
		p.Step(t, fixture.Select(q, columns, types, [][]any{want}))
	}
	p.Step(t, fixture.Select("SELECT '2147483648'::oid::int4,'2147483648'::oid::int8,NULL::oid::int8 FROM one", []string{"int4", "int8", "int8"}, []string{"INT4", "INT8", "INT8"}, [][]any{{int64(-2147483648), int64(2147483648), nil}}))
	p.Step(t, fixture.Select("SELECT v IS NULL,v IS DISTINCT FROM NULL FROM grouped ORDER BY v", []string{"?column?", "?column?"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}, {false, true}, {false, true}, {true, false}, {true, false}}))
	for _, q := range []string{"SELECT (-1)::int8::oid FROM one", "SELECT v=(-1)::int8 FROM one", "SELECT (-1)::int8=v FROM one", "INSERT INTO grouped VALUES(4294967296::int8)"} {
		p.Step(t, fixture.ImplicitError(q, fixture.Diagnostic{Code: "22003", Message: "OID out of range"}))
	}
	for _, q := range []string{"SELECT v::smallint FROM one", "SELECT NULL::oid::smallint FROM one"} {
		failure := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42846", Message: "cannot cast type oid to smallint", Position: strconv.Itoa(strings.LastIndex(q, "::") + 1)})
		failure.ErrorStage = "prepare"
		p.Step(t, fixture.Prepared(failure))
	}
	for _, backend := range p.Backends() {
		cell := any(int64(1))
		if backend.PG {
			cell = "1"
		} // pq owned decimal OID bytes normalize to string.
		success := fixture.Select("SELECT '1'::oid", []string{"oid"}, []string{"OID"}, [][]any{{cell}})
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, success)
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(success))
	}
}
