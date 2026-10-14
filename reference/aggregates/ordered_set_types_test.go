package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceOrderedSetAggregateTypes(t *testing.T) {
	orderedSetScenario(t,func(t *testing.T,p *fixture.Pair,c *sql.Conn,b fixture.Backend) {
		metadata:=map[string]string{
			"boolean":"BOOL","character":"BPCHAR","bytea":"BYTEA","date":"DATE","real":"FLOAT4","double precision":"FLOAT8","smallint":"INT2","integer":"INT4","bigint":"INT8","money":"MONEY","interval":"INTERVAL","name":"NAME","numeric":"NUMERIC",
			"oid":"OID","regclass":"REGCLASS","regtype":"REGTYPE","regproc":"REGPROC","regprocedure":"REGPROCEDURE","regrole":"REGROLE","regoper":"REGOPER","regoperator":"REGOPERATOR","text":"TEXT","character varying":"VARCHAR",
			"regcollation":"REGCOLLATION","regnamespace":"REGNAMESPACE","regconfig":"REGCONFIG","regdictionary":"REGDICTIONARY",
			"time without time zone":"TIME","timestamp without time zone":"TIMESTAMP","timestamp with time zone":"TIMESTAMPTZ","time with time zone":"TIMETZ","uuid":"UUID","jsonb":"JSONB","inet":"INET","cidr":"CIDR","xid8":"XID8",
		}
		for _, tc:=range []struct{typ,value string} {
			{"boolean","true"},{"character","a"},{"bytea","\\x00ff"},{"date","2020-01-01"},
			{"real","1"},{"double precision","1"},{"smallint","1"},{"integer","1"},{"bigint","1"},{"money","1"},{"interval","1 day"},{"name","a"},{"numeric","1"},
			{"oid","1"},{"regclass","pg_class"},{"regtype","integer"},{"regproc","1"},{"regprocedure","1"},{"regrole","1"},{"regoper","1"},{"regoperator","1"},
			{"regcollation",`pg_catalog."C"`},{"regnamespace","pg_catalog"},{"regconfig","-"},{"regdictionary","-"},
			{"text","a"},{"character varying","a"},{"time without time zone","01:02:03"},{"timestamp without time zone","2020-01-01 01:02:03"},{"timestamp with time zone","2020-01-01 01:02:03+00"},{"time with time zone","01:02:03+00"},
			{"uuid","00000000-0000-0000-0000-000000000001"},{"jsonb","{}"},{"inet","10.0.0.1"},{"cidr","10.0.0.0/24"},{"xid8","1"},
		} {
			t.Run(tc.typ,func(t *testing.T){
				for _, family:=range []string{"percentile_disc(.5)","mode()"} {
					call:=family+" WITHIN GROUP(ORDER BY '"+tc.value+"'::"+tc.typ+")"
					q:="SELECT ("+call+") IS NOT DISTINCT FROM '"+tc.value+"'::"+tc.typ+" AS v"
					orderedSetBoth(t,p,c,b,orderedSetValue(q,"BOOL",true))
					// The actual aggregate descriptor remains visible on empty
					// input; no unsupported sibling introspection is needed.
					name:=metadata[tc.typ]
					if b.PG&&(tc.typ=="xid8"||tc.typ=="regcollation") {name=""}
					empty:=orderedSetValue("SELECT "+call+" AS v WHERE FALSE",name,nil)
					if tc.typ=="regcollation"||tc.typ=="regnamespace"||tc.typ=="regconfig"||tc.typ=="regdictionary" {
						want:="-";if tc.typ=="regcollation" {want=`"C"`};if tc.typ=="regnamespace" {want="pg_catalog"}
						aliasMetadata:=func(t *testing.T,rows *sql.Rows){
							columns,err:=rows.ColumnTypes();if err!=nil||len(columns)!=1 {t.Fatal(columns,err)}
							column:=columns[0];scan:=reflect.TypeOf("");if b.PG {scan=reflect.TypeOf((*any)(nil)).Elem()}
							length,lengthKnown:=column.Length();precision,scale,decimalKnown:=column.DecimalSize();nullable,nullableKnown:=column.Nullable()
							if column.Name()!="v"||column.DatabaseTypeName()!=name||column.ScanType()!=scan||length!=0||lengthKnown||precision!=0||scale!=0||decimalKnown||nullable!=!b.PG||nullableKnown!=!b.PG {t.Fatalf("alias metadata %s name=%q type=%q scan=%v length=%d/%t precision=%d,%d/%t nullable=%t/%t",tc.typ,column.Name(),column.DatabaseTypeName(),column.ScanType(),length,lengthKnown,precision,scale,decimalKnown,nullable,nullableKnown)}
						}
						value:=orderedSetValue("SELECT "+call+" AS v",name,want)
						value.RawTypes=[]string{metadata[tc.typ]}
						value.CheckMetadata=aliasMetadata
						value.CheckRaw=func(t *testing.T,column,row int,value any) bool {
							fixture.RegtypeRaw(t,b.PG,value)
							if b.PG {if string(value.([]byte))!=want {t.Fatal("alias raw payload",value,want)}} else if value.(string)!=want {t.Fatal("alias raw payload",value,want)}
							return true
						}
						orderedSetBoth(t,p,c,b,value)
						empty.RawTypes=value.RawTypes;empty.CheckMetadata=aliasMetadata
					}
					orderedSetBoth(t,p,c,b,empty)
				}
			})
		}
		// Reuse the existing admitted pg_class.relkind CHAR expression route,
		// without inventing pg_type or enabling unsupported user CHAR casts.
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE TABLE os_char_type(id INT)",0))
		for _,family:=range []string{"percentile_disc(.5)","mode()"} {
			q:="SELECT "+family+" WITHIN GROUP(ORDER BY relkind) AS v FROM pg_catalog.pg_class WHERE relname='os_char_type'"
			orderedSetBoth(t,p,c,b,orderedSetValue(q,"CHAR","r"))
			orderedSetBoth(t,p,c,b,orderedSetValue("SELECT "+family+" WITHIN GROUP(ORDER BY relkind) AS v FROM pg_catalog.pg_class WHERE FALSE","CHAR",nil))
		}
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE TYPE os_enum AS ENUM('z','a')",0))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE DOMAIN os_domain AS INT",0))
		for _, typ:=range []string{"os_enum","os_domain"} {
			value:="1";if typ=="os_enum" {value="z"}
			q:="SELECT percentile_disc(.5) WITHIN GROUP(ORDER BY '"+value+"'::"+typ+") AS v"
			metadata:="";want:=any("z");raw:="TEXT"
			if typ=="os_domain" {metadata="INT4";want=int64(1);raw="INT4"}
			s:=orderedSetValue(q,metadata,want);s.RawTypes=[]string{raw}
			for _,prepared:=range []bool{false,true} {
				s.Prepared=prepared
				fixture.EnumReferenceRunStep(t,p.Ctx,c,b.Recorder,s)
			}
		}
		orderedSetBoth(t,p,c,b,fixture.Select("SELECT percentile_disc(.5) WITHIN GROUP(ORDER BY 1::real) AS d,percentile_cont(.5) WITHIN GROUP(ORDER BY 1::real) AS c",[]string{"d","c"},[]string{"FLOAT4","FLOAT8"},[][]any{{float64(1),float64(1)}}))
		// Known scalar direct fraction resolves UNKNOWN aggregated input to float8.
		orderedSetBoth(t,p,c,b,orderedSetValue("SELECT percentile_cont(.5) WITHIN GROUP(ORDER BY NULL) AS v","FLOAT8",nil))
		for _, collation:=range []string{"C","POSIX"} {
			orderedSetBoth(t,p,c,b,orderedSetValue("SELECT mode() WITHIN GROUP(ORDER BY v COLLATE \""+collation+"\") AS v FROM (VALUES('b'::text),('a')) AS a(v)","TEXT","a"))
		}
	})
}

