package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceOrderedSetAggregatePlainRoutes(t *testing.T) {
	p:=fixture.NewScenario(t)
	for _,b:=range p.Backends() {
		t.Run(b.Name,func(t *testing.T){
			fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("CREATE TABLE os_plain_input(x INT)",0))
			fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("INSERT INTO os_plain_input VALUES(40),(10),(NULL),(20),(30)",5))
			fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("CREATE TABLE os_plain_result(v FLOAT8)",0))
			for _,transaction:=range []bool{false,true} {
				for _,plain:=range []bool{true,false} {
					name:="DB";if transaction {name="Tx"};if plain {name+="/plain"} else {name+="/context"}
					t.Run(name,func(t *testing.T){
						var source fixture.Executor=b.DB
						var tx *sql.Tx
						if transaction {
							var err error
							if plain {tx,err=b.DB.Begin()} else {tx,err=b.DB.BeginTx(p.Ctx,nil)}
							if err!=nil {t.Fatal(err)}
							defer tx.Rollback()
							source=tx
						}
						run:=func(s fixture.Step){s.NonContext=plain;fixture.RunStep(t,p.Ctx,source,b.Recorder,s)}
						// Every failed Tx operation has its own real savepoint, so a
						// PostgreSQL abort cannot contaminate the next route witness.
						failure:=func(executor fixture.Executor,s fixture.Step){
							if tx!=nil {run(fixture.Exec("SAVEPOINT os_plain_failure",0))}
							s.NonContext=plain
							fixture.RunStep(t,p.Ctx,executor,b.Recorder,s)
							if tx!=nil {run(fixture.Exec("ROLLBACK TO os_plain_failure",0));run(fixture.Exec("RELEASE os_plain_failure",0))}
						}
						for _,prepared:=range []bool{false,true} {
							s:=fixture.Select("SELECT percentile_cont(.25) WITHIN GROUP(ORDER BY x) AS c,percentile_disc(.25) WITHIN GROUP(ORDER BY x) AS d,mode() WITHIN GROUP(ORDER BY x) AS m FROM os_plain_input",[]string{"c","d","m"},[]string{"FLOAT8","INT4","INT4"},[][]any{{float64(17.5),int64(10),int64(10)}})
							s.Prepared=prepared;run(s)
							s=fixture.ArrayReferenceSelect("SELECT percentile_cont('{.25,NULL,.75}'::float8[]) WITHIN GROUP(ORDER BY x) AS v FROM os_plain_input",[]string{"v"},[]string{"_FLOAT8"},[]string{"_FLOAT8"},[][]any{{"{17.5,NULL,32.5}"}},b.PG)
							s.Prepared=prepared;run(s)
							s=fixture.ArrayReferenceSelect("SELECT percentile_cont('[-1:1]={.25,NULL,.75}'::float8[]) WITHIN GROUP(ORDER BY x) AS v FROM os_plain_input",[]string{"v"},[]string{"_FLOAT8"},[]string{"_FLOAT8"},[][]any{{"[-1:1]={17.5,NULL,32.5}"}},b.PG)
							s.Prepared=prepared;run(s)
							s=fixture.Select("SELECT percentile_cont(.5) WITHIN GROUP(ORDER BY x) AS c,percentile_disc('{.5}'::float8[]) WITHIN GROUP(ORDER BY x) AS a FROM os_plain_input WHERE FALSE GROUP BY x",[]string{"c","a"},[]string{"FLOAT8","_INT4"},[][]any{})
							s.Prepared=prepared
							s.CheckMetadata=func(t *testing.T,rows *sql.Rows){
								types,err:=rows.ColumnTypes();if err!=nil||len(types)!=2 {t.Fatal(types,err)}
								for i,column:=range types {
									want:=reflect.TypeOf(float64(0));if i==1 {want=reflect.TypeOf("");if b.PG {want=reflect.TypeOf((*any)(nil)).Elem()}}
									if column.ScanType()!=want {t.Fatal("typed empty scan authority",i,column.ScanType(),want)}
									if _,ok:=column.Length();ok {t.Fatal("unexpected length")};if _,_,ok:=column.DecimalSize();ok {t.Fatal("unexpected precision")}
									nullable,known:=column.Nullable();if b.PG&&known||!b.PG&&(!known||!nullable) {t.Fatal("empty nullable metadata",nullable,known)}
								}
							}
							run(s)
							global:=s;global.Query=strings.TrimSuffix(s.Query," GROUP BY x");global.Rows=[][]any{{nil,nil}};run(global)
							s=fixture.Exec("INSERT INTO os_plain_result SELECT percentile_cont(.5) WITHIN GROUP(ORDER BY x) FROM os_plain_input",1);s.Prepared=prepared;run(s)
							run(orderedSetValue("SELECT v AS v FROM os_plain_result","FLOAT8",float64(25)))
							run(fixture.Exec("DELETE FROM os_plain_result",1))
							s=fixture.Exec("INSERT INTO os_plain_result SELECT percentile_cont(.5) WITHIN GROUP(ORDER BY x) FROM os_plain_input WHERE FALSE",1);s.Prepared=prepared;run(s)
							run(orderedSetValue("SELECT v AS v FROM os_plain_result","FLOAT8",nil))
							run(fixture.Exec("DELETE FROM os_plain_result",1))
							s=fixture.Exec("INSERT INTO os_plain_result SELECT percentile_cont(.5) WITHIN GROUP(ORDER BY x) FROM os_plain_input WHERE FALSE GROUP BY x",0);s.Prepared=prepared;run(s)
							run(orderedSetValue("SELECT count(*) AS v FROM os_plain_result","INT8",int64(0)))
							late:="INSERT INTO os_plain_result SELECT percentile_cont(p) WITHIN GROUP(ORDER BY x) FROM (VALUES(1,.5::float8),(2,1.1::float8)) AS late(x,p) GROUP BY p ORDER BY p"
							s=orderedSetDiagnostic(late,"22003","percentile value 1.1 is not between 0 and 1","","");s.Prepared=prepared;s.SelectRows=false;s.ErrorStage="execute"
							failure(source,s)
							run(orderedSetValue("SELECT count(*) AS v FROM os_plain_result","INT8",int64(0)))
							q:="SELECT percentile_cont(.5)"
							s=orderedSetDiagnostic(q,"42883","function percentile_cont(numeric) does not exist","percentile_cont","No function matches the given name and argument types. You might need to add explicit type casts.")
							s.Prepared=prepared;s.ErrorStage="execute";if prepared {s.ErrorStage="prepare"}
							failure(source,s)
						}
						for _,family:=range []string{"percentile_cont","percentile_disc"} {
							for _,array:=range []bool{false,true} {
								fraction:="$1";typ:="FLOAT8";if family=="percentile_disc" {typ="INT4"};if array {fraction+="::float8[]";typ="_FLOAT8";if family=="percentile_disc" {typ="_INT4"}}
								q:="SELECT "+family+"("+fraction+") WITHIN GROUP(ORDER BY x) AS v FROM os_plain_input"
								var stmt *sql.Stmt;var err error
								if plain {stmt,err=source.(fixture.NonContextExecutor).Prepare(q)} else {stmt,err=source.PrepareContext(p.Ctx,q)}
								if err!=nil {t.Fatal("quiet held prepare",err)}
								defer stmt.Close()
								held:=fixture.HeldStatement{Executor:source,Statement:stmt,SQL:q}
								steps:=[]struct{arg,want any}{{float64(.25),float64(17.5)},{nil,nil},{float64(.75),float64(32.5)}}
								if family=="percentile_disc" {steps=[]struct{arg,want any}{{float64(.25),int64(10)},{nil,nil},{float64(.75),int64(30)}}}
								if array {want:="{17.5,NULL,32.5}";if family=="percentile_disc" {want="{10,NULL,30}"};steps=[]struct{arg,want any}{{"{.25,NULL,.75}",want},{nil,nil}}}
								heldSuccess:=func(arg,want any){
									s:=orderedSetValue(q,typ,want)
									if array {s=fixture.ArrayReferenceSelect(q,[]string{"v"},[]string{typ},[]string{typ},[][]any{{want}},b.PG)}
									s.Args=[]any{arg};s.NonContext=plain
									fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
								}
								for _,step:=range steps {heldSuccess(step.arg,step.want)}
								bad:=any(float64(1.1));if array {bad="{1.1,-.1}"}
								for _,selectRows:=range []bool{true,false} {
									s:=orderedSetDiagnostic(q,"22003","percentile value 1.1 is not between 0 and 1","","");s.Args=[]any{bad};s.SelectRows=selectRows;s.ErrorStage="execute"
									failure(held,s)
								}
								// Recovery uses the exact same already-prepared DB/Tx statement.
								if array {heldSuccess("{0,1}","{10,40}")} else {want:=any(float64(25));if family=="percentile_disc" {want=int64(20)};heldSuccess(float64(.5),want)}
							}
						}
					})
				}
			}
		})
	}
}
