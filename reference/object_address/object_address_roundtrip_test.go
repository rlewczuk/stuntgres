package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func objectAddressCore(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) objectAddressValue {
	t.Helper()
	objectAddressRun(t, p, b, c, fixture.Exec("CREATE TABLE addr_probe(a INTEGER,b TEXT)", 0))
	a := objectAddressWitness(t, p, b, c, 1259, "table column", []string{"public", "addr_probe", "b"}, nil, "public", "addr_probe", "public.addr_probe.b", "column b of table addr_probe", "SELECT 'addr_probe'::regclass::oid")
	if a.sub != 2 {
		t.Fatal("stable attribute number", a)
	}
	return a
}

func TestReferenceObjectAddressRoundTrips(t *testing.T) {
	objectAddressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		a := objectAddressCore(t, p, b, c)
		objectAddressWitness(t, p, b, c, 1259, "table", []string{"public", "addr_probe"}, nil, "public", "addr_probe", "public.addr_probe", "table addr_probe", "SELECT 'addr_probe'::regclass::oid")
		q := "SELECT i.identity,pg_describe_object(g.classid,g.objid,g.objsubid) AS description,g.classid::bigint AS class,g.objid::bigint AS object,g.objsubid FROM pg_identify_object_as_address(1259,'addr_probe'::regclass,2) a, LATERAL pg_get_object_address(a.type,a.object_names,a.object_args) g, LATERAL pg_identify_object(g.classid,g.objid,g.objsubid) i"
		objectAddressRun(t, p, b, c, fixture.Select(q, []string{"identity", "description", "class", "object", "objsubid"}, []string{"TEXT", "TEXT", "INT8", "INT8", "INT4"}, [][]any{{"public.addr_probe.b", "column b of table addr_probe", a.class, a.object, a.sub}}))
	})
}

