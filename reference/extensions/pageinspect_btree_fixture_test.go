package reference_test

import (
 "database/sql"
 "encoding/binary"
 "testing"
 "stuntgres/reference/fixture"
)

// Independently assembled REL_18_6 bufpage/itemid/itup/nbtree images.
func btTID(p []byte,block uint32,offset uint16){binary.LittleEndian.PutUint16(p,uint16(block>>16));binary.LittleEndian.PutUint16(p[2:],uint16(block));binary.LittleEndian.PutUint16(p[4:],offset)}
func btTuple(block uint32,offset,info uint16,payload ...byte)[]byte{p:=make([]byte,8+len(payload));btTID(p,block,offset);binary.LittleEndian.PutUint16(p[6:],info);copy(p[8:],payload);return p}
func btPage(flags uint16,level,next uint32,tuples ...[]byte)[]byte{
 p:=make([]byte,8192);upper:=8176
 binary.LittleEndian.PutUint16(p[12:],uint16(24+4*len(tuples)));binary.LittleEndian.PutUint16(p[16:],8176);binary.LittleEndian.PutUint16(p[18:],0x2004)
 binary.LittleEndian.PutUint32(p[8180:],next);binary.LittleEndian.PutUint32(p[8184:],level);binary.LittleEndian.PutUint16(p[8188:],flags)
 for i,tuple:=range tuples{upper=(upper-len(tuple))& -8;copy(p[upper:],tuple);binary.LittleEndian.PutUint32(p[24+4*i:],uint32(upper)|1<<15|uint32(len(tuple))<<17)}
 binary.LittleEndian.PutUint16(p[14:],uint16(upper));return p
}
var btKey=[]byte{42,0,0,0,0,0,0,0}
func btOrdinary()[]byte{return btPage(1,0,0,btTuple(65538,513,16,249,255,255,255,0,0,0,0))}
func btPosting()[]byte{p:=btTuple(16,8194,0x2020,make([]byte,24)...);copy(p[8:],btKey);btTID(p[16:],1,6);btTID(p[22:],65538,513);copy(p[28:],[]byte{9,8,7,6});return btPage(1,0,0,p)}
var btNames=[]string{"itemoffset","ctid","itemlen","nulls","vars","data","dead","htid","tids"}
var btTypes=[]string{"INT2","TID","INT2","BOOL","BOOL","TEXT","BOOL","TID","_TID"}
var btOrdinaryRows=[][]any{{int64(1),"(65538,513)",int64(16),false,false,"f9 ff ff ff 00 00 00 00",false,"(65538,513)",nil}}
var btPostingRows=[][]any{{int64(1),"(16,8194)",int64(32),false,false,"2a 00 00 00 00 00 00 00",false,"(1,6)",`{"(1,6)","(65538,513)"}`}}
var btNullRows=[][]any{{nil,nil,nil,nil,nil,nil,nil,nil,nil}}
const btQuery="SELECT * FROM bt_part.bt_page_items($1) ORDER BY itemoffset"
func btStep(page []byte,rows [][]any)fixture.Step{return fixture.Select(btQuery,btNames,btTypes,rows,page)}
func btBoth(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,s fixture.Step){t.Helper();fixture.RunStep(t,p.Ctx,c,b.Recorder,s);fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Prepared(s))}
func btCase(t *testing.T,body func(*testing.T,*fixture.Pair,fixture.Backend,*sql.Conn)){
 t.Helper();fixture.SchemaReferenceCleanup(t,"bt_part","bt_moved");p:=fixture.NewScenario(t)
 for _,b:=range p.Backends(){t.Run(b.Name,func(t *testing.T){c,err:=b.DB.Conn(p.Ctx);if err!=nil{t.Fatal(err)};defer c.Close()
  if b.PG{clean:=func(){for _,q:=range []string{"ROLLBACK","RESET SESSION AUTHORIZATION","DROP EXTENSION IF EXISTS pageinspect CASCADE","DROP SCHEMA IF EXISTS bt_part CASCADE","DROP SCHEMA IF EXISTS bt_moved CASCADE"}{if _,err:=c.ExecContext(p.Ctx,q);err!=nil{t.Error(q,err)}}};clean();defer clean()}
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE SCHEMA bt_part",0));fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE EXTENSION pageinspect SCHEMA bt_part",0));body(t,p,b,c)
 })}
}
