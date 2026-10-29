package reference_test

import (
 "database/sql"
 "encoding/binary"
 "testing"
 "stuntgres/reference/fixture"
)

func brinTuple(info byte,bitmap []byte,data []byte)[]byte{
 p:=make([]byte,8);binary.LittleEndian.PutUint32(p,0x80010002);p[4]=info|8;copy(p[5:],bitmap);return append(p,data...)
}
func brinItems(tuples ...[]byte)[]byte{
 p:=fixture.BRINImage(0xf093);upper:=8184
 for i,tuple:=range tuples{
  if tuple==nil{continue};upper=(upper-len(tuple))& -8;copy(p[upper:],tuple)
  binary.LittleEndian.PutUint32(p[24+i*4:],uint32(upper)|1<<15|uint32(len(tuple))<<17)
 }
 binary.LittleEndian.PutUint16(p[12:],uint16(24+4*len(tuples)));binary.LittleEndian.PutUint16(p[14:],uint16(upper));return p
}
func brinInt4(a,b int32)[]byte{p:=make([]byte,8);binary.LittleEndian.PutUint32(p,uint32(a));binary.LittleEndian.PutUint32(p[4:],uint32(b));return brinTuple(0,nil,p)}
func brinShort(p []byte)[]byte{return append([]byte{byte((len(p)+1)*2+1)},p...)}
func brinLong(p []byte)[]byte{out:=make([]byte,4,len(p)+4);binary.LittleEndian.PutUint32(out,uint32(len(p)+4)<<2);return append(out,p...)}
func brinPair(p []byte,align int,short bool)[]byte{out:=append([]byte(nil),p...);if !short{for (8+len(out))%align!=0{out=append(out,0)}};return append(out,p...)}
func brinCase(t *testing.T,body func(*testing.T,*fixture.Pair,fixture.Backend,*sql.Conn)){
 t.Helper();fixture.SchemaReferenceCleanup(t,"brin_part","brin_moved");p:=fixture.NewScenario(t)
 for _,b:=range p.Backends(){t.Run(b.Name,func(t *testing.T){
  c,err:=b.DB.Conn(p.Ctx);if err!=nil{t.Fatal(err)};defer c.Close()
  if b.PG{clean:=func(){for _,q:=range []string{"ROLLBACK","RESET SESSION AUTHORIZATION","DROP EXTENSION IF EXISTS pageinspect CASCADE","DROP SCHEMA IF EXISTS brin_part CASCADE","DROP SCHEMA IF EXISTS brin_moved CASCADE"}{if _,err:=c.ExecContext(p.Ctx,q);err!=nil{t.Error(q,err)}}};clean();defer clean()}
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE SCHEMA brin_part",0));fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE EXTENSION pageinspect SCHEMA brin_part",0))
  body(t,p,b,c)
 })}
}
var brinItemNames=[]string{"itemoffset","blknum","attnum","allnulls","hasnulls","placeholder","empty","value"}
var brinItemTypes=[]string{"INT4","INT8","INT4","BOOL","BOOL","BOOL","BOOL","TEXT"}
func brinItemStep(page []byte,rows [][]any)fixture.Step{s:=fixture.Select("SELECT * FROM brin_part.brin_page_items($1,'pi_idx'::regclass) ORDER BY itemoffset,attnum",brinItemNames,brinItemTypes,rows,page);return s}
