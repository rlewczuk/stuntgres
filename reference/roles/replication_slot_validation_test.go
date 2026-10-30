package reference_test

import (
 "context"
 "time"
 "database/sql"
 "fmt"
 "strings"
 "testing"
 "stuntgres/reference/fixture"
)
func TestReferenceReplicationSlotValidation(t *testing.T) {
 slotCases(t,func(s *fixture.RoleMembershipScenario,db *sql.DB){
  for _,name:=range []string{"","UPPER","bad-name","é",strings.Repeat("a",62)+"é"} {
   // NAME clips the final incomplete UTF8 character, leaving 62 valid bytes.
   if strings.HasPrefix(name,strings.Repeat("a",62)) {
    clipped:=strings.Repeat("a",62)
    if s.Backend.PG { slotCleanup(s.T,db,clipped); var got string; var lsn sql.NullString; if err:=s.C.QueryRowContext(s.P.Ctx,"SELECT * FROM pg_create_physical_replication_slot($1)",name).Scan(&got,&lsn); err!=nil || got!=clipped || lsn.Valid { s.T.Fatal(got,lsn,err) }; slotDrop(s,clipped) } else { slotFailure(s,"SELECT * FROM pg_create_physical_replication_slot('"+name+"')",slotBoundary(false)) }
    continue
   }
   d:=fixture.Diagnostic{Code:"42602",Message:fmt.Sprintf(`replication slot name "%s" contains invalid character`,name),Hint:"Replication slot names may only contain lower case letters, numbers, and the underscore character."}
   if name=="" { d.Message=`replication slot name "" is too short`; d.Hint="" }
   slotFailure(s,"SELECT * FROM pg_create_physical_replication_slot("+fileListLiteral(name)+")",d)
   slotFailure(s,"SELECT * FROM pg_create_logical_replication_slot("+fileListLiteral(name)+",'unlisted',true,false,true)",d)
   slotFailure(s,"SELECT pg_drop_replication_slot("+fileListLiteral(name)+")",logicalDecodingMissing(name))
   slotFailure(s,"SELECT * FROM pg_replication_slot_advance("+fileListLiteral(name)+",'0/1')",logicalDecodingMissing(name))
  }
  for _,sig:=range slotSignatures[3:8] { slotFailure(s,"SELECT * FROM "+slotCall(sig,sig.args),logicalDecodingMissing("rsl_absent")) }
  for _,lsn:=range []string{"0/1","80000000/0","FFFFFFFF/FFFFFFFF"} { slotFailure(s,"SELECT * FROM pg_replication_slot_advance('rsl_absent','"+lsn+"')",logicalDecodingMissing("rsl_absent")) }
  slotFailure(s,"SELECT * FROM pg_replication_slot_advance('rsl_absent','0/0')",fixture.Diagnostic{Code:"22023",Message:"invalid target WAL LSN"})
  for _,length:=range []int{63,64} { name:=strings.Repeat("s",length); clipped:=name[:63]; if s.Backend.PG { slotCleanup(s.T,db,clipped) }; if s.Backend.PG { var got string; var lsn sql.NullString; if err:=s.C.QueryRowContext(s.P.Ctx,"SELECT * FROM pg_create_physical_replication_slot($1)",name).Scan(&got,&lsn); err!=nil || got!=clipped || lsn.Valid { s.T.Fatal(got,lsn,err) }; slotDrop(s,clipped) } else { slotFailure(s,"SELECT * FROM pg_create_physical_replication_slot('"+name+"')",slotBoundary(false)) } }
 })
}
func TestReferenceReplicationSlotPluginArguments(t *testing.T) {
 slotCases(t,func(s *fixture.RoleMembershipScenario,db *sql.DB){
  slotFailure(s,"SELECT * FROM pg_create_logical_replication_slot('rsl_plugin','unlisted',true,false,true)",fixture.Diagnostic{Code:"0A000",Message:"cannot enable failover for a temporary replication slot"})
  for _,plugin:=range []string{"unlisted","PGOUTPUT","bad-name","has space","$libdir/pgoutput",""} { slotFailure(s,"SELECT * FROM pg_create_logical_replication_slot('rsl_plugin',"+fileListLiteral(plugin)+")",fixture.Diagnostic{Code:"42501",Message:fmt.Sprintf(`library "%s" may not be used as an output plugin`,plugin),Hint:`If it is safe for all REPLICATION users to use this library as an output plugin, add it to "output_plugin_libraries" and reload the server configuration.`}) }
  if s.Backend.PG { slotCleanup(s.T,db,"rsl_plugin") }
  slotCreate(s,"rsl_plugin",true,false,true)
  if s.Backend.PG { slotDrop(s,"rsl_plugin") }
  for _,options:=range []string{"false,true,false","false,false,true","true,true,false"} {
   q:="SELECT * FROM pg_create_logical_replication_slot('rsl_plugin','pgoutput',"+options+")"
   if !s.Backend.PG { slotFailure(s,q,slotBoundary(true)) } else { var name,lsn string; if err:=s.C.QueryRowContext(s.P.Ctx,q).Scan(&name,&lsn); err!=nil || name!="rsl_plugin" || lsn=="" { s.T.Fatal(name,lsn,err) }; slotDrop(s,"rsl_plugin") }
  }
  // set_config preserves the raw SplitGUCList spelling, unlike SET's
  // GUC_LIST_QUOTE reconstruction. This setting is superuser-settable.
  s.T.Cleanup(func(){ ctx,cancel:=context.WithTimeout(context.Background(),10*time.Second); defer cancel(); if _,err:=s.C.ExecContext(ctx,"RESET output_plugin_libraries"); err!=nil { s.T.Error(err) } })
  held:=predefinedHeld(s,"SELECT * FROM pg_create_logical_replication_slot('rsl_plugin',$1::name,true)")
  peer,err:=db.Conn(s.P.Ctx); if err!=nil { s.T.Fatal(err) }; defer peer.Close()
  for _,tc:=range []struct{list string; allow bool}{
   {`"has space", "a""b", "has,comma", pgoutput`,true},
   {`pgoutput, odd"quote`,true},
   {"PGOUTPUT",false},{"$libdir/pgoutput",false},{"pgoutput,",false},
   {`pgoutput,"unfinished`,false},{"pgoutput,,other",false},{`pgoutput,"ok"junk`,false},{"",false},
   {"pgoutput, test_decoding",true},
  } {
   var got string
   if err:=s.C.QueryRowContext(s.P.Ctx,"SELECT set_config('output_plugin_libraries',$1,false)",tc.list).Scan(&got); err!=nil || got!=tc.list { s.T.Fatal("raw plugin configuration",got,err) }
   var peerList string; if err:=peer.QueryRowContext(s.P.Ctx,"SELECT current_setting('output_plugin_libraries')").Scan(&peerList); err!=nil || peerList!="pgoutput, test_decoding" { s.T.Fatal("plugin setting escaped its session",peerList,err) }
   if !tc.allow { logicalDecodingHeldFailure(s,held,fixture.Diagnostic{Code:"42501",Message:`library "pgoutput" may not be used as an output plugin`,Hint:`If it is safe for all REPLICATION users to use this library as an output plugin, add it to "output_plugin_libraries" and reload the server configuration.`},"pgoutput") } else if !s.Backend.PG { logicalDecodingHeldFailure(s,held,slotBoundary(true),"pgoutput") } else {
    var name,lsn string; if err:=held.Statement.QueryRowContext(s.P.Ctx,"pgoutput").Scan(&name,&lsn); err!=nil || name!="rsl_plugin" || lsn=="" { s.T.Fatal(name,lsn,err) }; slotDrop(s,"rsl_plugin")
   }
   s.Exec("RESET output_plugin_libraries")
  }
  predefinedSelect(s,"SELECT current_setting('output_plugin_libraries') AS plugins",[]string{"plugins"},[]string{"TEXT"},[][]any{{"pgoutput, test_decoding"}})
 })
}
