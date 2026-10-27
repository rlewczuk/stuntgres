package sqltype

import (
 "context"
 "fmt"
 "strings"
 "stuntgres/internal/sqlstate"
)

// BtreePage retains only immutable caller-supplied bytes, never a relation page.
type BtreePage struct { raw string; new,deleted,leaf,rightmost bool }
type BtreeTID struct { Block uint32; Offset uint16 }
type BtreePageItem struct {
 Offset,Length int32
 CTID,HeapTID BtreeTID
 Nulls,Vars,Dead,DeadNull,HeapNull bool
 Data string
 Posting []BtreeTID
}
func ReadBtreePage(raw string)(BtreePage,error){
 fail:=func(message,detail string)error{return &sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"22023",Message:message,Detail:detail}}
 if len(raw)!=8192{return BtreePage{},fail("invalid page size",fmt.Sprintf("Expected 8192 bytes, got %d.",len(raw)))}
 p:=BtreePage{raw:raw,new:native16(raw[14:])==0};if p.new{return p,nil}
 special:=int(native16(raw[16:]));size:=uint16((native16(raw[18:])&0xff00)-uint16(special))
 if size!=16{return BtreePage{},fail("input page is not a valid btree page",fmt.Sprintf("Expected special size 16, got %d.",size))}
 if special>len(raw)-16{return BtreePage{},nativeLayoutError()}
 opaque:=raw[special:];flags:=native16(opaque[12:])
 if flags&8!=0{return BtreePage{},fail("block is a meta page","")}
 p.leaf=flags&1!=0;p.rightmost=native32(opaque[4:])==0
 if p.leaf && native32(opaque[8:])!=0{return BtreePage{},fail("block is not a valid btree leaf page","")}
 p.deleted=flags&4!=0;return p,nil
}
func(p BtreePage)IsNew()bool{return p.new}
func(p BtreePage)IsDeleted()bool{return p.deleted}
func btreeTID(raw string)BtreeTID{return BtreeTID{uint32(native16(raw))<<16|uint32(native16(raw[2:])),native16(raw[4:])}}
func(p BtreePage)Items(ctx context.Context)([]BtreePageItem,error){
 if err:=ctx.Err();err!=nil{return nil,err}
 if p.new || p.deleted{return nil,nil}
 count:=0;if lower:=int(native16(p.raw[12:]));lower>24{count=(lower-24)/4}
 if count>(len(p.raw)-24)/4{return nil,nativeLayoutError()}
 out:=make([]BtreePageItem,0,count)
 for slot:=range count {
  if err:=ctx.Err();err!=nil{return nil,err}
  word:=native32(p.raw[24+slot*4:]);at:=int(word&0x7fff)
  if at>len(p.raw)-8{return nil,nativeLayoutError()};tuple:=p.raw[at:]
  info:=native16(tuple[6:]);size:=int(info&0x1fff);tid:=btreeTID(tuple)
  item:=BtreePageItem{Offset:int32(slot+1),Length:int32(size),CTID:tid,Nulls:info&0x8000!=0,Vars:info&0x4000!=0,HeapTID:tid}
  item.DeadNull=!p.leaf || (!p.rightmost && slot==0);item.Dead=word>>15&3==3
  start:=8;if item.Nulls{start=16};end:=size
  alternate:=info&0x2000!=0;posting:=alternate && tid.Offset&0x2000!=0
  trailer:=alternate && !posting && tid.Offset&0x1000!=0
  if posting{end=int(tid.Block)}else if trailer{end-=8}
  length:=end-start
  if length<0 || length>8191{return nil,&sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"XX000",Message:fmt.Sprintf("invalid tuple length %d for tuple at offset number %d",length,slot+1)}}
  if start>len(tuple) || end>len(tuple){return nil,nativeLayoutError()}
  if length!=0 {
   const hex="0123456789abcdef";var data strings.Builder;data.Grow(length*3-1)
   for i:=range length{if i!=0{data.WriteByte(' ')};v:=tuple[start+i];data.WriteByte(hex[v>>4]);data.WriteByte(hex[v&15])}
   item.Data=data.String()
  }
  switch {
  case posting:
   n:=int(tid.Offset&0x0fff)
   // The first TID is read for htid even when the encoded count is zero.
   if end>len(tuple)-6 || n>(len(tuple)-end)/6{return nil,nativeLayoutError()}
   item.HeapTID=btreeTID(tuple[end:]);item.Posting=make([]BtreeTID,n)
   for i:=range n{item.Posting[i]=btreeTID(tuple[end+i*6:])}
  case trailer:
   if size<6 || size>len(tuple){return nil,nativeLayoutError()};item.HeapTID=btreeTID(tuple[size-6:])
  default:item.HeapNull=alternate || item.DeadNull
  }
  out=append(out,item)
 }
 return out,nil
}
