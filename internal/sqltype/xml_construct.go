package sqltype

import (
 "context"
 "encoding/base64"
 "fmt"
 "strings"
 "stuntgres/internal/sqlstate"
 "stuntgres/internal/xml"
)

func xmlConstructed(d *Descriptor, result xml.Result, err error) (Value,error) {
 if err!=nil{return Value{},xmlError(err)}
 if d==nil||d.id!=XML||!d.Concrete()||!result.Valid(){return Value{},fmt.Errorf("invalid generated XML value")}
 return Value{typ:d,xml:&xmlPayload{result:result}},nil
}
func XMLText(ctx context.Context,d *Descriptor,text string,comment bool)(Value,error){
 var result xml.Result;var err error
 if comment{result,err=xml.ConstructComment(ctx,text)}else{result,err=xml.ConstructText(ctx,text)}
 return xmlConstructed(d,result,err)
}
func XMLConcat(ctx context.Context,d *Descriptor,values []Value)(Value,error){
 operands:=make([]xml.Result,0,len(values))
 for i,value:=range values{if i%256==0{if err:=ctx.Err();err!=nil{return Value{},err}};if value.IsNull(){continue};result,err:=value.XML();if err!=nil{return Value{},err};operands=append(operands,result)}
 if err:=ctx.Err();err!=nil{return Value{},err};if len(operands)==0{return Null(d),nil}
 result,err:=xml.ConstructConcat(ctx,operands);return xmlConstructed(d,result,err)
}
func XMLPI(ctx context.Context,d *Descriptor,name string,data *string,explicit bool)(Value,error){
 result,null,err:=xml.ConstructPI(ctx,name,data,explicit);if err!=nil{return Value{},xmlError(err)};if null{return Null(d),nil};return xmlConstructed(d,result,nil)
}
func XMLRoot(ctx context.Context,d *Descriptor,value Value,version *string,standalone xml.RootStandalone)(Value,error){
 if value.IsNull(){return Null(d),nil};operand,err:=value.XML();if err!=nil{return Value{},err};result,err:=xml.ConstructRoot(ctx,operand,version,standalone);return xmlConstructed(d,result,err)
}
func XMLElement(ctx context.Context,d *Descriptor,name string,names []string,values []Value,env OutputEnvironment)(Value,error){
 if len(names)>len(values){return Value{},fmt.Errorf("invalid XML element operands")}
 attrs:=make([]xml.Attribute,0,len(names));content:=make([]string,0,len(values)-len(names))
 for i,value:=range values{if i%256==0{if err:=ctx.Err();err!=nil{return Value{},err}};if value.IsNull(){continue};text,err:=XMLValueCharacters(ctx,value,i<len(names),env);if err!=nil{return Value{},err};if i<len(names){attrs=append(attrs,xml.Attribute{Name:names[i],Characters:text})}else{content=append(content,text)}}
 result,err:=xml.ConstructElement(ctx,name,attrs,content);return xmlConstructed(d,result,err)
}
func XMLForest(ctx context.Context,d *Descriptor,names []string,values []Value,env OutputEnvironment)(Value,error){
 if len(names)!=len(values){return Value{},fmt.Errorf("invalid XML forest operands")}
 fields:=make([]xml.Field,0,len(values))
 for i,value:=range values{if i%256==0{if err:=ctx.Err();err!=nil{return Value{},err}};if value.IsNull(){continue};text,err:=XMLValueCharacters(ctx,value,false,env);if err!=nil{return Value{},err};fields=append(fields,xml.Field{Name:names[i],Characters:text})}
 if err:=ctx.Err();err!=nil{return Value{},err};if len(fields)==0{return Null(d),nil};result,err:=xml.ConstructForest(ctx,fields);return xmlConstructed(d,result,err)
}

