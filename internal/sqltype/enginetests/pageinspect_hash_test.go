package enginetests

import (
 "context"
 "encoding/binary"
 "errors"
 "math"
 "reflect"
 "testing"
 "unsafe"
 "stuntgres/internal/sqltype"
)
func hashUnitPage(flags uint16)[]byte{
 p:=make([]byte,8192);binary.LittleEndian.PutUint16(p[12:],24);binary.LittleEndian.PutUint16(p[14:],8176);binary.LittleEndian.PutUint16(p[16:],8176);binary.LittleEndian.PutUint16(p[18:],0x2004)
 binary.LittleEndian.PutUint32(p[8176:],0xffffffff);binary.LittleEndian.PutUint32(p[8180:],17);binary.LittleEndian.PutUint32(p[8184:],0x80000000);binary.LittleEndian.PutUint16(p[8188:],flags);binary.LittleEndian.PutUint16(p[8190:],0xff80)
 if flags&15==8{binary.LittleEndian.PutUint32(p[24:],0x06440640);binary.LittleEndian.PutUint32(p[28:],4)};return p
}
func hashUnitRead(t *testing.T,raw string,kind sqltype.HashPageKind)sqltype.HashPage{t.Helper();p,err:=sqltype.ReadHashPage(raw,kind);if err!=nil{t.Fatal(err)};return p}
func hashUnitItems()[]byte{
 p:=hashUnitPage(2);binary.LittleEndian.PutUint16(p[12:],32);binary.LittleEndian.PutUint16(p[14:],8144)
 binary.LittleEndian.PutUint32(p[24:],8160|1<<15|16<<17);binary.LittleEndian.PutUint32(p[28:],8144|3<<15|16<<17)
 btUnitTID(p[8160:],65538,513);binary.LittleEndian.PutUint16(p[8166:],16);binary.LittleEndian.PutUint32(p[8168:],0xffffffff)
 btUnitTID(p[8144:],9,7);binary.LittleEndian.PutUint16(p[8150:],16);binary.LittleEndian.PutUint32(p[8152:],0x80000000);return p
}
func TestHashPageDecode(t *testing.T){
 for _,tc:=range []struct{flags uint16;name string}{{0,"unused"},{1,"overflow"},{2,"bucket"},{4,"bitmap"},{8,"metapage"},{0x8002,"bucket"}}{if got:=hashUnitRead(t,string(hashUnitPage(tc.flags)),sqltype.HashAnyPage).PageType();got!=tc.name{t.Fatal(got,tc)}}
 raw:=hashUnitItems();immutable:=string(raw);page:=hashUnitRead(t,immutable,sqltype.HashBucketPage)
 if backing:=reflect.ValueOf(page).FieldByName("raw").String();unsafe.StringData(backing)!=unsafe.StringData(immutable){t.Fatal("hash reader copied immutable bytes")}
 raw[8168]=0
 stats,err:=page.Stats();if err!=nil||stats!=[9]int64{1,1,8192,8108,4294967295,17,2147483648,2,65408}{t.Fatal(stats,err)}
 want:=[]sqltype.HashPageItem{{Offset:1,TID:sqltype.BtreeTID{Block:65538,Offset:513},Data:4294967295},{Offset:2,TID:sqltype.BtreeTID{Block:9,Offset:7},Data:2147483648}}
 items,err:=page.Items(context.Background());if err!=nil||!reflect.DeepEqual(items,want){t.Fatal(items,err)};items[0].Data=0
 again,err:=page.Items(context.Background());if err!=nil||!reflect.DeepEqual(again,want){t.Fatal("mutable output retained",again,err)}
 for _,low:=range []byte{0,255}{raw:=hashUnitItems();raw[18]=low;s,err:=hashUnitRead(t,string(raw),sqltype.HashBucketPage).Stats();if err!=nil||s!=stats{t.Fatal(s,err)}}
 raw=hashUnitItems();copy(raw[4080:],raw[8176:]);binary.LittleEndian.PutUint16(raw[16:],4080);binary.LittleEndian.PutUint16(raw[18:],0x1004);s,err:=hashUnitRead(t,string(raw),sqltype.HashBucketPage).Stats();if err!=nil||s[2]!=4096{t.Fatal(s,err)}
 for _,flags:=range []uint32{0,1,2,3}{raw:=hashUnitItems();binary.LittleEndian.PutUint32(raw[24:],8160|flags<<15);p:=hashUnitRead(t,string(raw),sqltype.HashBucketPage);rows,err:=p.Items(context.Background());if err!=nil||!reflect.DeepEqual(rows,want){t.Fatal("lp_len/flag gate invented",rows,err)};s,err:=p.Stats();dead:=int64(1);if flags==3{dead=2};if err!=nil||s[1]!=dead{t.Fatal(s,err)}}
 raw=hashUnitItems();binary.LittleEndian.PutUint16(raw[8166:],0x8018);binary.LittleEndian.PutUint32(raw[8168:],123);binary.LittleEndian.PutUint32(raw[8176:],42)
 rows,err:=hashUnitRead(t,string(raw),sqltype.HashBucketPage).Items(context.Background());if err!=nil||rows[0].Data!=42{t.Fatal("NULL data offset",rows,err)}
 for _,bits:=range []uint64{0x8000000000000000,math.Float64bits(12.5),0x7ff0000000000000,0xfff0000000000000,0x7ff8000000000001}{
  raw:=hashUnitPage(8);binary.LittleEndian.PutUint64(raw[32:],bits)
  for i,v:=range []uint16{65535,8192,4096,15}{binary.LittleEndian.PutUint16(raw[40+i*2:],v)}
  for i,v:=range []uint32{0xffffffff,0x80000000,17,3,0xffffffff,1,4000000000}{binary.LittleEndian.PutUint32(raw[48+i*4:],v)}
  for i:=range 98{binary.LittleEndian.PutUint32(raw[76+i*4:],uint32(i)*40000000)};for i:=range 1024{binary.LittleEndian.PutUint32(raw[468+i*4:],uint32(i)*4000000)}
  p:=hashUnitRead(t,string(raw),sqltype.HashMetaPage);if math.Float64bits(p.MetaTuples())!=bits{t.Fatal("float bits",bits,p.MetaTuples())}
  for i,v:=range []int64{105121344,4,0,65535,8192,4096,15,4294967295,2147483648,17,3,4294967295,1,4000000000}{if i!=2&&p.MetaInteger(i)!=v{t.Fatal(i,p.MetaInteger(i),v)}}
  for array,n:=range []int{98,1024}{for i:=range n{multiplier:=uint32(40000000);if array==1{multiplier=4000000};if p.MetaArrayElement(array,i)!=uint32(i)*multiplier{t.Fatal("full metadata array",array,i)}}}
 }
 poisoned:=hashUnitPage(15);binary.LittleEndian.PutUint16(poisoned[14:],0);binary.LittleEndian.PutUint16(poisoned[16:],65535);if hashUnitRead(t,string(poisoned),sqltype.HashAnyPage).PageType()!="unused"{t.Fatal("new-page opaque inspected")}
}
func TestHashPageSafety(t *testing.T){
 cases:=[]func()[]byte{
  func()[]byte{p:=hashUnitPage(2);binary.LittleEndian.PutUint16(p[16:],65264);binary.LittleEndian.PutUint16(p[18:],0xff04);return p},
  func()[]byte{p:=hashUnitPage(2);binary.LittleEndian.PutUint16(p[12:],65535);return p},
 }
 for _,offset:=range []uint32{8181,8185,8190,32767}{cases=append(cases,func()[]byte{p:=hashUnitItems();binary.LittleEndian.PutUint32(p[24:],offset);return p})}
 cases=append(cases,func()[]byte{p:=hashUnitItems();binary.LittleEndian.PutUint32(p[24:],8180);binary.LittleEndian.PutUint16(p[8186:],0x8000);return p})
 good:=hashUnitRead(t,string(hashUnitItems()),sqltype.HashBucketPage)
 for i,build:=range cases{raw:=string(build());p,err:=sqltype.ReadHashPage(raw,sqltype.HashBucketPage);if err==nil{_,err=p.Items(context.Background())};if err==nil||err.Error()!="stuntgres: unsafe PostgreSQL native datum layout"{t.Fatal(i,err)};var state interface{SQLState()string};if errors.As(err,&state){t.Fatal("invented SQLSTATE",err)};rows,err:=good.Items(context.Background());if err!=nil||len(rows)!=2||rows[0].Data!=4294967295{t.Fatal("failed layout contaminated page",rows,err)}}
 // Stats must not validate tuple locations it never dereferences.
 raw:=hashUnitItems();binary.LittleEndian.PutUint32(raw[24:],32767|1<<15);p:=hashUnitRead(t,string(raw),sqltype.HashBucketPage);if _,err:=p.Stats();err!=nil{t.Fatal(err)}
}
func TestHashPageCancellation(t *testing.T){
 p:=hashUnitRead(t,string(hashUnitItems()),sqltype.HashBucketPage);ctx,cancel:=context.WithCancel(context.Background());cancel();if rows,err:=p.Items(ctx);!errors.Is(err,context.Canceled)||rows!=nil{t.Fatal(rows,err)}
 for _,at:=range []int{2,3}{ctx,cancel:=context.WithCancel(context.Background());probe:=&ginCancelContext{Context:ctx,cancel:cancel,at:at};rows,err:=p.Items(probe);cancel();if !errors.Is(err,context.Canceled)||rows!=nil{t.Fatal("partial cancellation",at,rows,err)}}
 if rows,err:=p.Items(context.Background());err!=nil||len(rows)!=2{t.Fatal(rows,err)}
}
