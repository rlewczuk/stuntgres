package reference_test

import (
 "database/sql"
 "database/sql/driver"
 "runtime"
 "fmt"
 "testing"
 "stuntgres/reference/fixture"
)
func TestReferenceReplicationSlotBoundary(t *testing.T) {
 slotCases(t,func(s *fixture.RoleMembershipScenario,db *sql.DB){
  s.Exec("CREATE TABLE rsl_sentinel(v integer)"); s.Run(fixture.Exec("INSERT INTO rsl_sentinel VALUES(7)",1))
  peer,err:=db.Conn(s.P.Ctx); if err!=nil { s.T.Fatal(err) }; defer peer.Close()
  if s.Backend.PG { slotCleanup(s.T,db,"rsl_boundary") }
  for _,logical:=range []bool{false,true} { for _,temporary:=range []bool{false,true} {
   slotCreate(s,"rsl_boundary",logical,false,temporary)
   if s.Backend.PG { slotDrop(s,"rsl_boundary") }
   for _,executor:=range []*sql.Conn{s.C,peer} { d:=logicalDecodingMissing("rsl_boundary"); d.Severity="ERROR"; d.SeverityUnlocalized="ERROR"; step:=fixture.ErrorDiagnostic("SELECT pg_drop_replication_slot('rsl_boundary')",d); step.SelectRows=true; fixture.RunStep(s.T,s.P.Ctx,executor,s.Record,step) }
   for _,fn:=range []string{"pg_copy_physical_replication_slot","pg_copy_logical_replication_slot"} { slotFailure(s,"SELECT * FROM "+fn+"('rsl_boundary','BAD')",logicalDecodingMissing("rsl_boundary")) }
   slotFailure(s,"SELECT * FROM pg_replication_slot_advance('rsl_boundary','0/0')",fixture.Diagnostic{Code:"22023",Message:"invalid target WAL LSN"})
   slotFailure(s,"SELECT * FROM pg_replication_slot_advance('rsl_boundary','0/1')",logicalDecodingMissing("rsl_boundary"))
  } }
  predefinedSelect(s,"SELECT v FROM rsl_sentinel",[]string{"v"},[]string{"INT4"},[][]any{{int64(7)}})
 })
}
func TestReferenceReplicationSlotReferenceLifecycle(t *testing.T) {
 slotCases(t,func(s *fixture.RoleMembershipScenario,db *sql.DB){
  names:=[]string{"rsl_unreserved","rsl_reserved","rsl_phcopy","rsl_logical","rsl_lcopy2","rsl_lcopy3","rsl_lcopy4"}
  if s.Backend.PG { slotCleanup(s.T,db,names...) }
  slotCreate(s,names[0],false,false,false)
  if s.Backend.PG {
   predefinedSelect(s,"SELECT slot_type,restart_lsn::text FROM pg_replication_slots WHERE slot_name='rsl_unreserved'",[]string{"slot_type","restart_lsn"},[]string{"TEXT","TEXT"},[][]any{{"physical",nil}})
   slotFailure(s,"SELECT * FROM pg_copy_physical_replication_slot('rsl_unreserved','rsl_phcopy')",fixture.Diagnostic{Code:"55000",Message:"cannot copy a replication slot that doesn't reserve WAL"})
   slotFailure(s,"SELECT * FROM pg_replication_slot_advance('rsl_unreserved','0/1')",fixture.Diagnostic{Code:"55000",Message:`replication slot "rsl_unreserved" cannot be advanced`,Detail:"This slot has never previously reserved WAL, or it has been invalidated."})
  } else {
   slotFailure(s,"SELECT * FROM pg_copy_physical_replication_slot('rsl_unreserved','rsl_phcopy')",logicalDecodingMissing(names[0]))
   slotFailure(s,"SELECT * FROM pg_replication_slot_advance('rsl_unreserved','0/1')",logicalDecodingMissing(names[0]))
  }
  start:=slotCreate(s,names[1],false,true,false)
  sourceQuery:="SELECT * FROM pg_create_logical_replication_slot('rsl_logical','test_decoding',false,true,true)"
  var logicalStart string
  if s.Backend.PG { var name string; if err:=s.C.QueryRowContext(s.P.Ctx,sourceQuery).Scan(&name,&logicalStart); err!=nil || name!=names[3] || logicalStart=="" { s.T.Fatal(name,logicalStart,err) } } else { slotFailure(s,sourceQuery,slotBoundary(true)) }
  if !s.Backend.PG { for _,sig:=range slotSignatures[3:] { slotFailure(s,"SELECT * FROM "+slotCall(sig,sig.args),logicalDecodingMissing("rsl_absent")) }; return }
  var copied,lsn string
  // Establish the actual source options and positions before checking copy resets.
  predefinedSelect(s,"SELECT slot_type,plugin,temporary,two_phase,failover,confirmed_flush_lsn::text,restart_lsn IS NOT NULL AS reserved FROM pg_replication_slots WHERE slot_name='rsl_logical'",[]string{"slot_type","plugin","temporary","two_phase","failover","confirmed_flush_lsn","reserved"},[]string{"TEXT","NAME","BOOL","BOOL","BOOL","TEXT","BOOL"},[][]any{{"logical","test_decoding",false,true,true,logicalStart,true}})
  // Physical copy returns NULL lsn; its WAL reservation lives in the catalog.
  predefinedSelect(s,"SELECT * FROM pg_copy_physical_replication_slot('rsl_reserved','rsl_phcopy')",[]string{"slot_name","lsn"},[]string{"NAME","PG_LSN"},[][]any{{names[2],nil}})
  predefinedSelect(s,"SELECT slot_type,temporary,restart_lsn::text,restart_lsn=(SELECT restart_lsn FROM pg_replication_slots WHERE slot_name='rsl_reserved') AS same FROM pg_replication_slots WHERE slot_name='rsl_phcopy'",[]string{"slot_type","temporary","restart_lsn","same"},[]string{"TEXT","BOOL","TEXT","BOOL"},[][]any{{"physical",false,start,true}})
  slotCleanup(s.T,db,"rsl_inherit_src","rsl_inherit_copy")
  inherited:=slotCreate(s,"rsl_inherit_src",false,true,true)
  predefinedSelect(s,"SELECT * FROM pg_copy_physical_replication_slot('rsl_inherit_src','rsl_inherit_copy')",[]string{"slot_name","lsn"},[]string{"NAME","PG_LSN"},[][]any{{"rsl_inherit_copy",nil}})
  predefinedSelect(s,"SELECT temporary,restart_lsn::text,restart_lsn=(SELECT restart_lsn FROM pg_replication_slots WHERE slot_name='rsl_inherit_src') AS same FROM pg_replication_slots WHERE slot_name='rsl_inherit_copy'",[]string{"temporary","restart_lsn","same"},[]string{"BOOL","TEXT","BOOL"},[][]any{{true,inherited,true}})
  slotDrop(s,"rsl_inherit_copy")
  predefinedSelect(s,"SELECT * FROM pg_copy_physical_replication_slot('rsl_inherit_src','rsl_inherit_copy',false)",[]string{"slot_name","lsn"},[]string{"NAME","PG_LSN"},[][]any{{"rsl_inherit_copy",nil}})
  predefinedSelect(s,"SELECT temporary,restart_lsn::text,restart_lsn=(SELECT restart_lsn FROM pg_replication_slots WHERE slot_name='rsl_inherit_src') AS same FROM pg_replication_slots WHERE slot_name='rsl_inherit_copy'",[]string{"temporary","restart_lsn","same"},[]string{"BOOL","TEXT","BOOL"},[][]any{{false,inherited,true}})
  slotDrop(s,"rsl_inherit_copy"); slotDrop(s,"rsl_inherit_src")
  slotFailure(s,"SELECT * FROM pg_copy_physical_replication_slot('rsl_logical','rsl_phcopy')",fixture.Diagnostic{Code:"0A000",Message:`cannot copy physical replication slot "rsl_logical" as a logical replication slot`})
  slotFailure(s,"SELECT * FROM pg_copy_logical_replication_slot('rsl_reserved','rsl_lcopy2')",fixture.Diagnostic{Code:"0A000",Message:`cannot copy logical replication slot "rsl_reserved" as a physical replication slot`})
  for i,args:=range []string{"'rsl_logical','rsl_lcopy2'","'rsl_logical','rsl_lcopy3',true","'rsl_logical','rsl_lcopy4',false,'pgoutput'"} {
   if err:=s.C.QueryRowContext(s.P.Ctx,"SELECT * FROM pg_copy_logical_replication_slot("+args+")").Scan(&copied,&lsn); err!=nil || copied!=names[4+i] || lsn!=logicalStart { s.T.Fatal(copied,lsn,err) }
   plugin:="test_decoding"; if i==2 { plugin="pgoutput" }
   predefinedSelect(s,"SELECT slot_type,plugin,temporary,failover,two_phase,confirmed_flush_lsn::text FROM pg_replication_slots WHERE slot_name='"+copied+"'",[]string{"slot_type","plugin","temporary","failover","two_phase","confirmed_flush_lsn"},[]string{"TEXT","NAME","BOOL","BOOL","BOOL","TEXT"},[][]any{{"logical",plugin,i==1,false,false,lsn}})
   predefinedSelect(s,"SELECT confirmed_flush_lsn=(SELECT confirmed_flush_lsn FROM pg_replication_slots WHERE slot_name='rsl_logical') AS same,restart_lsn IS NOT NULL AS reserved,restart_lsn=(SELECT restart_lsn FROM pg_replication_slots WHERE slot_name='rsl_logical') AS same_restart FROM pg_replication_slots WHERE slot_name='"+copied+"'",[]string{"same","reserved","same_restart"},[]string{"BOOL","BOOL","BOOL"},[][]any{{true,true,true}})
  }
  slotCleanup(s.T,db,"rsl_linherit_src","rsl_linherit_copy")
  inherited=slotCreate(s,"rsl_linherit_src",true,false,true)
  if err:=s.C.QueryRowContext(s.P.Ctx,"SELECT * FROM pg_copy_logical_replication_slot('rsl_linherit_src','rsl_linherit_copy')").Scan(&copied,&lsn); err!=nil || lsn!=inherited { s.T.Fatal(copied,lsn,err) }
  predefinedSelect(s,"SELECT temporary,plugin,restart_lsn IS NOT NULL AS reserved,restart_lsn=(SELECT restart_lsn FROM pg_replication_slots WHERE slot_name='rsl_linherit_src') AS same_restart FROM pg_replication_slots WHERE slot_name='rsl_linherit_copy'",[]string{"temporary","plugin","reserved","same_restart"},[]string{"BOOL","NAME","BOOL","BOOL"},[][]any{{true,"test_decoding",true,true}})
  slotDrop(s,"rsl_linherit_copy"); slotDrop(s,"rsl_linherit_src")
  // A beyond-current request must clamp to the real flush position observed
  // between these samples, not to the requested maximum or a fixed allocator LSN.
  var flushBefore,insertBefore,flushAfter,insertAfter string
  if err:=s.C.QueryRowContext(s.P.Ctx,"SELECT pg_current_wal_flush_lsn()::text,pg_current_wal_insert_lsn()::text").Scan(&flushBefore,&insertBefore); err!=nil { s.T.Fatal(err) }
  target:="FFFFFFFF/FFFFFFFF"
  if err:=s.C.QueryRowContext(s.P.Ctx,"SELECT * FROM pg_replication_slot_advance('rsl_reserved',$1::pg_lsn)",target).Scan(&copied,&lsn); err!=nil || copied!="rsl_reserved" { s.T.Fatal(copied,lsn,err) }
  if err:=s.C.QueryRowContext(s.P.Ctx,"SELECT pg_current_wal_flush_lsn()::text,pg_current_wal_insert_lsn()::text").Scan(&flushAfter,&insertAfter); err!=nil { s.T.Fatal(err) }
  s.Run(fixture.Select(
   "SELECT $1::pg_lsn >= $2::pg_lsn AS forward,"+
    "$1::pg_lsn >= $3::pg_lsn AS flush_lower,$1::pg_lsn <= $4::pg_lsn AS flush_upper,"+
    "$3::pg_lsn <= $5::pg_lsn AND $5::pg_lsn <= $6::pg_lsn AND $3::pg_lsn <= $4::pg_lsn AND $4::pg_lsn <= $6::pg_lsn AS observed_bounds,"+
    "$7::pg_lsn > $6::pg_lsn AS beyond_current,$1::pg_lsn < $7::pg_lsn AS clamped",
   []string{"forward","flush_lower","flush_upper","observed_bounds","beyond_current","clamped"},
   []string{"BOOL","BOOL","BOOL","BOOL","BOOL","BOOL"},[][]any{{true,true,true,true,true,true}},
   lsn,start,flushBefore,flushAfter,insertBefore,insertAfter,target))
  predefinedSelect(s,"SELECT restart_lsn::text FROM pg_replication_slots WHERE slot_name='rsl_reserved'",[]string{"restart_lsn"},[]string{"TEXT"},[][]any{{lsn}})
  slotFailure(s,"SELECT * FROM pg_replication_slot_advance('rsl_reserved','0/1')",fixture.Diagnostic{Code:"55000",Message:fmt.Sprintf("cannot advance replication slot to 0/1, minimum is %s",lsn)})
  for _,name:=range names { if name!="rsl_lcopy3" { slotDrop(s,name) }; slotFailure(s,"SELECT pg_drop_replication_slot('"+name+"')",logicalDecodingMissing(name)) }
 })
}
func TestReferenceReplicationSlotTemporaryOwnership(t *testing.T) {
 slotCases(t,func(s *fixture.RoleMembershipScenario,db *sql.DB){
  if s.Backend.PG { slotCleanup(s.T,db,"rsl_temp_owner","rsl_temp_close","rsl_permanent") }
  slotCreate(s,"rsl_temp_owner",false,false,true)
  peer,err:=db.Conn(s.P.Ctx); if err!=nil { s.T.Fatal(err) }; defer peer.Close()
  d:=logicalDecodingMissing("rsl_temp_owner")
  if s.Backend.PG { var pid int64; if err:=s.C.QueryRowContext(s.P.Ctx,"SELECT pg_backend_pid()").Scan(&pid); err!=nil { s.T.Fatal(err) }; d=fixture.Diagnostic{Code:"55006",Message:fmt.Sprintf(`replication slot "rsl_temp_owner" is active for PID %d`,pid)} }
  d.Severity="ERROR"; d.SeverityUnlocalized="ERROR"; step:=fixture.ErrorDiagnostic("SELECT pg_drop_replication_slot('rsl_temp_owner')",d); step.SelectRows=true; fixture.RunStep(s.T,s.P.Ctx,peer,s.Record,step)
  slotFailure(s,"SELECT pg_drop_replication_slot('rsl_deliberate_absent')",logicalDecodingMissing("rsl_deliberate_absent"))
  slotFailure(s,"SELECT pg_drop_replication_slot('rsl_temp_owner')",logicalDecodingMissing("rsl_temp_owner"))
  // Force physical owner termination rather than merely returning a pooled Conn.
  owner,err:=db.Conn(s.P.Ctx); if err!=nil { s.T.Fatal(err) }
  if s.Backend.PG { var a string; var b sql.NullString; if err:=owner.QueryRowContext(s.P.Ctx,"SELECT * FROM pg_create_physical_replication_slot('rsl_temp_close',false,true)").Scan(&a,&b); err!=nil { owner.Close(); s.T.Fatal(err) } }
  if err:=owner.Raw(func(any)error{return driver.ErrBadConn}); err!=nil && err!=driver.ErrBadConn { owner.Close(); s.T.Fatal(err) }; owner.Close()
  if s.Backend.PG { for { var exists bool; if err:=peer.QueryRowContext(s.P.Ctx,"SELECT EXISTS(SELECT 1 FROM pg_replication_slots WHERE slot_name='rsl_temp_close')").Scan(&exists); err!=nil { s.T.Fatal(err) }; if !exists { break }; if err:=s.P.Ctx.Err(); err!=nil { s.T.Fatal("temporary slot survived termination",err) }; runtime.Gosched() } }
  slotFailure(s,"SELECT pg_drop_replication_slot('rsl_temp_close')",logicalDecodingMissing("rsl_temp_close"))
  slotCreate(s,"rsl_permanent",false,false,false)
  s.Exec("ALTER ROLE rsl_repl REPLICATION"); s.Exec("SET ROLE rsl_repl")
  if s.Backend.PG { slotDrop(s,"rsl_permanent") } else { slotFailure(s,"SELECT pg_drop_replication_slot('rsl_permanent')",logicalDecodingMissing("rsl_permanent")) }; s.Exec("RESET ROLE")
 })
}
