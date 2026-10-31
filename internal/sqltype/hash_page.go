package sqltype

import (
 "context"
 "fmt"
 "math"
 "stuntgres/internal/sqlstate"
)

// HashPageKind selects validation, never physical page acquisition.
type HashPageKind uint8
const (
 HashAnyPage HashPageKind = iota
 HashBucketPage
 HashMetaPage
)
// HashPage borrows immutable bytea backing; collections returned by Items are owned.
type HashPage struct { raw string; kind HashPageKind; typ,flags,id uint16; lower,upper,size int; prev,next,bucket uint32 }
type HashPageItem struct { Offset int32; TID BtreeTID; Data uint32 }
func ReadHashPage(raw string,kind HashPageKind)(HashPage,error) {
 if kind>HashMetaPage{return HashPage{},nativeLayoutError()}
 fail:=func(code,message,detail string)(HashPage,error){return HashPage{},&sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:code,Message:message,Detail:detail}}
 if len(raw)!=8192{return fail("22023","invalid page size",fmt.Sprintf("Expected 8192 bytes, got %d.",len(raw)))}
 p:=HashPage{raw:raw,kind:kind,lower:int(native16(raw[12:])),upper:int(native16(raw[14:])),size:int(native16(raw[18:])&0xff00)}
 if p.upper!=0 {
  special:=int(native16(raw[16:]));size:=uint16(uint16(p.size)-uint16(special))
  if size!=16{return fail("22023","input page is not a valid hash page",fmt.Sprintf("Expected special size 16, got %d.",size))}
  if special>len(raw)-16{return HashPage{},nativeLayoutError()}
  opaque:=raw[special:];p.prev=native32(opaque);p.next=native32(opaque[4:]);p.bucket=native32(opaque[8:]);p.flags=native16(opaque[12:]);p.id=native16(opaque[14:]);p.typ=p.flags&15
  if p.id!=0xff80{return fail("22023","input page is not a valid hash page",fmt.Sprintf("Expected 0000ff80, got %08x.",p.id))}
  switch p.typ{case 0,1,2,4,8:default:return fail("22023",fmt.Sprintf("invalid hash page type %08x",p.typ),"")}
 }
 if kind==HashBucketPage && p.typ!=1 && p.typ!=2{return fail("22023","page is not a hash bucket or overflow page","")}
 if kind==HashMetaPage && p.typ!=8{return fail("22023","page is not a hash meta page","")}
 if p.typ==8 {
  if magic:=native32(raw[24:]);magic!=0x06440640{return fail("XX002","invalid magic number for metadata",fmt.Sprintf("Expected 0x06440640, got 0x%08x.",magic))}
  if version:=native32(raw[28:]);version!=4{return fail("XX002","invalid version for metadata",fmt.Sprintf("Expected 4, got %d.",version))}
 }
 return p,nil
}
func(p HashPage)PageType()string{switch p.typ{case 1:return "overflow";case 2:return "bucket";case 4:return "bitmap";case 8:return "metapage"};return "unused"}
func(p HashPage)itemCount()(int,error){n:=0;if p.lower>=24{n=(p.lower-24)/4};if n>(len(p.raw)-24)/4{return 0,nativeLayoutError()};return n,nil}
func(p HashPage)Stats()([9]int64,error){
 var out [9]int64
 if p.kind!=HashBucketPage{return out,nativeLayoutError()}
 n,err:=p.itemCount();if err!=nil{return out,err}
 for i:=range n{if (native32(p.raw[24+i*4:])>>15)&3==3{out[1]++}else{out[0]++}}
 free:=p.upper-p.lower-4;if free<0{free=0}
 out[2]=int64(p.size);out[3]=int64(free);out[4]=int64(p.prev);out[5]=int64(p.next);out[6]=int64(p.bucket);out[7]=int64(p.flags);out[8]=int64(p.id)
 return out,nil
}
func(p HashPage)Items(ctx context.Context)([]HashPageItem,error){
 if err:=ctx.Err();err!=nil{return nil,err};if p.kind!=HashBucketPage{return nil,nativeLayoutError()}
 n,err:=p.itemCount();if err!=nil{return nil,err};items:=make([]HashPageItem,n)
 for i:=range n{
  if err:=ctx.Err();err!=nil{return nil,err}
  off:=int(native32(p.raw[24+i*4:])&0x7fff);if off>len(p.raw)-8{return nil,nativeLayoutError()}
  tuple:=p.raw[off:];at:=8;if native16(tuple[6:])&0x8000!=0{at=16};if len(tuple)<at+4{return nil,nativeLayoutError()}
  items[i]=HashPageItem{Offset:int32(i+1),TID:btreeTID(tuple),Data:native32(tuple[at:])}
 }
 return items,nil
}
// MetaInteger exposes fixed unsigned fields without a duplicate metadata buffer.
func(p HashPage)MetaInteger(index int)int64{
 switch {case index<2:return int64(native32(p.raw[24+index*4:]));case index>=3 && index<=6:return int64(native16(p.raw[40+(index-3)*2:]));case index>=7 && index<=13:return int64(native32(p.raw[48+(index-7)*4:]))};return 0
}
func(p HashPage)MetaTuples()float64{return math.Float64frombits(native64(p.raw[32:]))}
func(p HashPage)MetaArrayElement(array,index int)uint32{at:=76;if array==1{at=468};return native32(p.raw[at+index*4:])}
