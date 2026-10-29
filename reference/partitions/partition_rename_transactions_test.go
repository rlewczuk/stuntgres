package reference_test

import (
 "testing"
 "stuntgres/reference/fixture"
)

func partitionRenameTransactions(t *testing.T,p *fixture.Pair,b fixture.Backend){
 c,e:=b.DB.Conn(p.Ctx);if e!=nil{t.Fatal(e)};defer c.Close()
 run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)};partitionRenameSetup(run)
 for _,end:=range []string{"ROLLBACK","COMMIT"}{
  run(fixture.Exec("BEGIN",0));run(fixture.Exec("ALTER TABLE pr_root RENAME k TO first",0));partitionRenameRows(run,"first","payload")
  run(fixture.Exec("SAVEPOINT second",0));run(fixture.Exec("ALTER TABLE pr_root RENAME first TO second",0));partitionRenameRows(run,"second","payload")
  run(fixture.Exec("ROLLBACK TO second",0));partitionRenameRows(run,"first","payload")
  run(fixture.Exec("RELEASE second",0));run(fixture.Exec(end,0))
  key:="k";if end=="COMMIT"{key="first"};partitionRenameRows(run,key,"payload")
 }
 run(fixture.Exec("BEGIN",0));run(fixture.Exec("SAVEPOINT before_rename",0))
 run(fixture.Exec("ALTER TABLE pr_root RENAME first TO transient",0));partitionRenameRows(run,"transient","payload")
 run(fixture.Exec("ROLLBACK TO before_rename",0));partitionRenameRows(run,"first","payload")
 run(referenceRangeError("ALTER TABLE pr_root RENAME first TO payload","42701",`column "payload" of relation "pr_mid" already exists`,fixture.Diagnostic{}))
 run(referenceRangeError("SELECT 1","25P02","current transaction is aborted, commands ignored until end of transaction block",fixture.Diagnostic{}))
 run(fixture.Exec("ROLLBACK TO before_rename",0));partitionRenameRows(run,"first","payload")
 run(fixture.Exec("ALTER TABLE pr_root RENAME first TO renamed",0));run(fixture.Exec("RELEASE before_rename",0));run(fixture.Exec("ROLLBACK",0));partitionRenameRows(run,"first","payload")
 run(fixture.Exec("BEGIN",0));run(fixture.Exec("ALTER TABLE pr_root RENAME first TO renamed",0));run(fixture.Exec("COMMIT",0));partitionRenameRows(run,"renamed","payload")
}
func TestReferencePartitionRenameColumnTransactions(t *testing.T){referenceRangeScenario(t,partitionRenameTransactions)}
