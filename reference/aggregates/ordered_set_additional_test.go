package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceOrderedSetAggregateDiagnosticRoutes(t *testing.T) {
	orderedSetScenario(t,func(t *testing.T,p *fixture.Pair,c *sql.Conn,b fixture.Backend) {
		orderedSetSetup(t,p,c,b)
		const hint="No function matches the given name and argument types. You might need to add explicit type casts."
		for _,tc:=range []struct{q,message,token string} {
			{"SELECT \"PERCENTILE_CONT\"(.5) WITHIN GROUP(ORDER BY x) FROM os_input","function PERCENTILE_CONT(numeric, integer) does not exist","\"PERCENTILE_CONT\""},
			{"SELECT percentile_cont(.5,.75) WITHIN GROUP(ORDER BY x) FROM os_input","function percentile_cont(numeric, numeric, integer) does not exist","percentile_cont"},
			{"SELECT percentile_cont(.5) WITHIN GROUP(ORDER BY x,p) FROM os_input","function percentile_cont(numeric, integer, double precision) does not exist","percentile_cont"},
			{"SELECT percentile_cont(.5) WITHIN GROUP(ORDER BY 'a'::text)","function percentile_cont(numeric, text) does not exist","percentile_cont"},
		} {orderedSetBoth(t,p,c,b,orderedSetDiagnostic(tc.q,"42883",tc.message,tc.token,hint))}
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE TABLE os_batch(x INT)",0))
		q:="/*é😀*/ INSERT INTO os_batch VALUES(99); SELECT percentile_cont(.5)"
		s:=orderedSetDiagnostic(q,"42883","function percentile_cont(numeric) does not exist","percentile_cont",hint)
		s.SelectRows=false;s.ErrorStage="execute"
		fixture.RunStep(t,p.Ctx,c,b.Recorder,s)
		fixture.RunStep(t,p.Ctx,c,b.Recorder,orderedSetValue("SELECT count(*) AS v FROM os_batch","INT8",int64(0)))
		s=fixture.ImplicitError(q,fixture.Diagnostic{Code:"42601",Message:"cannot insert multiple commands into a prepared statement"});s.Prepared=true;s.ErrorStage="prepare"
		fixture.RunStep(t,p.Ctx,c,b.Recorder,s)
		fixture.RunStep(t,p.Ctx,c,b.Recorder,orderedSetValue("SELECT count(*) AS v FROM os_batch","INT8",int64(0)))
	})
}

func TestReferenceOrderedSetAggregateCustomMetadata(t *testing.T) {
	orderedSetScenario(t,func(t *testing.T,p *fixture.Pair,c *sql.Conn,b fixture.Backend) {
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE TYPE os_meta_enum AS ENUM('z','a')",0))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE DOMAIN os_meta_domain AS INT",0))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE TABLE os_meta_input(g INT,e os_meta_enum,d os_meta_domain)",0))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO os_meta_input VALUES(0,'z',1),(0,'a',3),(1,NULL,NULL)",3))
		q:="SELECT percentile_disc(.5) WITHIN GROUP(ORDER BY e) AS e,mode() WITHIN GROUP(ORDER BY e) AS m,percentile_disc('{.5}'::float8[]) WITHIN GROUP(ORDER BY e) AS a,percentile_disc(.5) WITHIN GROUP(ORDER BY d) AS d,mode() WITHIN GROUP(ORDER BY d) AS dm,percentile_disc('{.5}'::float8[]) WITHIN GROUP(ORDER BY d) AS da FROM os_meta_input"
		names:=[]string{"e","m","a","d","dm","da"}
		types:=[]string{"","","_OS_META_ENUM","INT4","INT4","_OS_META_DOMAIN"}
		if b.PG {types[2]="";types[5]=""}
		metadata:=func(t *testing.T,rows *sql.Rows){
			columns,err:=rows.ColumnTypes();if err!=nil||len(columns)!=len(names) {t.Fatal(columns,err)}
			for i,column:=range columns {
				want:=reflect.TypeOf("");if i==3||i==4 {want=reflect.TypeOf(int64(0))}
				if b.PG {want=reflect.TypeOf((*any)(nil)).Elem();if i==3||i==4 {want=reflect.TypeOf(int32(0))}}
				if column.Name()!=names[i]||column.DatabaseTypeName()!=types[i]||column.ScanType()!=want {t.Fatalf("custom metadata %d name=%q type=%q scan=%v",i,column.Name(),column.DatabaseTypeName(),column.ScanType())}
				length,lengthKnown:=column.Length();if length!=0||lengthKnown {t.Fatal("length",length,lengthKnown)}
				precision,scale,decimalKnown:=column.DecimalSize();if precision!=0||scale!=0||decimalKnown {t.Fatal("precision",precision,scale,decimalKnown)}
				nullable,known:=column.Nullable();wantNullable:=!b.PG
				if nullable!=wantNullable||known!=wantNullable {t.Fatal("nullable",nullable,known,wantNullable)}
			}
		}
		for _,tc:=range []struct{name,tail string;rows [][]any}{
			{"populated","",[][]any{{"z","z","{z}",int64(1),int64(1),"{1}"}}},
			{"all_null"," WHERE g=1",[][]any{{nil,nil,nil,nil,nil,nil}}},
			{"global_empty"," WHERE FALSE",[][]any{{nil,nil,nil,nil,nil,nil}}},
			{"keyed_empty"," WHERE FALSE GROUP BY g",[][]any{}},
		} {
			t.Run(tc.name,func(t *testing.T){
				s:=fixture.Select(q+tc.tail,names,types,tc.rows)
				s.RawTypes=[]string{"TEXT","TEXT","_TEXT","INT4","INT4","_INT4"}
				s.CheckMetadata=metadata
				for _,prepared:=range []bool{false,true} {
					s.Prepared=prepared
					fixture.EnumReferenceRunStep(t,p.Ctx,c,b.Recorder,s)
				}
			})
		}
	})
}