func TestReferenceOrderedSetAggregateMetadata(t *testing.T) {
	orderedSetScenario(t,func(t *testing.T,p *fixture.Pair,c *sql.Conn,b fixture.Backend) {
		orderedSetSetup(t,p,c,b)
		q:="SELECT percentile_cont(.5) WITHIN GROUP(ORDER BY x) AS c,percentile_cont(.5) WITHIN GROUP(ORDER BY x*interval '1 day') AS i,percentile_cont('{.5}'::float8[]) WITHIN GROUP(ORDER BY x) AS ca,percentile_cont('{.5}'::float8[]) WITHIN GROUP(ORDER BY x*interval '1 day') AS ia,percentile_disc(.5) WITHIN GROUP(ORDER BY x) AS d,percentile_disc('{.5}'::float8[]) WITHIN GROUP(ORDER BY x) AS da,mode() WITHIN GROUP(ORDER BY x) AS m FROM os_input"
		for _,tc:=range []struct{tail string;rows [][]any}{
			{"",[][]any{{float64(25),"25 days","{25}","{\"25 days\"}",int64(20),"{20}",int64(10)}}},
			{" WHERE FALSE",[][]any{{nil,nil,nil,nil,nil,nil,nil}}},
			{" WHERE FALSE GROUP BY p",[][]any{}},
		} {
			s:=fixture.Select(q+tc.tail,[]string{"c","i","ca","ia","d","da","m"},[]string{"FLOAT8","INTERVAL","_FLOAT8","_INTERVAL","INT4","_INT4","INT4"},tc.rows)
			s.CheckMetadata=func(t *testing.T,rows *sql.Rows){
				types,err:=rows.ColumnTypes();if err!=nil {t.Fatal(err)}
				for i,typ:=range types {
					if typ.Name()!=s.Columns[i]||typ.DatabaseTypeName()!=s.Types[i] {t.Fatalf("descriptor %d %v",i,typ)}
					want:=reflect.TypeOf("")
					if i==0 {want=reflect.TypeOf(float64(0))}
					if i==4||i==6 {want=reflect.TypeOf(int64(0))}
					if b.PG {
						want=reflect.TypeOf((*any)(nil)).Elem()
						if i==0 {want=reflect.TypeOf(float64(0))}
						if i==4||i==6 {want=reflect.TypeOf(int32(0))}
					}
					if typ.ScanType()!=want {t.Fatalf("%s scan type %d %v want %v",b.Name,i,typ.ScanType(),want)}
					if _,ok:=typ.Length();ok {t.Fatal("unexpected length")};if _,_,ok:=typ.DecimalSize();ok {t.Fatal("unexpected decimal size")}
					nullable,known:=typ.Nullable();if b.PG&&known||!b.PG&&(!known||!nullable) {t.Fatalf("nullable %t %t",nullable,known)}
				}
			}
			orderedSetBoth(t,p,c,b,s)
		}
	})
}
