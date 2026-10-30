package xml

import (
 "context"
 "strconv"
 "strings"
 "unicode/utf8"
)

// EscapeProfile distinguishes the release provider's three output operations.
type EscapeProfile uint8
const (
 TextEscape EscapeProfile = iota
 ContentEscape
 AttributeEscape
)

func escapeReplacement(r rune, profile EscapeProfile) string {
 switch r {
 case '<': return "&lt;"
 case '>': return "&gt;"
 case '&': return "&amp;"
 case '"': if profile != ContentEscape { return "&quot;" }
 case '\r': if profile == ContentEscape { return "&#x0d;" }; return "&#13;"
 case '\t': if profile == AttributeEscape { return "&#9;" }
 case '\n': if profile == AttributeEscape { return "&#10;" }
 }
 return ""
}

// Escape performs checked sizing followed by bounded emission. It does not
// validate XML input characters: constructors intentionally admit controls.
func Escape(ctx context.Context, text string, profile EscapeProfile) (string,error) {
 if profile>AttributeEscape { return "", &ConstructError{Message:"invalid XML escape profile"} }
 size:=0
 var scratch [16]byte
 for at:=0;at<len(text); {
  if err:=ctx.Err();err!=nil{return "",err}
  end:=min(len(text),at+4096)
  for at<end {
   r,n:=utf8.DecodeRuneInString(text[at:]); replacement:=escapeReplacement(r,profile); count:=n
   if replacement!="" { count=len(replacement) }
   if profile==AttributeEscape&&r>=128{count=4;for x:=r;x>0;x>>=4{count++}}
   if !appendSize(size,count){return "",appendSizeError(size,count)};size+=count;at+=n
  }
 }
 if err:=ctx.Err();err!=nil{return "",err}
 var out strings.Builder;out.Grow(size)
 for at:=0;at<len(text); {
  if err:=ctx.Err();err!=nil{return "",err}
  end:=min(len(text),at+4096)
  for at<end {
   r,n:=utf8.DecodeRuneInString(text[at:]);replacement:=escapeReplacement(r,profile)
   if profile==AttributeEscape&&r>=128{
    out.WriteString("&#x");digits:=strconv.AppendInt(scratch[:0],int64(r),16)
    for _,c:=range digits{if c>='a'&&c<='f'{c-='a'-'A'};out.WriteByte(c)};out.WriteByte(';')
   }else if replacement!="" {out.WriteString(replacement)}else{out.WriteString(text[at:at+n])};at+=n
  }
 }
 if err:=ctx.Err();err!=nil{return "",err};return out.String(),nil
}

func constructJoin(ctx context.Context, parts []string) (string,error) {
 size:=0
 for _,s:=range parts {if err:=ctx.Err();err!=nil{return "",err};if !appendSize(size,len(s)){return "",appendSizeError(size,len(s))};size+=len(s)}
 var out strings.Builder
 if err:=ctx.Err();err!=nil{return "",err};out.Grow(size)
 for _,s:=range parts {if err:=ctx.Err();err!=nil{return "",err};for len(s)>0 {if err:=ctx.Err();err!=nil{return "",err};n:=min(4096,len(s));out.WriteString(s[:n]);s=s[n:]}}
 if err:=ctx.Err();err!=nil{return "",err};return out.String(),nil
}
