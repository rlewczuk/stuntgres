package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceOrderedSetAggregateCarrierArrays(t *testing.T) {
	orderedSetScenario(t,func(t *testing.T,p *fixture.Pair,c *sql.Conn,b fixture.Backend) {
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE TYPE os_array_enum AS ENUM('z','a')",0))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE DOMAIN os_array_domain AS INT",0))
		for _,tc:=range []struct{typ,input,cell,metadata,raw string} {
			{"boolean","true","t","_BOOL","_BOOL"},
			{"character","a","a","_BPCHAR","_BPCHAR"},
			{"bytea","\\x00ff","\"\\\\x00ff\"","_BYTEA","_BYTEA"},
			{"date","2020-01-01","2020-01-01","_DATE","_DATE"},
			{"real","1","1","_FLOAT4","_FLOAT4"},
			{"double precision","1","1","_FLOAT8","_FLOAT8"},
			{"smallint","1","1","_INT2","_INT2"},
			{"integer","1","1","_INT4","_INT4"},
			{"bigint","1","1","_INT8","_INT8"},
			{"interval","1 day","\"1 day\"","_INTERVAL","_INTERVAL"},
			{"numeric","1","1","_NUMERIC","_NUMERIC"},
			{"oid","1","1","_OID","_OID"},
			{"regclass","pg_class","pg_class","_REGCLASS","_REGCLASS"},
			{"name","a","a","_NAME","_NAME"},
			{"regtype","integer","integer","_REGTYPE","_REGTYPE"},
			{"regrole","1","1","_REGROLE","_REGROLE"},
			{"regproc","1","1","_REGPROC","_REGPROC"},
			{"regprocedure","1","1","_REGPROCEDURE","_REGPROCEDURE"},
			{"regcollation",`pg_catalog."C"`,`"\"C\""`, "_REGCOLLATION","_REGCOLLATION"},
			{"regnamespace","pg_catalog","pg_catalog","_REGNAMESPACE","_REGNAMESPACE"},
			{"text","a","a","_TEXT","_TEXT"},
			{"character varying","a","a","_VARCHAR","_VARCHAR"},
			{"time without time zone","01:02:03","01:02:03","_TIME","_TIME"},
			{"timestamp without time zone","2020-01-01 01:02:03","\"2020-01-01 01:02:03\"","_TIMESTAMP","_TIMESTAMP"},
			{"timestamp with time zone","2020-01-01 01:02:03+00","\"2020-01-01 01:02:03+00\"","_TIMESTAMPTZ","_TIMESTAMPTZ"},
			{"time with time zone","01:02:03+00","01:02:03+00","_TIMETZ","_TIMETZ"},
			{"uuid","00000000-0000-0000-0000-000000000001","00000000-0000-0000-0000-000000000001","_UUID","_UUID"},
			{"jsonb","{}","\"{}\"","_JSONB","_JSONB"},
			{"os_array_enum","z","z","_OS_ARRAY_ENUM","_TEXT"},
			{"os_array_domain","1","1","_OS_ARRAY_DOMAIN","_INT4"},
		} {
			t.Run(tc.typ,func(t *testing.T){
				q:="SELECT percentile_disc('{1,NULL,0,.5}'::float8[]) WITHIN GROUP(ORDER BY '"+tc.input+"'::"+tc.typ+") AS v"
				metadata:=tc.metadata
				if b.PG&&(tc.typ=="os_array_enum"||tc.typ=="os_array_domain"||tc.typ=="regcollation") {metadata=""}
				want:="{"+tc.cell+",NULL,"+tc.cell+","+tc.cell+"}"
				s:=fixture.ArrayReferenceSelect(q,[]string{"v"},[]string{metadata},[]string{tc.raw},[][]any{{want}},b.PG)
				// The common fixture intentionally has no generic raw contract
				// for these registry arrays. Its per-Step extension checks both
				// actual scans and recorder-owned values without normalization.
				if tc.raw=="_REGTYPE"||tc.raw=="_REGPROC"||tc.raw=="_REGPROCEDURE"||tc.raw=="_REGCOLLATION"||tc.raw=="_REGNAMESPACE" {
					s.CheckRaw=func(t *testing.T,column,row int,value any) bool {
						t.Helper()
						if b.PG {
							bytes,ok:=value.([]byte)
							if !ok||bytes==nil||string(bytes)!=want {t.Fatalf("%s PG raw got %T %#v want nonnil []byte(%q)",tc.raw,value,value,want)}
						} else {
							text,ok:=value.(string)
							if !ok||text!=want {t.Fatalf("%s native raw got %T %#v want string %q",tc.raw,value,value,want)}
						}
						return true
					}
				}
				orderedSetBoth(t,p,c,b,s)
			})
		}
	})
}
