package enginetests

import (
 "context"
 "encoding/binary"
 "strings"
 "testing"
 "stuntgres/internal/sqltype"
)

func brinUnitTypes()map[sqltype.ID]*sqltype.Descriptor{out:=map[sqltype.ID]*sqltype.Descriptor{};for _,d:=range sqltype.Builtins(){out[d.ID()]=d};return out}
func TestPageinspectBrinNativeDatum(t *testing.T){
 types:=brinUnitTypes()
 for _,tc:=range []struct{id sqltype.ID;raw string;at,end int;want string}{
  {sqltype.Int4,"\x00\x00\x00\x00\xf9\xff\xff\xff",1,8,"-7"},
  {sqltype.Text,"\x00\x07xy",1,4,"xy"},
  {sqltype.Text,"\x00\x00\x00\x00\x18\x00\x00\x00xy",1,10,"xy"},
  {sqltype.Numeric,"\x0b\x00\x81\x07\x00",0,5,"7.00"},
  {sqltype.Tid,"\x01\x00\x02\x00\x01\x02",0,6,"(65538,513)"},
  {sqltype.Float4,"\x00\x00\x00\x80",0,4,"-0"},
 }{
  d:=types[tc.id];v,next,err:=sqltype.NativeDatum(tc.raw,tc.at,d.Usage());if err!=nil{t.Fatal(tc,err)}
  text,err:=d.Output(context.Background(),v);if err!=nil || next!=tc.end || text!=tc.want || v.Type()!=d{t.Fatal(tc,text,next,err)}
 }
 // No panic, SQLSTATE invention, NULL fallback or out-of-bounds slice for
 // undefined corrupt native lengths, pointers and compressed payloads.
 for _,raw:=range []string{"","\x01","\xfc\xff\xff\xff","\x22\x00\x00\x00\x64\x00\x00\x00"}{
  if _,_,err:=sqltype.NativeDatum(raw,0,types[sqltype.Text].Usage());err==nil{t.Fatal("unsafe datum accepted",raw)}
 }
}
func TestPageinspectBrinCompression(t *testing.T){
 d:=brinUnitTypes()[sqltype.Text]
 for _,tc:=range []struct{method uint32;payload []byte}{
  {0,[]byte{2,'A',15,1,81}},
  {1,[]byte{0x1f,'A',1,0,75,0x50,'A','A','A','A','A'}},
 }{
  raw:=make([]byte,8);binary.LittleEndian.PutUint32(raw,uint32(8+len(tc.payload))<<2|2);binary.LittleEndian.PutUint32(raw[4:],tc.method<<30|100);raw=append(raw,tc.payload...)
  immutable:=string(raw);v,next,err:=sqltype.NativeDatum(immutable,0,d.Usage());if err!=nil{t.Fatal(err)};raw[8]=0
  got,err:=v.Text();if err!=nil || got!=strings.Repeat("A",100) || next!=len(immutable){t.Fatal(got,next,err)}
 }
}
func TestPageinspectBrinPageHeaders(t *testing.T){
 raw:=make([]byte,8192);binary.LittleEndian.PutUint16(raw[14:],8184);binary.LittleEndian.PutUint16(raw[16:],8184);binary.LittleEndian.PutUint16(raw[18:],0x20ff);binary.LittleEndian.PutUint16(raw[8190:],0xf091)
 for i,v:=range []uint32{0xa8109cfa,0xffffffff,0x80000000,0xffffffff}{binary.LittleEndian.PutUint32(raw[24+i*4:],v)}
 page,err:=sqltype.ReadBrinPage(string(raw),0xf091);if err!=nil{t.Fatal(err)};magic,version,width,last:=page.Meta()
 if page.IsNew() || page.PageType()!="meta" || magic!="0xA8109CFA" || version!=-1 || width!=-2147483648 || last!=4294967295{t.Fatal(magic,version,width,last)}
 raw[14],raw[15]=0,0;raw[16]=0;page,err=sqltype.ReadBrinPage(string(raw),0xf092);if err!=nil || !page.IsNew(){t.Fatal(page,err)}
 rows,err:=page.Revmap(brinUnitTypes()[sqltype.Tid]);if err!=nil || len(rows)!=1 || !rows[0].IsNull(){t.Fatal(rows,err)}
 for _,n:=range []int{0,8191,8193}{if _,err:=sqltype.ReadBrinPage(string(make([]byte,n)),0);err==nil{t.Fatal(n)}}
}
