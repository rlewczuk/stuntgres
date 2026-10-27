package xml

import (
 "context"
 "fmt"
 "strings"
 "stuntgres/internal/sqltype/pgencoding18"
)

type EncodingErrorKind uint8
const ( InvalidEncodingName EncodingErrorKind = iota; MissingConversion; InvalidSequence; UntranslatableCharacter )
type EncodingError struct {
 Kind EncodingErrorKind
 Label string
 SourceID, DestinationID int
 SourceName, DestinationName, Bytes string
 Message, Detail string
}
func (e *EncodingError) Error() string {return e.Message}
type SizeErrorKind uint8
const ( ConversionSize SizeErrorKind = iota; AppendSize )
type SizeError struct {Kind SizeErrorKind; Message, Detail string; SourceLength, CurrentLength, AdditionalLength int}
func (e *SizeError) Error() string {return e.Message}
func conversionInputSize(n int) bool {return n>=0&&uint(n)<(^uint(0)>>1)/4}
func conversionSizeError(n int) error {return &SizeError{Kind:ConversionSize,SourceLength:n,Message:"out of memory",Detail:fmt.Sprintf("String of %d bytes is too long for encoding conversion.",n)}}
func appendSize(current,needed int) bool {return current>=0&&needed>=0&&current<0x3fffffff&&needed<0x3fffffff-current}
func appendSizeError(current,needed int) error {return &SizeError{Kind:AppendSize,CurrentLength:current,AdditionalLength:needed,Message:"string buffer exceeds maximum allowed length (1073741823 bytes)",Detail:fmt.Sprintf("Cannot enlarge string buffer containing %d bytes by %d more bytes.",current,needed)}}
func encodingID(name string) int {return pgencoding18.ID(name)}
func encodingFailure(kind EncodingErrorKind,src,dst int,raw string) error {
 e:=&EncodingError{Kind:kind,SourceID:src,DestinationID:dst,SourceName:pgencoding18.Name(src),DestinationName:pgencoding18.Name(dst)}
 if raw!="" {
  n:=min(pgencoding18.Length(src,raw),len(raw),8)
  var b strings.Builder
  for i:=range n {if i!=0 {b.WriteByte(' ')};fmt.Fprintf(&b,"0x%02x",raw[i])};e.Bytes=b.String()
 }
 switch kind {
 case MissingConversion:e.Message=fmt.Sprintf("default conversion function for encoding %q to %q does not exist",e.SourceName,e.DestinationName)
 case InvalidSequence:e.Message=fmt.Sprintf("invalid byte sequence for encoding %q: %s",e.SourceName,e.Bytes)
 case UntranslatableCharacter:e.Message=fmt.Sprintf("character with byte sequence %s in encoding %q has no equivalent in encoding %q",e.Bytes,e.SourceName,e.DestinationName)
 }
 return e
}
func convert(ctx context.Context,raw string,src,dst int)(string,error) {
 if err:=ctx.Err();err!=nil{return "",err}
 if raw==""||src==dst||src==0||dst==0 {return raw,ctx.Err()}
 return convertParts(ctx,[]string{raw},src,dst)
}
func convertParts(ctx context.Context,parts []string,src,dst int)(string,error) {
 if err:=ctx.Err();err!=nil{return "",err}
 if !pgencoding18.HasPair(src,dst) {return "",encodingFailure(MissingConversion,src,dst,"")}
 total:=0
 for _,raw:=range parts {
  if len(raw)>int(^uint(0)>>1)-total {return "",conversionSizeError(len(raw))};total+=len(raw)
 }
 if !conversionInputSize(total) {return "",conversionSizeError(total)}
 // Kernels can inspect the following character for a combined mapping. Verify
 // the whole source before any Step, not merely the current character.
 for _,raw:=range parts {
  next:=0
  for i:=0;i<len(raw); {
   if i>=next {if err:=ctx.Err();err!=nil{return "",err};next=i+4096}
   n:=pgencoding18.Verify(src,raw[i:]);if n<0{return "",encodingFailure(InvalidSequence,src,dst,raw[i:])};i+=n
  }
  if err:=ctx.Err();err!=nil{return "",err}
 }
 size,err:=conversionPass(ctx,parts,src,dst,total,nil);if err!=nil{return "",err}
 var b strings.Builder;b.Grow(size)
 if _,err=conversionPass(ctx,parts,src,dst,total,&b);err!=nil{return "",err}
 return b.String(),nil
}
func conversionPass(ctx context.Context,parts []string,src,dst,total int,out *strings.Builder)(int,error) {
 size:=0
 for _,raw:=range parts {
  next:=0
  for i:=0;i<len(raw); {
   if i>=next {if err:=ctx.Err();err!=nil{return 0,err};next=i+4096}
   a,b,n,invalid:=pgencoding18.Step(src,dst,raw[i:])
   if invalid{return 0,encodingFailure(InvalidSequence,src,dst,raw[i:])}
   if n==0{return 0,encodingFailure(UntranslatableCharacter,src,dst,raw[i:])}
   width:=pgencoding18.Width(a)+pgencoding18.Width(b)
   if !appendSize(size,width) {return 0,conversionSizeError(total)};size+=width
   if out!=nil {writeWord(out,a);writeWord(out,b)}
   i+=n
  }
  if err:=ctx.Err();err!=nil{return 0,err}
 }
 return size,nil
}
func writeWord(out *strings.Builder,word uint32) {for shift:=24;shift>=0;shift-=8 {if b:=byte(word>>uint(shift));b!=0{out.WriteByte(b)}}}
