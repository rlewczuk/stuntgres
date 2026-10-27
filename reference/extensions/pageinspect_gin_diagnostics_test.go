package reference_test

import (
 "database/sql"
 "encoding/binary"
 "fmt"
 "testing"

 "stuntgres/reference/fixture"
)

func ginError(i int,raw []byte,code,message,detail string) fixture.Step {
 s:=fixture.ImplicitError(ginQuery(i),fixture.Diagnostic{Code:code,Message:message,Detail:detail},raw)
 s.SelectRows=true;s.ErrorStage="execute";return s
}
func TestReferencePageinspectGINDiagnostics(t *testing.T) {ginCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
 var notices []fixture.Diagnostic
 _,noticeConn,recorder:=fixture.NoticeReferencePool(t,p,b.PG,&notices)
 c=noticeConn;b.Recorder=recorder
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE TABLE gin_sentinel(n integer)",0))
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO gin_sentinel VALUES(7)",1))
 for i:=range ginMembers {
  st,err:=c.PrepareContext(p.Ctx,ginQuery(i));if err!=nil {t.Fatal("body must not run during prepare",err)}
  held:=fixture.HeldStatement{Executor:c,Statement:st,SQL:ginQuery(i)}
  check:=func(s fixture.Step) {
   t.Helper();ginBoth(t,p,b,c,s);fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
   fixture.RunStep(t,p.Ctx,held,b.Recorder,ginStep(i,ginOrdinary(i),ginOrdinaryRows(i)))
   ginBoth(t,p,b,c,fixture.Select("SELECT n FROM gin_sentinel ORDER BY n",[]string{"n"},[]string{"INT4"},[][]any{{int64(7)}}))
  }
  for _,n:=range []int{0,3,8191,8193} {check(ginError(i,make([]byte,n),"22023","invalid page size",fmt.Sprintf("Expected 8192 bytes, got %d.",n)))}
  raw:=ginOrdinary(i);binary.LittleEndian.PutUint16(raw[16:],8183)
  // The flags are also invalid for meta/leaf: special size must win.
  binary.LittleEndian.PutUint16(raw[8190:],0xffff)
  message:="input page is not a valid GIN data leaf page";if i==0 {message="input page is not a valid GIN metapage"}
  check(ginError(i,raw,"22023",message,"Expected special size 8, got 9."))
  if i!=1 {for _,flags:=range []uint16{0,9,0x81,0x87,0xc3,0xffff} {
   raw:=ginOrdinary(i);binary.LittleEndian.PutUint16(raw[8190:],flags)
   message,expected:="input page is not a GIN metapage",uint16(8)
   if i==2 {message,expected="input page is not a compressed GIN data leaf page",0x83}
   check(ginError(i,raw,"22023",message,fmt.Sprintf("Flags %04X, expected %04X",flags,expected)))
  }}
  // PageIsNew precedes special-space/type inspection, but not length.
  raw=ginOrdinary(i);binary.LittleEndian.PutUint16(raw[14:],0);binary.LittleEndian.PutUint16(raw[16:],65535);binary.LittleEndian.PutUint16(raw[18:],0xffff)
  ginBoth(t,p,b,c,ginStep(i,raw,ginNullRows(i)))
  if err:=st.Close();err!=nil {t.Fatal(err)}
 }
 if len(notices)!=0 {t.Fatal("GIN controlled paths emitted notices",notices)}
 for i:=range ginMembers {null:=ginNullRows(i);if i==2 {null=nil};for _,s:=range []fixture.Step{
  ginStep(i,ginOrdinary(i),ginOrdinaryRows(i)),ginStep(i,nil,null),ginStep(i,make([]byte,8192),ginNullRows(i)),ginError(i,[]byte{1,2,3},"22023","invalid page size","Expected 8192 bytes, got 3."),
 } {for _,prepared:=range []bool{false,true} {notices=nil;step:=s;if prepared {step=fixture.Prepared(step)};fixture.RunStep(t,p.Ctx,noticeConn,recorder,step);if len(notices)!=0 {t.Fatal("GIN emitted notices",notices)}}}}
})}
