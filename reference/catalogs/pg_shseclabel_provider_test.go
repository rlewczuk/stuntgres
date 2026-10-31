package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
 "github.com/lib/pq"
)

func shseclabelProviderError(t *testing.T,p *fixture.Pair,b fixture.Backend,e fixture.Executor,q,message string) {
 t.Helper()
 want:=fixture.ControlsReferenceDiagnostic("22023",message)
 for _,prepared:=range []bool{false,true}{s:=fixture.ErrorDiagnostic(q,want);s.Prepared,s.ErrorStage=prepared,"execute";fixture.RunStep(t,p.Ctx,e,b.Recorder,s)}
 stmt,err:=e.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal("provider error belongs to Execute",err)};defer stmt.Close()
 held:=fixture.HeldStatement{Executor:e,Statement:stmt,SQL:q}
 for range 2{s:=fixture.ErrorDiagnostic(q,want);s.ErrorStage="execute";fixture.RunStep(t,p.Ctx,held,b.Recorder,s)}
}
func shseclabelSentinel() fixture.Step{return fixture.Select("SELECT id,v FROM shseclabel_sentinel",[]string{"id","v"},[]string{"INT4","TEXT"},[][]any{{int64(7),"kept"}})}
func shseclabelCreateSentinel(t *testing.T,p *fixture.Pair,b fixture.Backend,e fixture.Executor){
 t.Helper();for _,s:=range []fixture.Step{fixture.Exec("CREATE TABLE shseclabel_sentinel(id integer,v text)",0),fixture.Exec("INSERT INTO shseclabel_sentinel VALUES(7,'kept')",1)}{fixture.RunStep(t,p.Ctx,e,b.Recorder,s)}
}
func shseclabelProviderSmoke(t *testing.T,p *fixture.Pair,b fixture.Backend,admin,actor *sql.Conn){
 t.Helper();shseclabelCreateSentinel(t,p,b,admin)
 for _,e:=range []fixture.Executor{admin,actor}{
  shseclabelProviderError(t,p,b,e,"SECURITY LABEL ON ROLE shseclabel_reader IS 'label'","no security label providers have been loaded")
  shseclabelProviderError(t,p,b,e,`SECURITY LABEL FOR "MiSsInG" ON ROLE shseclabel_reader IS NULL`,`security label provider "MiSsInG" is not loaded`)
 }
 fixture.RunStep(t,p.Ctx,admin,b.Recorder,shseclabelSentinel());fixture.RunStep(t,p.Ctx,admin,b.Recorder,shseclabelCount())
}
func TestReferencePGShseclabelProviderDiagnostics(t *testing.T){
 withShseclabelReader(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,admin,actor *sql.Conn){
  shseclabelCreateSentinel(t,p,b,admin)
  for _,e:=range []fixture.Executor{admin,actor}{
   for _,target:=range []string{"ROLE shseclabel_missing","DATABASE shseclabel_missing","TABLESPACE shseclabel_missing","SUBSCRIPTION shseclabel_missing","ROLE shseclabel_reader"}{
    for _,label:=range []string{"'label'","NULL"}{
     for _,provider:=range []struct{clause,name string;present bool}{
      {"","",false},{" FOR MiSsInG","missing",true},{` FOR "MiSsInG"`,"MiSsInG",true},{" FOR 'MiSsInG'","MiSsInG",true},{" FOR ''","",true},{" FOR 'quo''te'","quo'te",true},{` FOR 'quo"te'`,`quo"te`,true},{` FOR E'back\\slash'`,`back\slash`,true},
     }{
      message:="no security label providers have been loaded";if provider.present{message=`security label provider "`+provider.name+`" is not loaded`}
      shseclabelProviderError(t,p,b,e,"SECURITY LABEL"+provider.clause+" ON "+target+" IS "+label,message)
     }
     fixture.RunStep(t,p.Ctx,admin,b.Recorder,shseclabelSentinel());fixture.RunStep(t,p.Ctx,admin,b.Recorder,shseclabelCount())
    }
   }
   // Existing database spelling is backend-specific; the lookup still cannot
   // precede provider selection, even for a real target.
   rows,err:=e.QueryContext(p.Ctx,"SELECT current_database()");if err!=nil{t.Fatal(err)};var database string
   if !rows.Next(){rows.Close();t.Fatal("missing database")};if err:=rows.Scan(&database);err!=nil{rows.Close();t.Fatal(err)};rows.Close()
   shseclabelProviderError(t,p,b,e,"SECURITY LABEL ON DATABASE "+pq.QuoteIdentifier(database)+" IS NULL","no security label providers have been loaded")
  }
  run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,admin,b.Recorder,s)}
  run(fixture.Exec("BEGIN",0));defer admin.ExecContext(p.Ctx,"ROLLBACK")
  run(fixture.Exec("SAVEPOINT label_failure",0))
  run(fixture.ErrorDiagnostic("SECURITY LABEL ON ROLE shseclabel_missing IS NULL",fixture.ControlsReferenceDiagnostic("22023","no security label providers have been loaded")))
  failed:=fixture.ErrorDiagnostic("SELECT count(*) FROM pg_shseclabel",fixture.ControlsReferenceDiagnostic("25P02","current transaction is aborted, commands ignored until end of transaction block"));failed.SelectRows=true;run(failed)
  run(fixture.Exec("ROLLBACK TO SAVEPOINT label_failure",0));run(shseclabelSentinel());run(shseclabelCount());run(fixture.Exec("COMMIT",0))
  run(fixture.Exec("BEGIN READ ONLY",0))
  run(fixture.ErrorDiagnostic("SECURITY LABEL ON ROLE shseclabel_missing IS NULL",fixture.ControlsReferenceDiagnostic("25006","cannot execute SECURITY LABEL in a read-only transaction")))
  run(fixture.Exec("ROLLBACK",0));run(shseclabelSentinel());run(shseclabelCount())
 })
}
