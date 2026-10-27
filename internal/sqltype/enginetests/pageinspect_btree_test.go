package enginetests

import (
 "context"
 "encoding/binary"
 "errors"
 "reflect"
 "testing"
 "stuntgres/internal/sqltype"
)
func btUnitTID(p []byte,block uint32,offset uint16){binary.LittleEndian.PutUint16(p,uint16(block>>16));binary.LittleEndian.PutUint16(p[2:],uint16(block));binary.LittleEndian.PutUint16(p[4:],offset)}
func btUnitPage()[]byte{p:=make([]byte,8192);binary.LittleEndian.PutUint16(p[12:],28);binary.LittleEndian.PutUint16(p[14:],8100);binary.LittleEndian.PutUint16(p[16:],8176);binary.LittleEndian.PutUint16(p[18:],0x2004);binary.LittleEndian.PutUint16(p[8188:],1);binary.LittleEndian.PutUint32(p[24:],8100|1<<15|16<<17);btUnitTID(p[8100:],65538,513);binary.LittleEndian.PutUint16(p[8106:],16);copy(p[8108:],[]byte{249,255,255,255,0,0,0,0});return p}
func btUnitRead(t *testing.T,raw []byte)sqltype.BtreePage{t.Helper();p,err:=sqltype.ReadBtreePage(string(raw));if err!=nil{t.Fatal(err)};return p}
func TestPageinspectBtreePageHeaders(t *testing.T){
 for _,n:=range []int{0,8191,8193}{if _,err:=sqltype.ReadBtreePage(string(make([]byte,n)));err==nil{t.Fatal(n)}}
 p:=btUnitPage();copy(p[4080:],p[8176:]);binary.LittleEndian.PutUint16(p[16:],4080);binary.LittleEndian.PutUint16(p[18:],0x10ff)
 page:=btUnitRead(t,p);items,err:=page.Items(context.Background());if err!=nil || len(items)!=1{t.Fatal(items,err)}
 for _,lower:=range []uint16{0,23,24,25,27,28,31}{raw:=btUnitPage();binary.LittleEndian.PutUint16(raw[12:],lower);rows,err:=btUnitRead(t,raw).Items(context.Background());want:=0;if lower>=28{want=1};if err!=nil || len(rows)!=want{t.Fatal(lower,rows,err)}}
 for _,flags:=range []uint16{4,5,16,17}{raw:=btUnitPage();binary.LittleEndian.PutUint16(raw[8188:],flags);page:=btUnitRead(t,raw);if page.IsDeleted()!=(flags&4!=0){t.Fatal(flags)}}
 raw:=btUnitPage();binary.LittleEndian.PutUint16(raw[14:],0);binary.LittleEndian.PutUint16(raw[16:],65535);page=btUnitRead(t,raw);if !page.IsNew(){t.Fatal("not new")}
}
func TestPageinspectBtreeTuples(t *testing.T){
 raw:=btUnitPage();page:=btUnitRead(t,raw);raw[8108]=0
 rows,err:=page.Items(context.Background());want:=sqltype.BtreePageItem{Offset:1,Length:16,CTID:sqltype.BtreeTID{Block:65538,Offset:513},HeapTID:sqltype.BtreeTID{Block:65538,Offset:513},Data:"f9 ff ff ff 00 00 00 00"}
 if err!=nil || len(rows)!=1 || !reflect.DeepEqual(rows[0],want){t.Fatal(rows,err)}
 rows[0].Data="mutated";again,err:=page.Items(context.Background());if err!=nil || again[0].Data!=want.Data{t.Fatal(again,err)}
 for _,flags:=range []uint32{0,1,2,3}{raw:=btUnitPage();binary.LittleEndian.PutUint32(raw[24:],8100|flags<<15|1<<17);rows,err:=btUnitRead(t,raw).Items(context.Background());if err!=nil || rows[0].Length!=16 || rows[0].Dead!=(flags==3){t.Fatal(rows,err)}}
 for _,contextual:=range []bool{false,true}{raw:=btUnitPage();if contextual{binary.LittleEndian.PutUint32(raw[8180:],9)};rows,err:=btUnitRead(t,raw).Items(context.Background());if err!=nil || rows[0].DeadNull!=contextual || rows[0].HeapNull!=contextual{t.Fatal(rows,err)}}
 for _,tc:=range []struct{info uint16;offset uint16;size int;want string;heapNull bool}{{0x2008,0,8,"",true},{0x2010,1,16,"2a 00 00 00 00 00 00 00",true},{0x2018,4097,24,"2a 00 00 00 00 00 00 00",false},{0xc018,513,24,"2a 00 00 00 00 00 00 00",false}}{
  raw:=btUnitPage();binary.LittleEndian.PutUint16(raw[8104:],tc.offset);binary.LittleEndian.PutUint16(raw[8106:],tc.info);start:=8108;if tc.info&0x8000!=0{start=8116};copy(raw[start:],[]byte{42,0,0,0,0,0,0,0});if tc.offset==4097{btUnitTID(raw[8100+tc.size-6:],1,6)};rows,err:=btUnitRead(t,raw).Items(context.Background());if err!=nil || rows[0].Data!=tc.want || rows[0].HeapNull!=tc.heapNull{t.Fatal(tc,rows,err)}
 }
}
func TestPageinspectBtreeBounds(t *testing.T){
 cases:=[]func([]byte){
  func(p []byte){binary.LittleEndian.PutUint16(p[16:],65264);binary.LittleEndian.PutUint16(p[18:],0xff04)},
  func(p []byte){binary.LittleEndian.PutUint16(p[12:],65535)},
  func(p []byte){binary.LittleEndian.PutUint32(p[24:],32767)},
  func(p []byte){binary.LittleEndian.PutUint32(p[24:],8188)},
  func(p []byte){binary.LittleEndian.PutUint16(p[8106:],8191)},
  func(p []byte){binary.LittleEndian.PutUint16(p[8106:],0x3fff);binary.LittleEndian.PutUint16(p[8104:],4097)},
  func(p []byte){btUnitTID(p[8100:],16,0x2fff);binary.LittleEndian.PutUint16(p[8106:],0x2020)},
  func(p []byte){btUnitTID(p[8100:],92,0x2000);binary.LittleEndian.PutUint16(p[8106:],0x2020)},
 }
 for i,mutate:=range cases{p:=btUnitPage();mutate(p);page,err:=sqltype.ReadBtreePage(string(p));if err==nil{_,err=page.Items(context.Background())};if err==nil{t.Fatal("unsafe layout accepted",i)};var state interface{SQLState()string};if errors.As(err,&state){t.Fatal("invented corruption SQLSTATE",i,err)}}
}
func TestPageinspectBtreeArrays(t *testing.T){
 raw:=btUnitPage();btUnitTID(raw[8100:],16,8194);binary.LittleEndian.PutUint16(raw[8106:],0x2020);copy(raw[8108:],[]byte{42,0,0,0,0,0,0,0});btUnitTID(raw[8116:],1,6);btUnitTID(raw[8122:],65538,513)
 page:=btUnitRead(t,raw);rows,err:=page.Items(context.Background());if err!=nil || len(rows)!=1 || !reflect.DeepEqual(rows[0].Posting,[]sqltype.BtreeTID{{Block:1,Offset:6},{Block:65538,Offset:513}}){t.Fatal(rows,err)}
 rows[0].Posting[0].Block=99;raw[8116]=99;again,err:=page.Items(context.Background());if err!=nil || again[0].Posting[0].Block!=1{t.Fatal("posting ownership",again,err)}
 types:=brinUnitTypes();d:=types[1010];if !d.Concrete() || d.Element()!=types[sqltype.Tid]{t.Fatal(d)}
 one,err:=sqltype.TIDValue(d.Element(),1,6);if err!=nil{t.Fatal(err)};elements:=[]sqltype.Value{one,sqltype.Null(d.Element())};dims:=[]sqltype.ArrayDimension{{Lower:1,Length:2}}
 value,err:=sqltype.ArrayValue(d,dims,elements);if err!=nil{t.Fatal(err)};dims[0].Lower=8;elements[0]=sqltype.Null(d.Element());export:=value.ArrayDimensions();export[0].Lower=9
 text,err:=d.Output(context.Background(),value);if err!=nil || text!=`{"(1,6)",NULL}` || value.ArrayDimensions()[0].Lower!=1{t.Fatal(text,err)}
 parsed,err:=d.Input(context.Background(),text,-1);if err!=nil || parsed.ArrayLen()!=2 || !parsed.ArrayElement(1).IsNull(){t.Fatal(parsed,err)}
 for _,index:=range []int32{0,1,2,3}{v,err:=sqltype.ArrayFetch(context.Background(),value,[]sqltype.ArraySubscriptValue{{Upper:index,HasUpper:true}});if err!=nil || v.IsNull()!=(index!=1){t.Fatal(index,v,err)};if index==1{block,offset,err:=v.TID();if err!=nil || block!=1 || offset!=6{t.Fatal(v,err)}}}
 slice,err:=sqltype.ArraySlice(context.Background(),value,[]sqltype.ArraySubscriptValue{{Lower:1,Upper:1,HasLower:true,HasUpper:true}});if err!=nil{t.Fatal(err)};sliceText,err:=d.Output(context.Background(),slice);if err!=nil || sliceText!=`{"(1,6)"}` || slice.ArrayDimension(0)!=(sqltype.ArrayDimension{Lower:1,Length:1}){t.Fatal(sliceText,err)}
 owned,err:=sqltype.OwnedArrayValue(d,[]sqltype.ArrayDimension{{Lower:1,Length:1}},[]sqltype.Value{one});if err!=nil || !reflect.DeepEqual(owned.ArrayElement(0),one){t.Fatal(owned,err)}
 record,err:=sqltype.RecordShape([]sqltype.CompositeMember{{Attribute:1,Name:"tids",Usage:d.Usage()}});if err!=nil{t.Fatal(err)};fields:=[]sqltype.Value{value};composite,err:=sqltype.CompositeValue(record,fields);if err!=nil{t.Fatal(err)};fields[0]=sqltype.Null(d);if composite.CompositeField(0).IsNull(){t.Fatal("record retained mutable fields")}
 empty,err:=d.Input(context.Background(),"{}",-1);if err!=nil || empty.IsNull() || empty.ArrayLen()!=0 || !sqltype.Null(d).IsNull(){t.Fatal(empty,err)}
}
func TestPageinspectBtreeCancellation(t *testing.T){ctx,cancel:=context.WithCancel(context.Background());cancel();if _,err:=btUnitRead(t,btUnitPage()).Items(ctx);!errors.Is(err,context.Canceled){t.Fatal(err)}}
