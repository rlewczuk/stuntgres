package enginetests

import (
 "bytes"
 "encoding/binary"
 "errors"
 "fmt"
 "math"
 "reflect"
 "testing"
 "stuntgres/internal/sqlstate"
 "stuntgres/internal/sqltype"
 "stuntgres/internal/util"
)

func headerUnitAsymmetric() []byte {
 raw:=make([]byte,8192)
 binary.LittleEndian.PutUint32(raw,0x01234567);binary.LittleEndian.PutUint32(raw[4:],0x89abcdef)
 for i,v:=range []uint16{0xfedc,0x8005,0xffff,0x8000,0xfffe,0xfffe} {binary.LittleEndian.PutUint16(raw[8+i*2:],v)}
 binary.LittleEndian.PutUint32(raw[20:],0xffffffff);return raw
}
func headerUnitError(t *testing.T,err error,message,detail string) {
 t.Helper();var got *sqlstate.Error
 want:=sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"22023",Message:message,Detail:detail}
 if !errors.As(err,&got) || !reflect.DeepEqual(*got,want) {t.Fatalf("error %#v want %#v",err,want)}
}
func TestPageinspectHeaderDecode(t *testing.T) {
 asymmetric:=sqltype.PageHeader{LSN:0x0123456789abcdef,Checksum:-292,Flags:-32763,Lower:65535,Upper:32768,Special:65534,PageSize:65280,Version:254,PruneXID:4294967295}
 for _,tc:=range []struct{raw []byte;want sqltype.PageHeader}{
  {make([]byte,8192),sqltype.PageHeader{}},
  {bytes.Repeat([]byte{255},8192),sqltype.PageHeader{LSN:math.MaxUint64,Checksum:-1,Flags:-1,Lower:65535,Upper:65535,Special:65535,PageSize:65280,Version:255,PruneXID:math.MaxUint32}},
  {bytes.Repeat([]byte{1},8192),sqltype.PageHeader{LSN:0x0101010101010101,Checksum:257,Flags:257,Lower:257,Upper:257,Special:257,PageSize:256,Version:1,PruneXID:16843009}},
  {headerUnitAsymmetric(),asymmetric},
 } {
  raw:=string(tc.raw);before:=string(append([]byte(nil),tc.raw...))
  got,err:=sqltype.ReadPageHeader(raw);if err!=nil || got!=tc.want {t.Fatal(got,tc.want,err)}
  if raw!=before {t.Fatal("decoder mutated input")};clear(tc.raw)
  if got!=tc.want {t.Fatal("decoded fields retained caller backing")}
 }
 // Page size/version/flags/offset order are output, not validity constraints.
 raw:=headerUnitAsymmetric();binary.LittleEndian.PutUint16(raw[14:],0);binary.LittleEndian.PutUint16(raw[18:],0x01ff)
 want:=asymmetric;want.Upper=0;want.PageSize=256;want.Version=255
 if got,err:=sqltype.ReadPageHeader(string(raw));err!=nil||got!=want {t.Fatal(got,want,err)}
 for _,n:=range []int{0,3,8191,8193} {
  got,err:=sqltype.ReadPageHeader(string(make([]byte,n)));headerUnitError(t,err,"invalid page size",fmt.Sprintf("Expected 8192 bytes, got %d.",n));if got!=(sqltype.PageHeader{}) {t.Fatal("partial header on failure",got)}
 }
 immutable:=string(headerUnitAsymmetric())
 if alloc,n:=util.AllocationsPerCall(64,func(){if _,err:=sqltype.ReadPageHeader(immutable);err!=nil {panic(err)}});alloc!=0||n!=0 {t.Fatal("header read allocated",alloc,n)}
}

func TestPageinspectHeaderChecksum(t *testing.T) {
 // Independently published PostgreSQL regression vectors, not a second kernel.
 vectors:=[]struct{pattern []byte;want [3]int16}{
  {[]byte{1},[3]int16{1175,1225,1139}},{[]byte{4},[3]int16{28338,28352,28438}},{[]byte{255},[3]int16{3612,3598,3648}},
  {[]byte{0xab,0xcd},[3]int16{-30781,-30795,-30881}},{[]byte{0xe6,0xd6},[3]int16{-16269,-16251,-16305}},{[]byte{0x4a,0x5e},[3]int16{-27377,-27391,-27349}},
 }
 check:=func(raw string,block int64,want int16,newPage bool){t.Helper();before:=string(append([]byte(nil),raw...));got,isNew,err:=sqltype.ReadPageChecksum(raw,block);if err!=nil||got!=want||isNew!=newPage {t.Fatal("checksum",block,got,isNew,err,"want",want,newPage)};if raw!=before {t.Fatal("checksum mutated input")}}
 for _,v:=range vectors {for i,block:=range []int64{0,50,100} {check(string(bytes.Repeat(v.pattern,8192/len(v.pattern))),block,v.want[i],false)}}
 raw:=bytes.Repeat([]byte{1},8192);raw[8],raw[9]=0xdc,0xfe;check(string(raw),0,1175,false)
 raw=bytes.Repeat([]byte{1},8192);raw[100]^=1;check(string(raw),0,3816,false)
 raw=bytes.Repeat([]byte{1},8192);raw[10]^=1;check(string(raw),0,-13768,false)
 raw=bytes.Repeat([]byte{1},8192)
 for _,tc:=range []struct{block int64;want int16}{{10,1169},{2147483648,-31594},{4294967294,-1175}} {check(string(raw),tc.block,tc.want,false)}
 check(string(make([]byte,8192)),0,0,true)
 raw=bytes.Repeat([]byte{255},8192);raw[14],raw[15]=0,0;check(string(raw),4294967294,0,true)
 for _,block:=range []int64{-1,4294967295,math.MinInt64,math.MaxInt64} {for _,input:=range []string{"",string(raw),string(bytes.Repeat([]byte{1},8192))} {v,isNew,err:=sqltype.ReadPageChecksum(input,block);headerUnitError(t,err,"invalid block number","");if v!=0||isNew {t.Fatal("value on block failure",v,isNew)}}}
 for _,n:=range []int{0,3,8191,8193} {for _,block:=range []int64{0,4294967294} {v,isNew,err:=sqltype.ReadPageChecksum(string(make([]byte,n)),block);headerUnitError(t,err,"invalid page size",fmt.Sprintf("Expected 8192 bytes, got %d.",n));if v!=0||isNew {t.Fatal("value on size failure",v,isNew)}}}
 immutable:=string(bytes.Repeat([]byte{1},8192))
 if alloc,n:=util.AllocationsPerCall(64,func(){if _,_,err:=sqltype.ReadPageChecksum(immutable,0);err!=nil {panic(err)}});alloc!=0||n!=0 {t.Fatal("checksum copied or allocated",alloc,n)}
}
