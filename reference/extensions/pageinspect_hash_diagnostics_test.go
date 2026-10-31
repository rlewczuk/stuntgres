package reference_test

import (
 "database/sql"
 "encoding/binary"
 "fmt"
 "testing"
 "stuntgres/reference/fixture"
)
func TestReferencePageinspectHashDiagnostics(t *testing.T){hashCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 var notices []fixture.Diagnostic
 _,c,recorder:=fixture.NoticeReferencePool(t,p,b.PG,&notices);b.Recorder=recorder
 fixture.RunStep(t,p.Ctx,c,recorder,fixture.Exec("CREATE TABLE hash_sentinel(n integer)",0));fixture.RunStep(t,p.Ctx,c,recorder,fixture.Exec("INSERT INTO hash_sentinel VALUES(7)",1))
 for i:=range fixture.HashMembers{
  st,err:=c.PrepareContext(p.Ctx,hashQuery(i));if err!=nil{t.Fatal("prepare ran decoder",err)}
  held:=fixture.HeldStatement{Executor:c,Statement:st,SQL:hashQuery(i)}
  check:=func(raw []byte,code,message,detail string){t.Helper();bad:=hashError(i,raw,code,message,detail);fixture.BRINBoth(t,p,b,c,bad);fixture.RunStep(t,p.Ctx,held,recorder,bad);fixture.RunStep(t,p.Ctx,held,recorder,fixture.HashMetadata(hashStep(i,fixture.HashOrdinary(i),fixture.HashRows(i)),b.PG,false));fixture.RunStep(t,p.Ctx,c,recorder,fixture.Select("SELECT n FROM hash_sentinel",[]string{"n"},[]string{"INT4"},[][]any{{int64(7)}}))}
  for _,n:=range []int{0,3,8191,8193}{check(make([]byte,n),"22023","invalid page size",fmt.Sprintf("Expected 8192 bytes, got %d.",n))}
  raw:=fixture.HashPage(15);binary.LittleEndian.PutUint16(raw[16:],8175);binary.LittleEndian.PutUint16(raw[8190:],0x1234);check(raw,"22023","input page is not a valid hash page","Expected special size 16, got 17.")
  raw=fixture.HashPage(15);binary.LittleEndian.PutUint16(raw[8190:],0x1234);check(raw,"22023","input page is not a valid hash page","Expected 0000ff80, got 00001234.")
  for _,flags:=range []uint16{3,15}{check(fixture.HashPage(flags),"22023",fmt.Sprintf("invalid hash page type %08x",flags),"")}
  if i==1||i==2{for _,flags:=range []uint16{0,4,8}{check(fixture.HashPage(flags),"22023","page is not a hash bucket or overflow page","")};check(make([]byte,8192),"22023","page is not a hash bucket or overflow page","")}
  if i==3{for _,flags:=range []uint16{0,1,2,4}{check(fixture.HashPage(flags),"22023","page is not a hash meta page","")};check(make([]byte,8192),"22023","page is not a hash meta page","")}
  if i==0||i==3{
   raw=fixture.HashMeta();binary.LittleEndian.PutUint32(raw[24:],0);binary.LittleEndian.PutUint32(raw[28:],5);check(raw,"XX002","invalid magic number for metadata","Expected 0x06440640, got 0x00000000.")
   raw=fixture.HashMeta();binary.LittleEndian.PutUint32(raw[28:],5);check(raw,"XX002","invalid version for metadata","Expected 4, got 5.")
  }
  // An error aborts an explicit transaction, and savepoint rollback restores it.
  fixture.RunStep(t,p.Ctx,c,recorder,fixture.Exec("BEGIN",0));fixture.RunStep(t,p.Ctx,c,recorder,fixture.Exec("SAVEPOINT hash_recover",0))
  fixture.RunStep(t,p.Ctx,held,recorder,hashError(i,[]byte{1},"22023","invalid page size","Expected 8192 bytes, got 1."))
  aborted:=fixture.ImplicitError("SELECT n FROM hash_sentinel",fixture.Diagnostic{Code:"25P02",Message:"current transaction is aborted, commands ignored until end of transaction block"});aborted.SelectRows=true;fixture.RunStep(t,p.Ctx,c,recorder,aborted)
  fixture.RunStep(t,p.Ctx,c,recorder,fixture.Exec("ROLLBACK TO SAVEPOINT hash_recover",0));fixture.RunStep(t,p.Ctx,held,recorder,fixture.HashMetadata(hashStep(i,fixture.HashOrdinary(i),fixture.HashRows(i)),b.PG,false));fixture.RunStep(t,p.Ctx,c,recorder,fixture.Exec("COMMIT",0))
  if err:=st.Close();err!=nil{t.Fatal(err)}
 }
 if len(notices)!=0{t.Fatal("hash decoder notices",notices)}
})}