// XMLValueCharacters uses actual typed output authority. No environment survives
// the call; XML child notices originate in this mapping, not in retained values.
func XMLValueCharacters(ctx context.Context,v Value,attribute bool,env OutputEnvironment)(string,error){
 if err:=ctx.Err();err!=nil{return "",err}
 if v.null{return "",fmt.Errorf("cannot map SQL NULL to XML characters")}
 d:=v.typ.BaseType();var text string;var err error
 if d.IsArray(){
  var out strings.Builder
  for i:=0;i<v.ArrayLen();i++{if i%256==0{if err:=ctx.Err();err!=nil{return "",err}};child:=v.ArrayElement(i);if child.IsNull(){continue};mapped,e:=XMLValueCharacters(ctx,child,false,env);if e!=nil{return "",e};if e=xmlMappingAppend(ctx,&out,"<element>",mapped,"</element>");e!=nil{return "",e}}
  text=out.String()
 }else{switch d.id {
 case Bool:if v.boolean{text="true"}else{text="false"}
 case Date,Timestamp,Timestamptz:
  if v.integer==DateNegativeInfinity||v.integer==DatePositiveInfinity{
   message,detail:="timestamp out of range","XML does not support infinite timestamp values."
   if d.id==Date{message,detail="date out of range","XML does not support infinite date values."}
   return "",&sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"22008",Message:message,Detail:detail}
  }
  text=jsonCreateDateTime(v,env)
 case Bytea:text,err=xmlBinaryCharacters(ctx,v.text,env.XMLBinaryHex)
 default:text,err=v.typ.OutputWithEnvironment(ctx,v,env)
 }}
 if err!=nil{return "",err}
 if attribute{return xml.Escape(ctx,text,xml.AttributeEscape)}
 if d.IsArray()||d.id==XML||d.id==Bytea{if err:=ctx.Err();err!=nil{return "",err};return text,nil}
 return xml.Escape(ctx,text,xml.ContentEscape)
}

func xmlMappingAppend(ctx context.Context,out *strings.Builder,parts ...string)error{
 total:=int64(out.Len());for _,s:=range parts{total+=int64(len(s));if err:=stringPadSize(1,total);err!=nil{return err}}
 // Reserve once per geometric growth, copying old characters in bounded chunks.
 if int(total)>out.Cap(){prior:=out.String();capacity:=max(total,int64(out.Cap())*2);if err:=stringPadSize(1,capacity);err!=nil{capacity=total};if err:=ctx.Err();err!=nil{return err};out.Reset();out.Grow(int(capacity));for len(prior)>0{if err:=ctx.Err();err!=nil{return err};n:=min(4096,len(prior));out.WriteString(prior[:n]);prior=prior[n:]}}
 for _,s:=range parts{for len(s)>0{if err:=ctx.Err();err!=nil{return err};n:=min(4096,len(s));out.WriteString(s[:n]);s=s[n:]}}
 return ctx.Err()
}
func xmlBinaryCharacters(ctx context.Context,raw string,hex bool)(string,error){
 size:=int64(len(raw))*2
 if !hex{encoded:=(int64(len(raw))+2)/3*4;size=encoded;if encoded>0{size+=(encoded-1)/72*2}}
 if err:=stringPadSize(1,size);err!=nil{return "",err};if err:=ctx.Err();err!=nil{return "",err}
 var out strings.Builder;out.Grow(int(size))
 if hex{const digits="0123456789ABCDEF";for at:=0;at<len(raw);{if err:=ctx.Err();err!=nil{return "",err};end:=min(len(raw),at+2048);for ;at<end;at++{out.WriteByte(digits[raw[at]>>4]);out.WriteByte(digits[raw[at]&15])}}}else{
  var input [54]byte;var encoded [72]byte
  for at:=0;at<len(raw);{if err:=ctx.Err();err!=nil{return "",err};if at>0{out.WriteString("\r\n")};n:=min(54,len(raw)-at);copy(input[:n],raw[at:at+n]);base64.StdEncoding.Encode(encoded[:],input[:n]);out.Write(encoded[:base64.StdEncoding.EncodedLen(n)]);at+=n}
 }
 if err:=ctx.Err();err!=nil{return "",err};return out.String(),nil
}
