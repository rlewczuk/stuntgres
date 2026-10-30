package xml

import (
 "context"
 "strconv"
 "strings"
 "unicode/utf8"
)

func legacyRange(r rune, ranges [][2]rune) bool {
 lo,hi:=0,len(ranges)
 for lo<hi {mid:=lo+(hi-lo)/2;if r<ranges[mid][0]{hi=mid}else if r>ranges[mid][1]{lo=mid+1}else{return true}}
 return false
}
func legacyNameStart(r rune) bool {
 return r=='_'||r==':'||r>='A'&&r<='Z'||r>='a'&&r<='z'||r>=0xC0&&r<=0xD6||r>=0xD8&&r<=0xF6||r>=0xF8&&r<=0xFF||legacyRange(r,legacyBase[:])||legacyRange(r,legacyIdeographic[:])
}
func legacyNameChar(r rune) bool {
 return legacyNameStart(r)||r=='-'||r=='.'||r>='0'&&r<='9'||r==0xB7||legacyRange(r,legacyDigit[:])||legacyRange(r,legacyCombining[:])||legacyRange(r,legacyExtender[:])
}

// MapName maps an already-folded SQL identifier using the release provider's
// legacy name rules. Inferred names additionally escape colons and initial xml.
func MapName(ctx context.Context, name string, inferred bool) (string,error) {
 encode:=func(at int,r rune)bool {
  if r=='_'&&at+1<len(name)&&name[at+1]=='x' {return true}
  if r==':'&&(at==0||inferred){return true}
  if inferred&&at==0&&len(name)>=3&&strings.EqualFold(name[:3],"xml"){return true}
  if at==0 {return !legacyNameStart(r)};return !legacyNameChar(r)
 }
 size,work:=0,0
 for at,r:=range name {work++;if work==1024{if err:=ctx.Err();err!=nil{return "",err};work=0};n:=utf8.RuneLen(r);if encode(at,r){n=7;if r>0xFFFF{n=8};if r>0xFFFFF{n=9}};if !appendSize(size,n){return "",appendSizeError(size,n)};size+=n}
 if err:=ctx.Err();err!=nil{return "",err}
 var out strings.Builder;out.Grow(size);var scratch [8]byte
 work=0
 for at,r:=range name {work++;if work==1024{if err:=ctx.Err();err!=nil{return "",err};work=0};if !encode(at,r){out.WriteRune(r);continue};digits:=strconv.AppendInt(scratch[:0],int64(r),16);out.WriteString("_x");for i:=len(digits);i<4;i++{out.WriteByte('0')};for _,c:=range digits{if c>='a'&&c<='f'{c-='a'-'A'};out.WriteByte(c)};out.WriteByte('_')}
 if err:=ctx.Err();err!=nil{return "",err};return out.String(),nil
}

// UnmapName is the release presentation inverse, not a construction validator.
// It decodes exactly four hex digits once; supplementary escapes remain literal.
func UnmapName(name string) string {
 var out strings.Builder
 start:=0
 for i:=0;i+6<len(name);i++ {
  if name[i]!='_'||name[i+1]!='x'||name[i+6]!='_'{continue}
  var value rune
  valid:=true
  for j:=i+2;j<i+6;j++ {
   c:=name[j];var digit byte
   switch {case c>='0'&&c<='9':digit=c-'0';case c>='a'&&c<='f':digit=c-'a'+10;case c>='A'&&c<='F':digit=c-'A'+10;default:valid=false}
   if !valid{break};value=value*16+rune(digit)
  }
  if !valid{continue}
  if out.Len()==0{out.Grow(len(name))}
  out.WriteString(name[start:i]);out.WriteRune(value)
  i+=6;start=i+1
 }
 if start==0{return name}
 out.WriteString(name[start:]);return out.String()
}
