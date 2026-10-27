package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func relationSizeComponents(t *testing.T,p *fixture.Pair,b fixture.Backend,name string) [3]int64 {
	t.Helper()
	var got [3]int64
	q:="SELECT pg_table_size('"+name+"'),pg_indexes_size('"+name+"'),pg_total_relation_size('"+name+"')"
	if err:=b.DB.QueryRowContext(p.Ctx,q).Scan(&got[0],&got[1],&got[2]);err!=nil {t.Fatal(err)}
	if got[0]<0 || got[1]<0 || got[2]!=got[0]+got[1] {t.Fatal("component identity",got)}
	return got
}

func TestReferenceRelationSizeComponents(t *testing.T) {
	dbs:=[]string{"rs_components_db"}
	fixture.DatabaseReferenceCleanup(t,&dbs)
	p:=fixture.NewScenario(t)
	for _,b:=range p.Backends(){t.Run(b.Name,func(t *testing.T){
		relationSizeRun(t,p,b,fixture.Exec("CREATE TABLE rs_components(i INT,s TEXT,b BOOL)",0))
		if !b.PG {relationSizeRun(t,p,b,relationSizeSelect("SELECT pg_relation_size('rs_components') AS v",[][]any{{int64(64)}}))}
		relationSizeRun(t,p,b,fixture.Exec("INSERT INTO rs_components VALUES(1,'x',TRUE),(2,NULL,FALSE)",2))
		relationSizeRun(t,p,b,fixture.Exec("CREATE INDEX rs_components_idx ON rs_components(i)",0))
		before:=relationSizeComponents(t,p,b,"rs_components")
		if !b.PG && before!=([3]int64{201,112,313}){t.Fatal(before)}
		if got:=relationSizeComponents(t,p,b,"rs_components");got!=before {t.Fatal("unchanged nondeterminism",got,before)}
		relationSizeRun(t,p,b,fixture.Select("SELECT pg_relation_size('rs_components')=pg_relation_size('rs_components','main') AS v",[]string{"v"},[]string{"BOOL"},[][]any{{true}}))
		relationSizeRun(t,p,b,fixture.Select("SELECT pg_table_size('rs_components')>=pg_relation_size('rs_components')+pg_relation_size('rs_components','fsm')+pg_relation_size('rs_components','vm')+pg_relation_size('rs_components','init') AS v",[]string{"v"},[]string{"BOOL"},[][]any{{true}}))
		indexes:=[]struct{name,definition string; native int64}{
			{"rs_components_include","(i) INCLUDE(s)",144},
			{"rs_components_partial","(i) WHERE i=1",88},
			{"rs_components_expr","((i+1))",112},
			{"rs_components_two","(i,b)",128},
			{"rs_components_hash","USING hash(i)",112},
			{"rs_components_brin","USING brin(i) WITH(pages_per_range=1)",144},
		}
		var names []string
		names=append(names,"rs_components_idx")
		for _,idx:=range indexes {
			relationSizeRun(t,p,b,fixture.Exec("CREATE INDEX "+idx.name+" ON rs_components "+idx.definition,0))
			names=append(names,idx.name)
			if !b.PG {relationSizeRun(t,p,b,relationSizeSelect("SELECT pg_table_size('"+idx.name+"') AS v",[][]any{{idx.native}}))}
			terms:=make([]string,len(names));for i,name:=range names {terms[i]="pg_table_size('"+name+"')"}
			relationSizeRun(t,p,b,fixture.Select("SELECT pg_indexes_size('rs_components')="+strings.Join(terms,"+")+" AS v",[]string{"v"},[]string{"BOOL"},[][]any{{true}}))
		}
		old:=relationSizeComponents(t,p,b,"rs_components")
		var dropped int64
		if err:=b.DB.QueryRowContext(p.Ctx,"SELECT pg_table_size('rs_components_two')").Scan(&dropped);err!=nil{t.Fatal(err)}
		relationSizeRun(t,p,b,fixture.Exec("DROP INDEX rs_components_two",0))
		if got:=relationSizeComponents(t,p,b,"rs_components");got[1]!=old[1]-dropped{t.Fatal("drop component",got,old,dropped)}
		q:="SELECT pg_total_relation_size('rs_components')"
		stmt,err:=b.DB.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close()
		var heldBefore int64
		if err:=stmt.QueryRowContext(p.Ctx).Scan(&heldBefore);err!=nil{t.Fatal(err)}
		var values strings.Builder
		for i:=3;i<4099;i++ {if i>3{values.WriteByte(',')};fmt.Fprintf(&values,"(%d,'payload',TRUE)",i)}
		relationSizeRun(t,p,b,fixture.Exec("INSERT INTO rs_components VALUES"+values.String(),4096))
		var heldAfter int64
		if err:=stmt.QueryRowContext(p.Ctx).Scan(&heldAfter);err!=nil||heldAfter<=heldBefore{t.Fatal("volatile prepared growth",heldBefore,heldAfter,err)}
		relationSizeComponents(t,p,b,"rs_components")
		relationSizeRun(t,p,b,fixture.Exec("CREATE DATABASE rs_components_db",0))
		target,_,record:=fixture.DatabaseScenarioPool(t,p,b.PG,"rs_components_db","")
		fixture.RunStep(t,p.Ctx,target,record,fixture.Exec("CREATE TABLE rs_target(i INT)",0))
		var attachedBefore,targetBefore,spaceBefore int64
		if err:=b.DB.QueryRowContext(p.Ctx,"SELECT pg_database_size(current_database()),pg_database_size('rs_components_db'),pg_tablespace_size('pg_default')").Scan(&attachedBefore,&targetBefore,&spaceBefore);err!=nil{t.Fatal(err)}
		fixture.RunStep(t,p.Ctx,target,record,fixture.Exec("INSERT INTO rs_target VALUES(1),(2)",2))
		var attachedAfter,targetAfter,spaceAfter int64
		if err:=b.DB.QueryRowContext(p.Ctx,"SELECT pg_database_size(current_database()),pg_database_size('rs_components_db'),pg_tablespace_size('pg_default')").Scan(&attachedAfter,&targetAfter,&spaceAfter);err!=nil{t.Fatal(err)}
		if attachedAfter<0 || targetAfter<0 || spaceAfter<0 {t.Fatal(attachedAfter,targetAfter,spaceAfter)}
		if !b.PG && (attachedAfter!=attachedBefore || targetAfter-targetBefore!=89 || spaceAfter-spaceBefore!=89) {t.Fatal("registered runtime aggregation",attachedBefore,attachedAfter,targetBefore,targetAfter,spaceBefore,spaceAfter)}
	})}
}
