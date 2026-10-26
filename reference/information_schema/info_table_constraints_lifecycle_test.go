package reference_test

import (
 "bytes"
 "context"
 "database/sql"
 "errors"
 "testing"

 "stuntgres"
 "stuntgres/reference/fixture"
)

func TestReferenceInfoTableConstraintsLifecycle(t *testing.T) {
 tcScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *fixture.Recorder,database string) {
  run:=func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,r,s) }
  tcSeed(run)
  where:="table_schema IN ('tc_scope','tc_moved')"
  held:=dcHold(t,p,c,tcProjection(b.PG,where,nil).Query)
  want:=tcSeedRows(database)
  check:=func() { tcRoutes(t,p,c,r,held,tcProjection(b.PG,where,want)) }
  check()
  var retained *sql.Rows
  var columns []*sql.ColumnType
  if !b.PG {
   var err error
   retained,err=b.DB.QueryContext(p.Ctx,held.SQL);if err!=nil { t.Fatal(err) };defer retained.Close()
   columns,err=retained.ColumnTypes();if err!=nil { t.Fatal(err) };tcMetadata(t,columns,false)
  }
  run(fixture.Exec("BEGIN",0))
  run(fixture.Exec("ALTER TABLE tc_scope.b RENAME CONSTRAINT same TO transient",0))
  want=tcSeedRows(database);want[7]=tcRow(database,"tc_scope","b","transient","CHECK",nil);check()
  run(fixture.Exec("ROLLBACK",0));want=tcSeedRows(database);check()
  run(fixture.Exec("ALTER TABLE tc_scope.b RENAME CONSTRAINT same TO renamed_check",0))
  want[7]=tcRow(database,"tc_scope","b","renamed_check","CHECK",nil);check()
  run(fixture.Exec("ALTER TABLE tc_scope.b RENAME TO renamed",0))
  want[7]=tcRow(database,"tc_scope","renamed","renamed_check","CHECK",nil);check()
  run(fixture.Exec("CREATE SCHEMA tc_moved",0))
  run(fixture.Exec("ALTER TABLE tc_scope.renamed SET SCHEMA tc_moved",0))
  want[7]=tcRow(database,"tc_moved","renamed","renamed_check","CHECK",nil);check()
  run(fixture.Exec("CREATE ROLE tc_reader",0))
  run(fixture.Exec("ALTER TABLE tc_moved.renamed OWNER TO tc_reader",0))
  check()
  run(fixture.Exec("SET ROLE tc_reader",0))
  tcRoutes(t,p,c,r,held,tcProjection(b.PG,where,[][]any{want[7]}))
  run(fixture.Exec("RESET ROLE",0))
  run(fixture.Exec("ALTER TABLE tc_moved.renamed DROP CONSTRAINT renamed_check",0))
  want=want[:7];check()
  if retained!=nil { tcDrain(t,retained,tcSeedRows(database));tcMetadata(t,columns,false) }
 })
}

func TestReferenceInfoTableConstraintsNativeControls(t *testing.T) {
 tcScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *fixture.Recorder,database string) {
  run:=func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,r,s) }
  tcSeed(run)
  s:=tcProjection(b.PG,"table_schema='tc_scope'",tcSeedRows(database))
  tcRoutes(t,p,c,r,dcHold(t,p,c,s.Query),s)
  tcIdentity(t,p,b,c,r)
  if b.PG { return } // Process-local controls have no PostgreSQL counterpart.
  var dump bytes.Buffer
  if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil { t.Fatal(err) }
  retained,err:=b.DB.QueryContext(p.Ctx,s.Query);if err!=nil { t.Fatal(err) };defer retained.Close()
  columns,err:=retained.ColumnTypes();if err!=nil { t.Fatal(err) }
  if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil { t.Fatal(err) }
  run(tcProjection(false,"table_schema='tc_scope'",nil))
  tcIdentity(t,p,b,c,r)
  if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes()));err!=nil { t.Fatal(err) }
  run(s);run(fixture.Prepared(s));tcIdentity(t,p,b,c,r)
  tcDrain(t,retained,tcSeedRows(database));tcMetadata(t,columns,false)
  target:=fixture.NewFixture(t,false,true)
  if err:=stuntgres.Restore(target.Ctx,target.Native,bytes.NewReader(dump.Bytes()));err!=nil { t.Fatal(err) }
  var targetDatabase string
  if err:=target.Memory.QueryRowContext(target.Ctx,"SELECT current_database()").Scan(&targetDatabase);err!=nil { t.Fatal(err) }
  targetStep:=tcProjection(false,"table_schema='tc_scope'",tcSeedRows(targetDatabase))
  fixture.RunStep(t,target.Ctx,target.Memory,target.MemoryRecord,targetStep)
  fixture.RunStep(t,target.Ctx,target.Memory,target.MemoryRecord,fixture.Prepared(targetStep))
  tcIdentity(t,target,target.Backends()[0],target.Memory,target.MemoryRecord)
  canceled,cancel:=context.WithCancel(p.Ctx);cancel()
  held:=dcHold(t,p,c,s.Query)
  for _,x:=range []fixture.Executor{c,held} {
   rows,err:=x.QueryContext(canceled,s.Query)
   if rows!=nil { rows.Close();t.Fatal("canceled read published rows") }
   if !errors.Is(err,context.Canceled) { t.Fatal("canceled read",err) }
   var state interface{ SQLState() string }
   if errors.As(err,&state) { t.Fatal("ordinary cancellation exposed SQLSTATE",state.SQLState()) }
  }
  tcRoutes(t,p,c,r,held,s)
 })
}
