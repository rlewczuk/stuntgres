package enginetests

import (
 "context"
 "encoding/binary"
 "errors"
 "reflect"
 "testing"

 "stuntgres/internal/sqltype"
)

func ginUnitPage(flags uint16) []byte {
 p:=make([]byte,8192)
 binary.LittleEndian.PutUint16(p[12:],32);binary.LittleEndian.PutUint16(p[14:],8184)
 binary.LittleEndian.PutUint16(p[16:],8184);binary.LittleEndian.PutUint16(p[18:],0x2004)
 binary.LittleEndian.PutUint32(p[8184:],0xffffffff);binary.LittleEndian.PutUint16(p[8188:],65535);binary.LittleEndian.PutUint16(p[8190:],flags)
 return p
}
func ginUnitRead(t *testing.T,raw string,kind sqltype.GinPageKind) sqltype.GinPage {t.Helper();p,err:=sqltype.ReadGinPage(raw,kind);if err!=nil {t.Fatal(err)};return p}
func ginUnitLeaf(payload ...byte) []byte {
 p:=ginUnitPage(0x83);btUnitTID(p[32:],8,41)
 binary.LittleEndian.PutUint16(p[38:],uint16(len(payload)));copy(p[40:],payload)
 binary.LittleEndian.PutUint16(p[12:],uint16(40+(len(payload)+1)&^1))
 return p
}
func TestGINPageDecode(t *testing.T) {
 p:=ginUnitPage(8)
 for _,f:=range []struct{off int;value uint32}{{24,0xffffffff},{28,17},{32,0x80000000},{36,19},{48,29},{52,31},{56,37},{72,0x80000000}} {binary.LittleEndian.PutUint32(p[f.off:],f.value)}
 binary.LittleEndian.PutUint64(p[40:],0x8000000000000000);binary.LittleEndian.PutUint64(p[64:],0x7fffffffffffffff);copy(p[60:64],[]byte{0xde,0xad,0xbe,0xef})
 want:=[10]int64{4294967295,17,-2147483648,19,-9223372036854775808,29,31,37,9223372036854775807,-2147483648}
 page:=ginUnitRead(t,string(p),sqltype.GinMetapage);if page.Meta()!=want {t.Fatal(page.Meta(),want)}
 for _,low:=range []byte{0,255} {p[18]=low;if ginUnitRead(t,string(p),sqltype.GinMetapage).Meta()!=want {t.Fatal("page version validated")}}
 copy(p[4088:4096],p[8184:8192]);clear(p[8184:8192]);binary.LittleEndian.PutUint16(p[16:],4088);binary.LittleEndian.PutUint16(p[18:],0x1004)
 if ginUnitRead(t,string(p),sqltype.GinMetapage).Meta()!=want {t.Fatal("hardcoded special offset")}
 for _,tc:=range []struct{flags uint16;want []string}{
  {0,[]string{}},{0xffff,[]string{"data","leaf","deleted","meta","list","list_fullrow","incomplete_split","compressed","ff00"}},{0x8103,[]string{"data","leaf","8100"}},
 } {link,maxoff,flags:=ginUnitRead(t,string(ginUnitPage(tc.flags)),sqltype.GinOpaquePage).Opaque();if link!=4294967295 || maxoff!=65535 || !reflect.DeepEqual(flags,tc.want) {t.Fatal(link,maxoff,flags)}}
 raw:=ginUnitLeaf(2,1,127)
 // Poison odd alignment padding, then append a physically high/low block TID.
 raw[43]=0xee;btUnitTID(raw[44:],65538,513);binary.LittleEndian.PutUint16(raw[50:],2);copy(raw[52:],[]byte{0x80,0x0c});binary.LittleEndian.PutUint16(raw[12:],54)
 immutable:=string(raw);leaf:=ginUnitRead(t,immutable,sqltype.GinLeafPage);raw[40]=99
 rows,err:=leaf.LeafItems(context.Background())
 expected:=[]sqltype.GinLeafItem{
  {First:sqltype.BtreeTID{Block:8,Offset:41},NBytes:3,TIDs:[]sqltype.BtreeTID{{Block:8,Offset:41},{Block:8,Offset:43},{Block:8,Offset:44},{Block:8,Offset:171}}},
  {First:sqltype.BtreeTID{Block:65538,Offset:513},NBytes:2,TIDs:[]sqltype.BtreeTID{{Block:65538,Offset:513},{Block:65539,Offset:1}}},
 }
 if err!=nil || !reflect.DeepEqual(rows,expected) {t.Fatal(rows,err)}
 rows[0].TIDs[0].Block=999;rows[1].NBytes=99
 again,err:=leaf.LeafItems(context.Background());if err!=nil || !reflect.DeepEqual(again,expected) || immutable[40]!=2 {t.Fatal("decoder retained mutable backing",again,err)}
 seven:=ginUnitLeaf(0x80,0x80,0x80,0x80,0x80,0x80,1);btUnitTID(seven[32:],0,1)
 rows,err=ginUnitRead(t,string(seven),sqltype.GinLeafPage).LeafItems(context.Background())
 if err!=nil || len(rows)!=1 || !reflect.DeepEqual(rows[0].TIDs,[]sqltype.BtreeTID{{Block:0,Offset:1},{Block:2147483648,Offset:1}}) {t.Fatal(rows,err)}
 // These complete bounded payloads are positive released conversions, not
 // corruption errors. Literal rows independently mirror the oracle fixtures.
 for _,tc:=range []struct{name string;first sqltype.BtreeTID;payload []byte;want []sqltype.BtreeTID}{
  {"block_carry",sqltype.BtreeTID{Block:4294967294,Offset:2047},[]byte{0x82,0x10},[]sqltype.BtreeTID{{Block:4294967294,Offset:2047},{Block:0,Offset:1}}},
  {"tid_overflow",sqltype.BtreeTID{Block:4294967295,Offset:2047},[]byte{1},[]sqltype.BtreeTID{{Block:4294967295,Offset:2047},{Block:0,Offset:0}}},
  {"zero_delta",sqltype.BtreeTID{Block:8,Offset:41},[]byte{0},[]sqltype.BtreeTID{{Block:8,Offset:41},{Block:8,Offset:41}}},
  {"physical_offset",sqltype.BtreeTID{Block:0,Offset:65535},[]byte{1},[]sqltype.BtreeTID{{Block:0,Offset:65535},{Block:32,Offset:0}}},
  {"seventh_high_bits",sqltype.BtreeTID{Block:0,Offset:1},[]byte{0x80,0x80,0x80,0x80,0x80,0x80,0x7f},[]sqltype.BtreeTID{{Block:0,Offset:1},{Block:2147483648,Offset:1}}},
  {"seventh_continuation",sqltype.BtreeTID{Block:0,Offset:1},[]byte{0x80,0x80,0x80,0x80,0x80,0x80,0x80},[]sqltype.BtreeTID{{Block:0,Offset:1},{Block:0,Offset:1}}},
  {"next_delta",sqltype.BtreeTID{Block:0,Offset:1},[]byte{0x80,0x80,0x80,0x80,0x80,0x80,0x80,1},[]sqltype.BtreeTID{{Block:0,Offset:1},{Block:0,Offset:1},{Block:0,Offset:2}}},
  {"delta_overflow",sqltype.BtreeTID{Block:8,Offset:41},[]byte{0x80,0x80,0x80,0x80,0x80,0x80,0x80,0x80,0x80,2},[]sqltype.BtreeTID{{Block:8,Offset:41},{Block:8,Offset:41},{Block:24,Offset:41}}},
  {"overwide",sqltype.BtreeTID{Block:8,Offset:41},[]byte{0x80,0x80,0x80,0x80,0x80,0x80,0x80,0x80,0x80,0x80,0},[]sqltype.BtreeTID{{Block:8,Offset:41},{Block:8,Offset:41},{Block:8,Offset:41}}},
 } {t.Run(tc.name,func(t *testing.T) {
  raw:=ginUnitLeaf(tc.payload...);btUnitTID(raw[32:],tc.first.Block,tc.first.Offset)
  items,err:=ginUnitRead(t,string(raw),sqltype.GinLeafPage).LeafItems(context.Background())
  if err!=nil || len(items)!=1 || items[0].First!=tc.first || items[0].NBytes!=int16(len(tc.payload)) || !reflect.DeepEqual(items[0].TIDs,tc.want) {t.Fatal(items,err,tc.want)}
 })}
 zero:=ginUnitLeaf();btUnitTID(zero[32:],0,0);rows,err=ginUnitRead(t,string(zero),sqltype.GinLeafPage).LeafItems(context.Background());if err!=nil || len(rows)!=1 || rows[0].First.Offset!=0 || len(rows[0].TIDs)!=1 {t.Fatal(rows,err)}
 rows,err=ginUnitRead(t,string(ginUnitPage(0x83)),sqltype.GinLeafPage).LeafItems(context.Background());if err!=nil || len(rows)!=0 {t.Fatal(rows,err)}
 for _,kind:=range []sqltype.GinPageKind{sqltype.GinMetapage,sqltype.GinOpaquePage,sqltype.GinLeafPage} {p:=ginUnitPage(0xffff);binary.LittleEndian.PutUint16(p[14:],0);binary.LittleEndian.PutUint16(p[16:],65535);if !ginUnitRead(t,string(p),kind).IsNew() {t.Fatal("not new")}}
}
func TestGINPageSafety(t *testing.T) {
 cases:=[]struct{name string;raw func()[]byte}{
  {"special_outside",func()[]byte {p:=ginUnitLeaf();binary.LittleEndian.PutUint16(p[16:],65272);binary.LittleEndian.PutUint16(p[18:],0xff04);return p}},
  {"lower_before_segments",func()[]byte {p:=ginUnitLeaf();binary.LittleEndian.PutUint16(p[12:],31);return p}},
  {"lower_outside",func()[]byte {p:=ginUnitLeaf();binary.LittleEndian.PutUint16(p[12:],8193);return p}},
  {"truncated_header",func()[]byte {p:=ginUnitLeaf();binary.LittleEndian.PutUint16(p[12:],39);return p}},
  {"truncated_payload",func()[]byte {p:=ginUnitLeaf(1,2);binary.LittleEndian.PutUint16(p[12:],41);return p}},
  {"truncated_stride",func()[]byte {p:=ginUnitLeaf(1);binary.LittleEndian.PutUint16(p[12:],41);return p}},
  {"truncated_next_segment",func()[]byte {p:=ginUnitLeaf(1,2);binary.LittleEndian.PutUint16(p[12:],43);return p}},
  {"unterminated",func()[]byte {return ginUnitLeaf(0x80)}},
  {"missing_seventh",func()[]byte {return ginUnitLeaf(0x80,0x80,0x80,0x80,0x80,0x80)}},
  {"next_delta_missing_byte",func()[]byte {return ginUnitLeaf(0x80,0x80,0x80,0x80,0x80,0x80,0x80,0x80)}},
 }
 good:=ginUnitRead(t,string(ginUnitLeaf(2)),sqltype.GinLeafPage)
 for _,tc:=range cases {t.Run(tc.name,func(t *testing.T) {
  raw:=string(tc.raw());saved:=raw
  p,err:=sqltype.ReadGinPage(raw,sqltype.GinLeafPage);if err==nil {_,err=p.LeafItems(context.Background())}
  if err==nil || err.Error()!="stuntgres: unsafe PostgreSQL native datum layout" {t.Fatal("unsafe layout",err)}
  var state interface{SQLState()string};if errors.As(err,&state) {t.Fatal("invented SQLSTATE",err)}
  if raw!=saved {t.Fatal("failure changed input")}
  rows,err:=good.LeafItems(context.Background());if err!=nil || len(rows)!=1 || rows[0].TIDs[1]!=(sqltype.BtreeTID{Block:8,Offset:43}) {t.Fatal("failure contaminated another page",rows,err)}
 })}
}
type ginCancelContext struct {context.Context;cancel context.CancelFunc;calls,at int}
func(c *ginCancelContext)Err()error {c.calls++;if c.calls==c.at {c.cancel()};return c.Context.Err()}
func TestGINPageCancellation(t *testing.T) {
 p:=ginUnitLeaf();btUnitTID(p[40:],9,1);binary.LittleEndian.PutUint16(p[12:],48)
 page:=ginUnitRead(t,string(p),sqltype.GinLeafPage)
 ctx,cancel:=context.WithCancel(context.Background());cancel()
 if rows,err:=page.LeafItems(ctx);!errors.Is(err,context.Canceled) || rows!=nil {t.Fatal(rows,err)}
 for _,at:=range []int{2,3} {
  ctx,cancel:=context.WithCancel(context.Background());probe:=&ginCancelContext{Context:ctx,cancel:cancel,at:at}
  rows,err:=page.LeafItems(probe);cancel();if !errors.Is(err,context.Canceled) || rows!=nil {t.Fatal("segment cancellation",at,rows,err)}
 }
 rows,err:=page.LeafItems(context.Background());if err!=nil || len(rows)!=2 {t.Fatal("cancellation changed immutable page",rows,err)}
}
