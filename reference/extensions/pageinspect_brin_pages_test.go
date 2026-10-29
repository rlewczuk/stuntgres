package reference_test

import (
 "database/sql"
 "encoding/binary"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferencePageinspectBrinInstallation(t *testing.T){
 t.Run("uninstalled",func(t *testing.T){
  p:=fixture.NewScenario(t)
  for _,b:=range p.Backends(){
   fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("DROP EXTENSION IF EXISTS pageinspect CASCADE",0))
   s:=fixture.ImplicitError(`SELECT brin_page_type('\x'::bytea)`,fixture.Diagnostic{Code:"42883",Message:"function brin_page_type(bytea) does not exist",Hint:"No function matches the given name and argument types. You might need to add explicit type casts.",Position:"8"})
   fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s);fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Prepared(s))
  }
 })
 brinCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  s:=fixture.Select("SELECT p.proname,p.proargtypes::text AS args,p.prorettype::integer AS result,p.proisstrict,p.provolatile,p.proretset,p.proparallel FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='brin_part' AND p.proname IN ('brin_page_type','brin_metapage_info','brin_revmap_data','brin_page_items') ORDER BY p.proname",[]string{"proname","args","result","proisstrict","provolatile","proretset","proparallel"},[]string{"NAME","TEXT","INT4","BOOL","CHAR","BOOL","CHAR"},[][]any{
   {"brin_metapage_info","17",int64(2249),true,"v",false,"s"},{"brin_page_items","17 2205",int64(2249),true,"v",true,"r"},{"brin_page_type","17",int64(25),true,"v",false,"s"},{"brin_revmap_data","17",int64(27),true,"v",true,"s"},
  });fixture.BRINBoth(t,p,b,c,s)
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE EXTENSION IF NOT EXISTS pageinspect",0))
  fixture.BRINBoth(t,p,b,c,fixture.Select("SELECT brin_part.brin_page_type(page => $1)",[]string{"brin_page_type"},[]string{"TEXT"},[][]any{{"meta"}},fixture.BRINMeta()))
 })
}
func TestReferencePageinspectBrinPages(t *testing.T){
 brinCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  for _,tc:=range []struct{tag uint16;want string}{{0xf091,"meta"},{0xf092,"revmap"},{0xf093,"regular"},{1,"unknown (01)"},{0x1234,"unknown (1234)"}}{
   fixture.BRINBoth(t,p,b,c,fixture.Select("SELECT brin_part.brin_page_type($1)",[]string{"brin_page_type"},[]string{"TEXT"},[][]any{{tc.want}},fixture.BRINImage(tc.tag)))
  }
  names:=[]string{"magic","version","pagesperrange","lastrevmappage"};types:=[]string{"TEXT","INT4","INT4","INT8"}
  q:="SELECT * FROM brin_part.brin_metapage_info($1)"
  fixture.BRINBoth(t,p,b,c,fixture.Select(q,names,types,[][]any{{"0xA8109CFA",int64(1),int64(128),int64(1)}},fixture.BRINMeta()))
  fixture.BRINBoth(t,p,b,c,fixture.Select("SELECT (brin_part.brin_metapage_info($1)).magic AS magic",[]string{"magic"},[]string{"TEXT"},[][]any{{"0xA8109CFA"}},fixture.BRINMeta()))
  mutated:=fixture.BRINMeta();binary.LittleEndian.PutUint32(mutated[24:],0);binary.LittleEndian.PutUint32(mutated[28:],99);mutated[18]=0xff
  fixture.BRINBoth(t,p,b,c,fixture.Select(q,names,types,[][]any{{"0x00000000",int64(99),int64(128),int64(1)}},mutated))
  binary.LittleEndian.PutUint32(mutated[28:],0xffffffff);binary.LittleEndian.PutUint32(mutated[32:],0x80000000);binary.LittleEndian.PutUint32(mutated[36:],0xffffffff)
  fixture.BRINBoth(t,p,b,c,fixture.Select(q,names,types,[][]any{{"0x00000000",int64(-1),int64(-2147483648),int64(4294967295)}},mutated))
  fresh:=fixture.BRINMeta();binary.LittleEndian.PutUint16(fresh[14:],0);binary.LittleEndian.PutUint16(fresh[16:],7)
  for _,page:=range []any{fresh,nil}{
   fixture.BRINBoth(t,p,b,c,fixture.Select("SELECT brin_part.brin_page_type($1)",[]string{"brin_page_type"},[]string{"TEXT"},[][]any{{nil}},page))
   fixture.BRINBoth(t,p,b,c,fixture.Select(q,names,types,[][]any{{nil,nil,nil,nil}},page))
   fixture.BRINBoth(t,p,b,c,fixture.Select("SELECT (brin_part.brin_metapage_info($1)).magic AS magic",[]string{"magic"},[]string{"TEXT"},[][]any{{nil}},page))
  }
 })
}
func TestReferencePageinspectBrinRevmap(t *testing.T){
 brinCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  page:=fixture.BRINImage(0xf092)
  // TID's block number is two little-endian uint16 halves, high half first.
  copy(page[24:],[]byte{1,0,2,0,1,2});copy(page[24+679*6:],[]byte{0,128,7,0,0,0});copy(page[24+1359*6:],[]byte{255,255,255,255,255,255})
  rows:=make([][]any,1360);for i:=range rows{rows[i]=[]any{"(0,0)"}};rows[0]=[]any{"(65538,513)"};rows[679]=[]any{"(2147483655,0)"};rows[1359]=[]any{"(4294967295,65535)"}
  s:=fixture.Select("SELECT * FROM brin_part.brin_revmap_data($1)",[]string{"pages"},[]string{"TID"},rows,page);s.Bag=true;fixture.BRINBoth(t,p,b,c,s)
  s.Rows=[][]any{{nil}};s.Args=[]any{make([]byte,8192)};fixture.BRINBoth(t,p,b,c,s)
  s.Rows=nil;s.Args=[]any{nil};fixture.BRINBoth(t,p,b,c,s)
 })
}
