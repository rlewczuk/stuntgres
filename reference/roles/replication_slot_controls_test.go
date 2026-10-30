package reference_test

import (
 "bytes"
 "database/sql"
 "strings"
 "strconv"
 "testing"
 "stuntgres"
 "stuntgres/reference/fixture"
)
func TestReferenceReplicationSlotControls(t *testing.T) {
 slotCases(t,func(s *fixture.RoleMembershipScenario,_ *sql.DB){
  s.Exec("CREATE TABLE rsl_control_sentinel(v integer)"); s.Run(fixture.Exec("INSERT INTO rsl_control_sentinel VALUES(7)",1))
  s.Exec("CREATE VIEW rsl_view AS SELECT * FROM pg_create_physical_replication_slot(temporary=>true,slot_name=>NULL) AS r(a,b)")
  s.Exec("CREATE VIEW rsl_advance_view AS SELECT * FROM pg_replication_slot_advance(NULL,'0/1')")
  // Distinct named BOOL/NAME/PG_LSN operands must retain their written
  // mapping, omitted create defaults and exact copy overload during replay.
  recipes:=[]struct{call,typ string}{
   {"pg_create_physical_replication_slot(temporary=>true,slot_name=>NULL)","RECORD"},
   {"pg_create_logical_replication_slot(failover=>false,plugin=>'pgoutput',slot_name=>NULL,twophase=>true)","RECORD"},
   {"pg_drop_replication_slot(NULL)","VOID"},
   {"pg_copy_physical_replication_slot(dst_slot_name=>'rsl_dst',src_slot_name=>NULL)","RECORD"},
   {"pg_copy_physical_replication_slot(temporary=>true,dst_slot_name=>'rsl_dst',src_slot_name=>NULL)","RECORD"},
   {"pg_copy_logical_replication_slot(dst_slot_name=>'rsl_dst',src_slot_name=>NULL)","RECORD"},
   {"pg_copy_logical_replication_slot(temporary=>false,dst_slot_name=>'rsl_dst',src_slot_name=>NULL)","RECORD"},
   {"pg_copy_logical_replication_slot(plugin=>'pgoutput',temporary=>true,dst_slot_name=>'rsl_dst',src_slot_name=>NULL)","RECORD"},
   {"pg_replication_slot_advance(upto_lsn=>'0/1',slot_name=>NULL)","RECORD"},
  }
  for i,recipe:=range recipes {
   query:="SELECT * FROM "+recipe.call+" AS r(a,b)"
   if recipe.typ=="VOID" { query="SELECT "+recipe.call+" IS NULL AS result" }
   s.Exec("CREATE VIEW rsl_recipe_"+strconv.Itoa(i)+" AS "+query)
  }
  check:=func(){
   predefinedSelect(s,"SELECT * FROM rsl_view",[]string{"a","b"},[]string{"NAME","PG_LSN"},[][]any{{nil,nil}})
   predefinedSelect(s,"SELECT * FROM rsl_advance_view",[]string{"slot_name","end_lsn"},[]string{"NAME","PG_LSN"},[][]any{{nil,nil}})
   for i,recipe:=range recipes {
    columns,types,rows:=[]string{"a","b"},[]string{"NAME","PG_LSN"},[][]any{{nil,nil}}
    if recipe.typ=="VOID" { columns,types,rows=[]string{"result"},[]string{"BOOL"},[][]any{{true}} }
    predefinedSelect(s,"SELECT * FROM rsl_recipe_"+strconv.Itoa(i),columns,types,rows)
   }
   predefinedSelect(s,"SELECT v FROM rsl_control_sentinel",[]string{"v"},[]string{"INT4"},[][]any{{int64(7)}})
  }
  check()
  if s.Backend.PG { return }
  var before,after bytes.Buffer
  if err:=stuntgres.Dump(s.P.Ctx,s.P.Native,&before); err!=nil { s.T.Fatal(err) }
  if strings.Contains(before.String(),"CREATE_REPLICATION_SLOT") { s.T.Fatal("fabricated slot directive in dump") }
  if err:=stuntgres.Reset(s.P.Ctx,s.P.Native); err!=nil { s.T.Fatal(err) }
  if err:=stuntgres.Restore(s.P.Ctx,s.P.Native,bytes.NewReader(before.Bytes())); err!=nil { s.T.Fatal(err) }; check()
  if err:=stuntgres.Dump(s.P.Ctx,s.P.Native,&after); err!=nil { s.T.Fatal(err) }; if before.String()!=after.String() { s.T.Fatal("typed view recipe changed on replay",before.String(),after.String()) }
  bad:=before.String()+"\nCREATE VIEW rsl_bad AS SELECT * FROM pg_copy_logical_replication_slot('x','y',plugin=>'pgoutput');\n"
  if err:=stuntgres.Restore(s.P.Ctx,s.P.Native,strings.NewReader(bad)); err==nil { s.T.Fatal("invalid restore succeeded") }; check()
  after.Reset(); if err:=stuntgres.Dump(s.P.Ctx,s.P.Native,&after); err!=nil { s.T.Fatal(err) }; if before.String()!=after.String() { s.T.Fatal("failed restore published state") }
 })
}
func TestReferenceReplicationSlotFeatureSmoke(t *testing.T) {
 slotCases(t,func(s *fixture.RoleMembershipScenario,db *sql.DB){
  if s.Backend.PG { slotCleanup(s.T,db,"rsl_smoke","rsl_smoke_logical") }
  s.Exec("CREATE TABLE rsl_smoke_sentinel(v integer)"); s.Run(fixture.Exec("INSERT INTO rsl_smoke_sentinel VALUES(7)",1))
  q:="SELECT * FROM pg_create_physical_replication_slot($1::name,false,true)"; held:=predefinedHeld(s,q)
  null:=func(){ fixture.RunStep(s.T,s.P.Ctx,held,s.Record,fixture.Select(q,[]string{"slot_name","lsn"},[]string{"NAME","PG_LSN"},[][]any{{nil,nil}},nil)) }; null()
  predefinedSelect(s,"SELECT * FROM pg_replication_slot_advance(NULL,'0/1')",[]string{"slot_name","end_lsn"},[]string{"NAME","PG_LSN"},[][]any{{nil,nil}})
  predefinedSelect(s,"SELECT pg_drop_replication_slot(NULL)",[]string{"pg_drop_replication_slot"},[]string{"VOID"},[][]any{{nil}})
  admin,err:=db.Conn(s.P.Ctx); if err!=nil { s.T.Fatal(err) }; defer admin.Close()
  s.Exec("SET ROLE rsl_actor"); logicalDecodingHeldFailure(s,held,logicalDecodingDenied(),"rsl_smoke")
  if _,err:=admin.ExecContext(s.P.Ctx,"ALTER ROLE rsl_actor REPLICATION"); err!=nil { s.T.Fatal(err) }
  if s.Backend.PG { fixture.RunStep(s.T,s.P.Ctx,held,s.Record,fixture.Select(q,[]string{"slot_name","lsn"},[]string{"NAME","PG_LSN"},[][]any{{"rsl_smoke",nil}},"rsl_smoke")); slotDrop(s,"rsl_smoke") } else { logicalDecodingHeldFailure(s,held,slotBoundary(false),"rsl_smoke") }
  slotFailure(s,"SELECT pg_drop_replication_slot('rsl_smoke')",logicalDecodingMissing("rsl_smoke"))
  peerMissing:=func(name string){ d:=logicalDecodingMissing(name); d.Severity="ERROR"; d.SeverityUnlocalized="ERROR"; step:=fixture.ErrorDiagnostic("SELECT pg_drop_replication_slot('"+name+"')",d); step.SelectRows=true; fixture.RunStep(s.T,s.P.Ctx,admin,s.Record,step) }
  peerMissing("rsl_smoke")
  if _,err:=admin.ExecContext(s.P.Ctx,"ALTER ROLE rsl_actor NOREPLICATION"); err!=nil { s.T.Fatal(err) }; logicalDecodingHeldFailure(s,held,logicalDecodingDenied(),"rsl_smoke"); null(); s.Exec("RESET ROLE")
  slotFailure(s,"SELECT * FROM pg_create_logical_replication_slot('rsl_smoke_logical','test_decoding',true,false,true)",fixture.Diagnostic{Code:"0A000",Message:"cannot enable failover for a temporary replication slot"})
  slotCreate(s,"rsl_smoke_logical",true,false,true); if s.Backend.PG { slotDrop(s,"rsl_smoke_logical") }
  peerMissing("rsl_smoke_logical")
  for _,fn:=range []string{"pg_copy_physical_replication_slot","pg_copy_logical_replication_slot"} { slotFailure(s,"SELECT * FROM "+fn+"('rsl_smoke','BAD')",logicalDecodingMissing("rsl_smoke")) }
  slotFailure(s,"SELECT * FROM pg_replication_slot_advance('rsl_smoke','0/0')",fixture.Diagnostic{Code:"22023",Message:"invalid target WAL LSN"}); slotFailure(s,"SELECT * FROM pg_replication_slot_advance('rsl_smoke','0/1')",logicalDecodingMissing("rsl_smoke"))
  slotFailure(s,"SELECT * FROM pg_create_physical_replication_slot('BAD')",fixture.Diagnostic{Code:"42602",Message:`replication slot name "BAD" contains invalid character`,Hint:"Replication slot names may only contain lower case letters, numbers, and the underscore character."})
  null(); predefinedSelect(s,"SELECT v FROM rsl_smoke_sentinel",[]string{"v"},[]string{"INT4"},[][]any{{int64(7)}})
 })
}
