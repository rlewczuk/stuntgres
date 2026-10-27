package xml

import (
 "context"
 "fmt"
 "strings"
 "stuntgres/internal/sqltype/pgencoding18"
)

// OutputWarning is an operation's PostgreSQL output warning, not a libxml event.
// An empty Message denotes no warning. Strings are immutable and independently
// owned; output never mutates the parsed Result or retains operation state.
type OutputWarning struct { Message, Detail string }

func OutputText(ctx context.Context,value Result)(string,OutputWarning,error) {return output(ctx,value,6)}
func OutputBinary(ctx context.Context,value Result,targetEncoding int)(string,OutputWarning,error) {return output(ctx,value,targetEncoding)}
func output(ctx context.Context,value Result,target int)(string,OutputWarning,error) {
 if err:=ctx.Err();err!=nil{return "",OutputWarning{},err}
 if !value.valid {return "",OutputWarning{},fmt.Errorf("cannot output zero XML Result")}
 if target<0||target>=pgencoding18.Count {return "",OutputWarning{},fmt.Errorf("invalid XML target encoding ID %d",target)}
 // Output always uses the custom byte0 reader, independently of DOCUMENT's
 // accepted declaration metadata. Only the prefix is scanned, never the body.
 d,detail,err:=readContentDeclaration(ctx,value.original)
 if err!=nil {return "",OutputWarning{},err}
 if detail!="" {
  warning:=OutputWarning{Message:"could not parse XML declaration in stored value",Detail:detail}
  original:=value.original
  if target!=0&&target!=6 {
   original,err=convert(ctx,original,6,target)
   if err!=nil {return "",warning,err}
  }
  if err:=ctx.Err();err!=nil{return "",warning,err}
  return original,warning,nil
 }
 body:=value.original[d.End:]
 version:="1.0";if d.Present {version=d.Version}
 emit:=(d.Present&&version!="1.0")||d.Standalone!=Absent||target!=0&&target!=6
 var parts [10]string
 n:=0
 if emit {
  parts[n]="<?xml version=\"";n++;parts[n]=version;n++;parts[n]="\"";n++
  if target!=0&&target!=6 {parts[n]=" encoding=\"";n++;parts[n]=pgencoding18.Name(target);n++;parts[n]="\"";n++}
  if d.Standalone!=Absent {if d.Standalone==Yes {parts[n]=" standalone=\"yes\""}else{parts[n]=" standalone=\"no\""};n++}
  parts[n]="?>";n++
 }else if len(body)!=0&&body[0]=='\n' {body=body[1:]}
 parts[n]=body;n++
 if target!=0&&target!=6 {converted,err:=convertParts(ctx,parts[:n],6,target);return converted,OutputWarning{},err}
 if n==1 {if err:=ctx.Err();err!=nil{return "",OutputWarning{},err};return body,OutputWarning{},nil}
 total:=0
 for _,s:=range parts[:n] {if !appendSize(total,len(s)){return "",OutputWarning{},appendSizeError(total,len(s))};total+=len(s)}
 var b strings.Builder;b.Grow(total)
 for _,s:=range parts[:n] {for len(s)>0 {if err:=ctx.Err();err!=nil{return "",OutputWarning{},err};size:=min(4096,len(s));b.WriteString(s[:size]);s=s[size:]}}
 if err:=ctx.Err();err!=nil{return "",OutputWarning{},err}
 return b.String(),OutputWarning{},nil
}
