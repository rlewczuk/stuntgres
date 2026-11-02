package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDatabaseTemplatePreparedTransactions(t *testing.T) {
	databaseCreateScenario(t,func(c *databaseCreateCase){
		c.create("dt_prepared_source","",false)
		setup,_,sr:=c.pool("dt_prepared_source","")
		templateRun(c,setup,sr,fixture.Exec("CREATE TABLE dt_prepared_data(v integer)",0),fixture.Exec("INSERT INTO dt_prepared_data VALUES(7)",1))
		if err:=setup.Close();err!=nil {c.t.Fatal(err)}
		cleanup,_,_:=c.pool("dt_prepared_source","")
		b:=c.backend;b.DB=cleanup
		for i:=1;i<=2;i++ {
			gid:=fmt.Sprintf("dt_source_gid_%d",i)
			fixture.PreparedReferenceCleanup(c.t,b,gid)
			db,_,rec:=c.pool("dt_prepared_source","")
			conn,err:=db.Conn(c.p.Ctx);if err!=nil {c.t.Fatal(err)}
			for _,step:=range []fixture.Step{fixture.Exec("BEGIN",0),fixture.Exec("INSERT INTO dt_prepared_data VALUES($1)",1,int64(i+10)),fixture.Exec("PREPARE TRANSACTION '"+gid+"'",0)} {fixture.RunStep(c.t,c.p.Ctx,conn,rec,step)}
			if err:=conn.Close();err!=nil {c.t.Fatal(err)};if err:=db.Close();err!=nil {c.t.Fatal(err)}
			detail:="There is 1 prepared transaction using the database.";if i==2 {detail="There are 2 prepared transactions using the database."}
			templateBusy(c,fmt.Sprintf("dt_prepared_block_%d",i),"dt_prepared_source",detail,i==2)
			if i==1 {
				live,_,_:=c.pool("dt_prepared_source","");if err:=live.PingContext(c.p.Ctx);err!=nil {c.t.Fatal(err)}
				templateBusy(c,"dt_prepared_mixed","dt_prepared_source","There are 1 other session(s) and 1 prepared transaction(s) using the database.",false)
				if err:=live.Close();err!=nil {c.t.Fatal(err)}
			}
		}
		finish,_,fr:=c.pool("dt_prepared_source","")
		templateRun(c,finish,fr,fixture.Exec("ROLLBACK PREPARED 'dt_source_gid_1'",0),fixture.Exec("ROLLBACK PREPARED 'dt_source_gid_2'",0))
		if err:=finish.Close();err!=nil {c.t.Fatal(err)}
		c.create("dt_prepared_block_1","TEMPLATE dt_prepared_source",true)
		clone,_,cr:=c.pool("dt_prepared_block_1","")
		templateRun(c,clone,cr,fixture.Select("SELECT v FROM dt_prepared_data ORDER BY v",[]string{"v"},[]string{"INT4"},[][]any{{int64(7)}}))
	})
}
