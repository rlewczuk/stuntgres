package enginetests

import (
 "context"
 "encoding/binary"
 "testing"
 "stuntgres/internal/sqltype"
)

func TestPageinspectBrinNativeFamilies(t *testing.T){
 types:=brinUnitTypes()
 le32:=func(n uint32)string{p:=make([]byte,4);binary.LittleEndian.PutUint32(p,n);return string(p)}
 le64:=func(n uint64)string{p:=make([]byte,8);binary.LittleEndian.PutUint64(p,n);return string(p)}
 short:=func(s string)string{return string([]byte{byte((len(s)+1)*2+1)})+s}
 cases:=[]struct{id sqltype.ID;raw,want string}{
  {sqltype.Int2,"\xff\x7f","32767"},{sqltype.Int4,le32(42),"42"},{sqltype.Int8,le64(42),"42"},
  {sqltype.Oid,le32(0xffffffff),"4294967295"},{sqltype.Char,"Q","Q"},{sqltype.Char,"\x00",""},
  {sqltype.Name,"hello"+string(make([]byte,59)),"hello"},
  {sqltype.Float4,le32(0x7f800000),"Infinity"},{sqltype.Float8,le64(0x7ff8000000000000),"NaN"},
  {sqltype.Text,short("é{ .. }"),"é{ .. }"},{sqltype.VarChar,short("abc"),"abc"},{sqltype.BPChar,short("a  "),"a  "},
  {sqltype.Bytea,short("\x00\xff"),"\\x00ff"},
  {sqltype.Bit,short(le32(3)+"\xa0"),"101"},{sqltype.VarBit,short(le32(11)+"\xa5\xa0"),"10100101101"},
  {sqltype.Numeric,short("\x01\xa2\x01\x00\x29\x09\x85\x1a"),"-12345.6789"},
  {sqltype.Numeric,short("\x00\xf0"),"-Infinity"},
  {sqltype.Date,le32(0),"2000-01-01"},{sqltype.Time,le64(1234567),"00:00:01.234567"},
  {sqltype.Timestamp,le64(1234567),"2000-01-01 00:00:01.234567"},
  {sqltype.Timestamptz,le64(0x7fffffffffffffff),"infinity"},
  {sqltype.Timetz,le64(1234567)+le32(18000),"00:00:01.234567-05"},
  {sqltype.Interval,le64(1234567)+le32(2)+le32(3),"3 mons 2 days 00:00:01.234567"},
  {sqltype.Inet,short("\x02\x18\xc0\x00\x02\x07"),"192.0.2.7/24"},
  {sqltype.Cidr,short("\x02\x18\xc0\x00\x02\x00"),"192.0.2.0/24"},
  {sqltype.Macaddr,"\x00\x01\x02\xfe\xff\x07","00:01:02:fe:ff:07"},
  {sqltype.Macaddr8,"\x80\x01\x02\x03\x04\x05\x06\x07","80:01:02:03:04:05:06:07"},
  {sqltype.UUID,"\x00\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f","00010203-0405-0607-0809-0a0b0c0d0e0f"},
 }
 for _,tc:=range cases{d:=types[tc.id];value,next,err:=sqltype.NativeDatum(tc.raw,0,d.Usage());if err!=nil{t.Fatal(tc.id,err)};out,err:=d.Output(context.Background(),value);if err!=nil || out!=tc.want || next!=len(tc.raw) || value.Type()!=d{t.Fatal(tc.id,out,next,err)}}
}
