package sqltype

import (
 "context"
 "fmt"
 "strings"
 "stuntgres/internal/sqlstate"
)

// BrinStoredColumn captures the actual admitted opclass's stored descriptor.
// Its private slice cannot be mutated by callers after publication.
type BrinStoredColumn struct { stored []Usage }
func BindBrinStoredColumn(input Usage,c IndexClass,storage *Descriptor) (BrinStoredColumn,error) {
 if _,err:=BindBrinComparison(input,c);err!=nil { return BrinStoredColumn{},err }
 if storage==nil || !storage.Concrete() || uint32(storage.ID())!=c.StorageOID(){return BrinStoredColumn{},fmt.Errorf("stuntgres: invalid BRIN stored descriptor")}
 // ConstructTupleDescriptor applies opckeytype before brin_minmax_opcinfo,
 // which declares two values of that actual index storage type.
 stored:=storage.Usage()
 return BrinStoredColumn{stored:[]Usage{stored,stored}},nil
}

// BrinPage is a checked immutable view of caller-supplied physical bytes.
type BrinPage struct { raw string; new bool; tag uint16 }
func ReadBrinPage(raw string,required uint16) (BrinPage,error) {
 fail:=func(message,detail string)error{return &sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"22023",Message:message,Detail:detail}}
 if len(raw)!=8192 { return BrinPage{},fail("invalid page size",fmt.Sprintf("Expected 8192 bytes, got %d.",len(raw))) }
 p:=BrinPage{raw:raw,new:native16(raw[14:])==0}
 if p.new { return p,nil }
 special:=native16(raw[16:]);size:=uint16((native16(raw[18:])&0xff00)-special)
 if size!=8 { return BrinPage{},fail("input page is not a valid BRIN page",fmt.Sprintf("Expected special size 8, got %d.",size)) }
 // PageGetSpecialPointer is header-relative, not hardcoded to block end.
 if int(special)>len(raw)-8 { return BrinPage{},nativeLayoutError() }
 p.tag=native16(raw[int(special)+6:])
 if required!=0 && p.tag!=required {
  label:=map[uint16]string{0xf091:"metapage",0xf092:"revmap",0xf093:"regular"}[required]
  return BrinPage{},fail(fmt.Sprintf("page is not a BRIN page of type %q",label),fmt.Sprintf("Expected special type %08x, got %08x.",required,p.tag))
 }
 return p,nil
}
func (p BrinPage) IsNew()bool{return p.new}
func (p BrinPage) PageType()string {
 switch p.tag { case 0xf091:return "meta";case 0xf092:return "revmap";case 0xf093:return "regular" };return fmt.Sprintf("unknown (%02x)",p.tag)
}
func (p BrinPage) Meta() (string,int32,int32,int64) {
 return fmt.Sprintf("0x%08X",native32(p.raw[24:])),int32(native32(p.raw[28:])),int32(native32(p.raw[32:])),int64(native32(p.raw[36:]))
}
func (p BrinPage) Revmap(d *Descriptor) ([]Value,error) {
 if p.new { return []Value{Null(d)},nil }
 out:=make([]Value,1360)
 for i:=range out { at:=24+i*6;v,err:=TIDValue(d,uint32(native16(p.raw[at:]))<<16|uint32(native16(p.raw[at+2:])),native16(p.raw[at+4:]));if err!=nil{return nil,err};out[i]=v }
 return out,nil
}

type BrinPageItem struct {
 Offset int32
 Unused bool
 Block int64
 Attribute int32
 AllNulls,HasNulls,Placeholder,Empty bool
 Value string
}
func (p BrinPage) Items(ctx context.Context,columns []BrinStoredColumn,env OutputEnvironment)([]BrinPageItem,error){
 if p.new { return nil,nil }
 lower:=int(native16(p.raw[12:]));count:=0
 if lower>24 { count=(lower-24)/4 };if count==0 { count=1 }
 if count>(len(p.raw)-24)/4 { return nil,nativeLayoutError() }
 out:=make([]BrinPageItem,0,count*len(columns))
 for slot:=range count {
  if err:=ctx.Err();err!=nil{return nil,err}
  word:=native32(p.raw[24+slot*4:]);base:=BrinPageItem{Offset:int32(slot+1)}
  if word>>15&3==0 { base.Unused=true;out=append(out,base);continue }
  at:=int(word&0x7fff)
  // lp_len is not consulted by the released deformer. Bound against the raw
  // allocation, preserving defined reads even when that length is mutated.
  if at>len(p.raw)-5 { return nil,nativeLayoutError() };tuple:=p.raw[at:]
  info:=tuple[4];base.Block=int64(native32(tuple));base.Placeholder=info&0x40!=0;base.Empty=info&0x20!=0
  dataOffset:=int(info&0x1f);if dataOffset>len(tuple){return nil,nativeLayoutError()}
  data,position:=tuple[dataOffset:],0;natts:=len(columns)
  bitmap:=func(bit int)(bool,error){ byteAt:=5+bit/8;if byteAt>=len(tuple){return false,nativeLayoutError()};return tuple[byteAt]&(1<<uint(bit%8))!=0,nil }
  for col,descriptor:=range columns {
   row:=base;row.Attribute=int32(col+1)
   if info&0x80!=0 {
    var err error;row.AllNulls,err=bitmap(col);if err!=nil{return nil,err}
    if !row.AllNulls {row.HasNulls,err=bitmap(col+natts);if err!=nil{return nil,err}}
   }
   if !row.AllNulls {
    var b strings.Builder;b.WriteByte('{')
    for i,u:=range descriptor.stored {
     v,next,err:=NativeDatum(data,position,u);if err!=nil{return nil,err};position=next
     text,err:=u.Type.OutputWithEnvironment(ctx,v,env);if err!=nil{return nil,err}
     if end:=strings.IndexByte(text,0);end>=0{text=text[:end]}
     if i!=0 {b.WriteString(" .. ")};b.WriteString(text)
    }
    b.WriteByte('}');row.Value=b.String()
   }
   out=append(out,row)
  }
 }
 return out,nil
}