func TestReferenceObjectAddressSignatures(t *testing.T) {
	objectAddressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		objectAddressCore(t, p, b, c)
		for _, prefix := range []string{"", "pg_catalog."} {
			for _, call := range []string{
				prefix + "pg_identify_object(1259,'addr_probe'::regclass,2)",
				prefix + "pg_identify_object(objsubid=>2,objid=>'addr_probe'::regclass,classid=>1259)",
			} {
				objectAddressRun(t, p, b, c, fixture.Select("SELECT ("+call+").identity AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"public.addr_probe.b"}}))
				objectAddressRun(t, p, b, c, fixture.Select("SELECT ("+call+").*", []string{"type", "schema", "name", "identity"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"table column", "public", "addr_probe", "public.addr_probe.b"}}))
			}
			objectAddressRun(t, p, b, c, fixture.Select("SELECT "+prefix+"pg_describe_object(1259,'addr_probe'::regclass,2) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"column b of table addr_probe"}}))
			objectAddressRun(t, p, b, c, fixture.Select("SELECT * FROM "+prefix+"pg_identify_object_as_address(objsubid=>2,classid=>1259,objid=>'addr_probe'::regclass)", []string{"type", "object_names", "object_args"}, []string{"TEXT", "_TEXT", "_TEXT"}, [][]any{{"table column", "{public,addr_probe,b}", "{}"}}))
			objectAddressRun(t, p, b, c, fixture.Select("SELECT ("+prefix+"pg_get_object_address(object_args=>'{}',object_names=>'{public,addr_probe,b}',type=>'table column')).objsubid AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}}))
		}
		objectAddressRun(t, p, b, c, fixture.Select("SELECT pg_identify_object(1259,'addr_probe'::regclass,2) AS v", []string{"v"}, []string{"RECORD"}, [][]any{{`("table column",public,addr_probe,public.addr_probe.b)`}}))
		objectAddressRun(t, p, b, c, fixture.Select("SELECT identity,n FROM pg_identify_object(1259,'addr_probe'::regclass,2) WITH ORDINALITY AS x(t,s,r,identity,n)", []string{"identity", "n"}, []string{"TEXT", "INT8"}, [][]any{{"public.addr_probe.b", int64(1)}}))
		objectAddressRun(t, p, b, c, fixture.Select("SELECT x.identity FROM (VALUES (1),(2)) AS n(i), LATERAL pg_identify_object(1259,'addr_probe'::regclass,n.i) x ORDER BY n.i", []string{"identity"}, []string{"TEXT"}, [][]any{{"public.addr_probe.a"}, {"public.addr_probe.b"}}))
		objectAddressRun(t, p, b, c, fixture.Select("SELECT a.identity,b.type FROM ROWS FROM (pg_identify_object(1259,'addr_probe'::regclass,2),pg_identify_object_as_address(1259,'addr_probe'::regclass,2)) AS a(t,s,n,identity,type,names,args), LATERAL pg_identify_object(1259,'addr_probe'::regclass,0) b", []string{"identity", "type"}, []string{"TEXT", "TEXT"}, [][]any{{"public.addr_probe.b", "table"}}))
		// PostgreSQL historically ignores VARIADIC after resolving a fixed
		// signature. These independent witnesses need fresh oracle proof.
		for _, tc := range []struct {
			q, typ string
			want   any
		}{
			{"SELECT (pg_identify_object(1259,'addr_probe'::regclass,VARIADIC 1)).identity AS v", "TEXT", "public.addr_probe.a"},
			{"SELECT (pg_identify_object_as_address(1259,'addr_probe'::regclass,VARIADIC 1)).object_names AS v", "_TEXT", "{public,addr_probe,a}"},
			{"SELECT pg_describe_object(1259,'addr_probe'::regclass,VARIADIC 1) AS v", "TEXT", "column a of table addr_probe"},
			{"SELECT (pg_get_object_address('type',ARRAY['integer'],VARIADIC '{}'::text[])).objid::bigint AS v", "INT8", int64(23)},
		} {
			objectAddressRun(t, p, b, c, fixture.Select(tc.q, []string{"v"}, []string{tc.typ}, [][]any{{tc.want}}))
		}
	})
}

func TestReferenceObjectAddressNulls(t *testing.T) {
	objectAddressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			fn            string
			args          []string
			labels, types []string
		}{
			{"pg_identify_object", []string{"2615::oid", "2200::oid", "0::int4"}, []string{"type", "schema", "name", "identity"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}},
			{"pg_identify_object_as_address", []string{"2615::oid", "2200::oid", "0::int4"}, []string{"type", "object_names", "object_args"}, []string{"TEXT", "_TEXT", "_TEXT"}},
			{"pg_get_object_address", []string{"'schema'::text", "'{public}'::text[]", "'{}'::text[]"}, []string{"classid", "objid", "objsubid"}, []string{"OID", "OID", "INT4"}},
			{"pg_describe_object", []string{"2615::oid", "2200::oid", "0::int4"}, []string{"v"}, []string{"TEXT"}},
		} {
			for slot := range 3 {
				for _, typed := range []bool{false, true} {
					args := append([]string(nil), tc.args...)
					args[slot] = "NULL"
					if typed {
						for i, x := range tc.args[slot] {
							if x == ':' {
								args[slot] += tc.args[slot][i:]
								break
							}
						}
					}
					call := tc.fn + "(" + args[0] + "," + args[1] + "," + args[2] + ")"
					q := "SELECT * FROM " + call
					if tc.fn == "pg_describe_object" {
						q = "SELECT " + call + " AS v"
					}
					objectAddressRun(t, p, b, c, fixture.Select(q, tc.labels, tc.types, [][]any{make([]any, len(tc.labels))}))
					if tc.fn != "pg_describe_object" {
						objectAddressRun(t, p, b, c, fixture.Select("SELECT "+call+" AS v", []string{"v"}, []string{"RECORD"}, [][]any{{nil}}))
					}
				}
			}
			q := fmt.Sprintf("SELECT %s($1,$2,$3) AS v", tc.fn)
			typ := "RECORD"
			if tc.fn == "pg_describe_object" {
				typ = "TEXT"
			}
			objectAddressRun(t, p, b, c, fixture.Select(q, []string{"v"}, []string{typ}, [][]any{{nil}}, nil, nil, nil))
		}
		objectAddressRun(t, p, b, c, fixture.Select("SELECT * FROM pg_identify_object(NULL,2200,0) WITH ORDINALITY", []string{"type", "schema", "name", "identity", "ordinality"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "INT8"}, [][]any{{nil, nil, nil, nil, int64(1)}}))
	})
}
