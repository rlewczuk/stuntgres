package reference_test

import (
 "database/sql"
 "encoding/binary"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferencePageinspectBrinItems(t *testing.T){
 brinCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
  run(fixture.Exec("CREATE TABLE pi_data(n integer)",0));run(fixture.Exec("CREATE INDEX pi_idx ON pi_data USING brin(n)",0))
  rows:=[][]any{{int64(1),int64(2147549186),int64(1),false,false,false,false,"{-7 .. 42}"},{int64(2),nil,nil,nil,nil,nil,nil,nil},{int64(3),int64(2147549186),int64(1),false,false,false,false,"{42 .. -7}"}}
  image:=brinItems(brinInt4(-7,42),nil,brinInt4(42,-7));fixture.BRINBoth(t,p,b,c,brinItemStep(image,rows))
  // lp_len is ignored by brin_deform_tuple. LP_REDIRECT/DEAD are also used.
  word:=binary.LittleEndian.Uint32(image[24:]);binary.LittleEndian.PutUint32(image[24:],word&0x7fff|3<<15)
  fixture.BRINBoth(t,p,b,c,brinItemStep(image,rows))
  // Moving a used pointer to another independently defined safe tuple changes
  // the decoded bytes; the descriptor table remains completely empty.
  binary.LittleEndian.PutUint32(image[24:],binary.LittleEndian.Uint32(image[32:]));rows[0][7]="{42 .. -7}";fixture.BRINBoth(t,p,b,c,brinItemStep(image,rows))
  fixture.BRINBoth(t,p,b,c,brinItemStep(make([]byte,8192),nil));s:=brinItemStep(nil,nil);s.Args=[]any{nil};fixture.BRINBoth(t,p,b,c,s)
  fixture.BRINBoth(t,p,b,c,brinItemStep(fixture.BRINImage(0xf093),[][]any{{int64(1),nil,nil,nil,nil,nil,nil,nil}}))
  for _,flags:=range []byte{0x20,0x40,0x60}{
   tuple:=brinInt4(-7,42);tuple[4]|=flags
   fixture.BRINBoth(t,p,b,c,brinItemStep(brinItems(tuple),[][]any{{int64(1),int64(2147549186),int64(1),false,false,flags&0x40!=0,flags&0x20!=0,"{-7 .. 42}"}}))
  }
  run(fixture.Exec("DROP INDEX pi_idx",0));run(fixture.Exec("DROP TABLE pi_data",0))
  run(fixture.Exec("CREATE TABLE pi_data(a integer,b integer,c integer,d integer,e integer)",0));run(fixture.Exec("CREATE INDEX pi_idx ON pi_data USING brin(a,b,c,d,e)",0))
  // allnull column 2 and hasnull columns 1/2/5 cross the byte boundary.
  // Simultaneous allnull/hasnull for column 2 must still expose hasnull=false.
  data:=make([]byte,32);for i,v:=range []int32{-7,42,3,4,5,6,7,8}{binary.LittleEndian.PutUint32(data[i*4:],uint32(v))}
  image=brinItems(brinTuple(0xe0,[]byte{0x62,0x02},data))
  rows=[][]any{
   {int64(1),int64(2147549186),int64(1),false,true,true,true,"{-7 .. 42}"},
   {int64(1),int64(2147549186),int64(2),true,false,true,true,nil},
   {int64(1),int64(2147549186),int64(3),false,false,true,true,"{3 .. 4}"},
   {int64(1),int64(2147549186),int64(4),false,false,true,true,"{5 .. 6}"},
   {int64(1),int64(2147549186),int64(5),false,true,true,true,"{7 .. 8}"},
  };fixture.BRINBoth(t,p,b,c,brinItemStep(image,rows))
  named:=fixture.Select("SELECT * FROM brin_part.brin_page_items(index_oid => 'pi_idx'::regclass,page => $1) ORDER BY itemoffset,attnum",brinItemNames,brinItemTypes,rows,image);fixture.BRINBoth(t,p,b,c,named)
 })
}
