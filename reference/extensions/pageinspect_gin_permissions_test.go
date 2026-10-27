package reference_test

import (
 "database/sql"
 "encoding/binary"
 "testing"

 "stuntgres/reference/fixture"
)

func ginAuthority(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
 t.Helper();run:=func(s fixture.Step) {fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
 run(fixture.Exec("CREATE ROLE gin_reader",0));run(fixture.Exec("GRANT USAGE ON SCHEMA gin_part TO gin_reader",0))
 defer func() {if _,err:=c.ExecContext(p.Ctx,"RESET SESSION AUTHORIZATION");err!=nil {t.Error(err)}}()
 for i,name:=range ginMembers {
  st,err:=c.PrepareContext(p.Ctx,ginQuery(i));if err!=nil {t.Fatal(err)}
  held:=fixture.HeldStatement{Executor:c,Statement:st,SQL:ginQuery(i)}
  run(fixture.Exec("SET SESSION AUTHORIZATION gin_reader",0))
  inputs:=[][]byte{ginOrdinary(i),make([]byte,8192)}
  for _,n:=range []int{0,3,8191,8193} {inputs=append(inputs,make([]byte,n))}
  special:=ginOrdinary(i);binary.LittleEndian.PutUint16(special[16:],8183);inputs=append(inputs,special)
  for _,flags:=range []uint16{0,9,0x81,0x87,0xc3,0xffff} {raw:=ginOrdinary(i);binary.LittleEndian.PutUint16(raw[8190:],flags);inputs=append(inputs,raw)}
  for _,raw:=range inputs {
   s:=ginError(i,raw,"42501","must be superuser to use raw page functions","")
   ginBoth(t,p,b,c,s);fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
  }
  null:=ginNullRows(i);if i==2 {null=nil}
  ginBoth(t,p,b,c,ginStep(i,nil,null));fixture.RunStep(t,p.Ctx,held,b.Recorder,ginStep(i,nil,null))
  // The same session and held selection must consult current role authority.
  fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("ALTER ROLE gin_reader SUPERUSER",0))
  fixture.RunStep(t,p.Ctx,held,b.Recorder,ginStep(i,ginOrdinary(i),ginOrdinaryRows(i)))
  fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("ALTER ROLE gin_reader NOSUPERUSER",0))
  fixture.RunStep(t,p.Ctx,held,b.Recorder,ginError(i,ginOrdinary(i),"42501","must be superuser to use raw page functions",""))
  run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
  fixture.RunStep(t,p.Ctx,held,b.Recorder,ginStep(i,ginOrdinary(i),ginOrdinaryRows(i)))
  run(fixture.Exec("REVOKE EXECUTE ON FUNCTION gin_part."+name+"(bytea) FROM PUBLIC",0))
  run(fixture.Exec("SET SESSION AUTHORIZATION gin_reader",0))
  denied:=ginError(i,nil,"42501","permission denied for function "+name,"")
  ginBoth(t,p,b,c,denied);fixture.RunStep(t,p.Ctx,held,b.Recorder,denied)
  run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
  run(fixture.Exec("GRANT EXECUTE ON FUNCTION gin_part."+name+"(bytea) TO PUBLIC",0))
  run(fixture.Exec("SET SESSION AUTHORIZATION gin_reader",0))
  fixture.RunStep(t,p.Ctx,held,b.Recorder,ginStep(i,nil,null))
  fixture.RunStep(t,p.Ctx,held,b.Recorder,ginError(i,ginOrdinary(i),"42501","must be superuser to use raw page functions",""))
  run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
  if err:=st.Close();err!=nil {t.Fatal(err)}
 }
 run(fixture.Exec("REVOKE USAGE ON SCHEMA gin_part FROM gin_reader",0))
 run(fixture.Exec("SET SESSION AUTHORIZATION gin_reader",0))
 for i:=range ginMembers {
  denied:=fixture.ImplicitError(ginQuery(i),fixture.Diagnostic{Code:"42501",Message:"permission denied for schema gin_part",Position:fixture.Position(15)},nil)
  ginBoth(t,p,b,c,denied)
 }
 run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
 run(fixture.Exec("GRANT USAGE ON SCHEMA gin_part TO gin_reader",0))
}
func TestReferencePageinspectGINPermissions(t *testing.T) {
 fixture.RoleReferenceCleanup(t,[]string{"gin_reader"})
 ginCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {ginAuthority(t,p,b,c)})
}

// Stored consumers retain procedure identity, never the preparing user's raw
// page authority. The same held view statements must recheck after Restore.
func ginHeldViewAuthority(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,held []fixture.HeldStatement) {
 t.Helper();run:=func(s fixture.Step) {fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
 defer func() {if _,err:=c.ExecContext(p.Ctx,"RESET SESSION AUTHORIZATION");err!=nil {t.Error(err)}}()
 run(fixture.Exec("SET SESSION AUTHORIZATION gin_reader",0))
 for i:=range held {
  denied:=fixture.ImplicitError(held[i].SQL,fixture.Diagnostic{Code:"42501",Message:"must be superuser to use raw page functions"})
  denied.SelectRows=true;denied.ErrorStage="execute"
  fixture.RunStep(t,p.Ctx,held[i],b.Recorder,denied)
 }
 run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
 run(fixture.Exec("UPDATE gin_images SET meta=NULL,opaque=NULL,leaf=NULL",1))
 run(fixture.Exec("SET SESSION AUTHORIZATION gin_reader",0))
 for i:=range held {
  null:=ginNullRows(i);if i==2 {null=nil}
  fixture.RunStep(t,p.Ctx,held[i],b.Recorder,ginMetadata(ginViewStep(i,null),b.PG,true))
 }
 run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
 run(fixture.Exec("UPDATE gin_images SET meta=$1,opaque=$2,leaf=$3",1,ginMeta(),ginPage(0xffff),ginPosting()))
 for i:=range held {fixture.RunStep(t,p.Ctx,held[i],b.Recorder,ginMetadata(ginViewStep(i,ginOrdinaryRows(i)),b.PG,true))}
}
