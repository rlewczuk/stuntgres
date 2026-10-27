package reference_test

import (
 "database/sql"
 "encoding/binary"
 "encoding/hex"
 "testing"
 "stuntgres/reference/fixture"
)
func TestReferencePageinspectBtreeRows(t *testing.T){btCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 for _,tc:=range []struct{name string;page []byte;rows [][]any}{
  {"ordinary",btOrdinary(),btOrdinaryRows},{"posting",btPosting(),btPostingRows},{"strict_null",nil,nil},{"new",make([]byte,8192),btNullRows},{"empty",btPage(1,0,0),nil},
  {"zero_key",btPage(0,1,0,btTuple(7,0,0x2008)),[][]any{{int64(1),"(7,0)",int64(8),false,false,"",nil,nil,nil}}},
  {"key_pivot",btPage(0,1,0,btTuple(9,1,0x2010,btKey...)),[][]any{{int64(1),"(9,1)",int64(16),false,false,"2a 00 00 00 00 00 00 00",nil,nil,nil}}},
  {"legacy",btPage(0,1,0,btTuple(9,1,16,btKey...)),[][]any{{int64(1),"(9,1)",int64(16),false,false,"2a 00 00 00 00 00 00 00",nil,nil,nil}}},
 }{t.Run(tc.name,func(t *testing.T){btBoth(t,p,b,c,btStep(tc.page,tc.rows))})}
 trailer:=btTuple(9,4097,0x2018,make([]byte,16)...);copy(trailer[8:],btKey);trailer[16]=99;trailer[17]=88;btTID(trailer[18:],65538,513)
 btBoth(t,p,b,c,btStep(btPage(0,1,0,trailer),[][]any{{int64(1),"(9,4097)",int64(24),false,false,"2a 00 00 00 00 00 00 00",nil,"(65538,513)",nil}}))
 for _,flags:=range []uint32{0,2,3}{page:=btOrdinary();word:=binary.LittleEndian.Uint32(page[24:]);binary.LittleEndian.PutUint32(page[24:],word&0x7fff|flags<<15|1<<17);row:=append([]any(nil),btOrdinaryRows[0]...);row[6]=flags==3;btBoth(t,p,b,c,btStep(page,[][]any{row}))}
 for _,info:=range []uint16{0x4010,0x8018,0xc018}{payload:=[]byte{7,120,121,0,0,0,0,0};if info&0x8000!=0{payload=append([]byte{255,254,253,252,251,250,249,248},payload...)};btBoth(t,p,b,c,btStep(btPage(1,0,0,btTuple(65538,513,info,payload...)),[][]any{{int64(1),"(65538,513)",int64(info&0x1fff),info&0x8000!=0,info&0x4000!=0,"07 78 79 00 00 00 00 00",false,"(65538,513)",nil}}))}
 high:=btPage(1,0,9,btTuple(9,1,16,btKey...),btTuple(65538,513,16,249,255,255,255,0,0,0,0));second:=append([]any(nil),btOrdinaryRows[0]...);second[0]=int64(2)
 btBoth(t,p,b,c,btStep(high,[][]any{{int64(1),"(9,1)",int64(16),false,false,"2a 00 00 00 00 00 00 00",nil,nil,nil},second}))
 half:=btOrdinary();binary.LittleEndian.PutUint16(half[8188:],17);btBoth(t,p,b,c,btStep(half,btOrdinaryRows))
 newCorrupt:=btPage(13,1,0);binary.LittleEndian.PutUint16(newCorrupt[14:],0);binary.LittleEndian.PutUint16(newCorrupt[16:],65535);btBoth(t,p,b,c,btStep(newCorrupt,btNullRows))
})}
func TestReferencePageinspectBtreeArrays(t *testing.T){btCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 btBoth(t,p,b,c,fixture.Select(`SELECT '{"(1,6)","(65538,513)"}'::tid[] AS a`,[]string{"a"},[]string{"_TID"},[][]any{{`{"(1,6)","(65538,513)"}`}}))
 q:="SELECT tids[1] AS a,tids[2] AS b,tids[3] AS c,tids[1:1] AS d,array_lower(tids,1) AS lo,array_upper(tids,1) AS hi FROM bt_part.bt_page_items($1) ORDER BY itemoffset"
 btBoth(t,p,b,c,fixture.Select(q,[]string{"a","b","c","d","lo","hi"},[]string{"TID","TID","TID","_TID","INT4","INT4"},[][]any{{"(1,6)","(65538,513)",nil,`{"(1,6)"}`,int64(1),int64(2)}},btPosting()))
 btBoth(t,p,b,c,fixture.Select(q,[]string{"a","b","c","d","lo","hi"},[]string{"TID","TID","TID","_TID","INT4","INT4"},[][]any{{nil,nil,nil,nil,nil,nil}},btOrdinary()))
 tuple:=btTuple(24,8195,0xa030,make([]byte,40)...);copy(tuple[16:],btKey);btTID(tuple[24:],7,3);btTID(tuple[30:],1,6);btTID(tuple[36:],65538,513);page:=btPage(1,0,0,tuple);word:=binary.LittleEndian.Uint32(page[24:]);binary.LittleEndian.PutUint32(page[24:],word|3<<15)
 btBoth(t,p,b,c,btStep(page,[][]any{{int64(1),"(24,8195)",int64(48),true,false,"2a 00 00 00 00 00 00 00",true,"(7,3)",`{"(7,3)","(1,6)","(65538,513)"}`}}))
})}
func TestReferencePageinspectBtreeBinding(t *testing.T){btCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 for _,q:=range []string{"SELECT * FROM bt_part.bt_page_items(page => $1) ORDER BY itemoffset","SELECT * FROM bt_part.bt_page_items($1::bytea) ORDER BY itemoffset"}{s:=btStep(btOrdinary(),btOrdinaryRows);s.Query=q;btBoth(t,p,b,c,s)}
 for _,q:=range []string{"CREATE DOMAIN bt_bytes AS bytea","CREATE TABLE bt_raw(raw bytea)"}{fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(q,0))}
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO bt_raw VALUES($1)",1,btOrdinary()))
 for _,q:=range []string{"SELECT i.* FROM bt_raw CROSS JOIN LATERAL bt_part.bt_page_items(raw) AS i ORDER BY itemoffset","SELECT * FROM bt_part.bt_page_items('\\x"+hex.EncodeToString(btOrdinary())+"') ORDER BY itemoffset"}{btBoth(t,p,b,c,fixture.Select(q,btNames,btTypes,btOrdinaryRows))}
 // Give pq the builtin BYTEA parameter OID before exercising domain coercion.
 s:=btStep(btOrdinary(),btOrdinaryRows);s.Query="SELECT * FROM bt_part.bt_page_items($1::bytea::bt_bytes) ORDER BY itemoffset";btBoth(t,p,b,c,s)
 btBoth(t,p,b,c,fixture.Select("SELECT bt_part.bt_page_items($1)",[]string{"bt_page_items"},[]string{"RECORD"},[][]any{{`(1,"(65538,513)",16,f,f,"f9 ff ff ff 00 00 00 00",f,"(65538,513)",)`}},btOrdinary()))
 for _,tc:=range []struct{call,signature string}{{"1","integer"},{"",""},{"'\\x'::bytea,'\\x'::bytea","bytea, bytea"},{"wrong => '\\x'::bytea","wrong => bytea"}}{
  s:=fixture.ImplicitError("SELECT * FROM bt_part.bt_page_items("+tc.call+")",fixture.Diagnostic{Code:"42883",Message:"function bt_part.bt_page_items("+tc.signature+") does not exist",Hint:"No function matches the given name and argument types. You might need to add explicit type casts.",Position:fixture.Position(15)});btBoth(t,p,b,c,s)
 }
 btBoth(t,p,b,c,fixture.ImplicitError("SELECT * FROM bt_part.bt_page_items('\\xGG')",fixture.Diagnostic{Code:"22023",Message:`invalid hexadecimal digit: "G"`,Position:fixture.Position(37)}))
})}
