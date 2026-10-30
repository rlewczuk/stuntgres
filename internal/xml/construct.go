package xml

import (
 "context"
 "fmt"
 "strings"
)

// ConstructError describes a constructor failure, independent of SQL positions.
type ConstructError struct { Message, Detail string; PI bool }
func (e *ConstructError) Error() string { return e.Message }

func constructed(ctx context.Context, parts []string, declaration Declaration) (Result,error) {
 text,err:=constructJoin(ctx,parts);if err!=nil{return Result{},err}
 return Result{original:text,requested:Content,effective:Content,declaration:declaration,valid:true},nil
}

func ConstructText(ctx context.Context, text string) (Result,error) {
 escaped,err:=Escape(ctx,text,TextEscape);if err!=nil{return Result{},err}
 if err:=ctx.Err();err!=nil{return Result{},err}
 return Result{original:escaped,requested:Content,effective:Content,valid:true},nil
}
func ConstructComment(ctx context.Context, text string) (Result,error) {
 for at:=0;at<len(text); {
  if err:=ctx.Err();err!=nil{return Result{},err};end:=min(len(text),at+4096)
  for i:=at;i<end;i++ {if text[i]=='-'&&(i+1==len(text)||text[i+1]=='-'){return Result{},&ConstructError{Message:"invalid XML comment"}}};at=end
 }
 return constructed(ctx,[]string{"<!--",text,"-->"},Declaration{})
}

// Attribute characters have already passed SQL type output and attribute escaping.
// Names are produced exclusively by MapName at the SQL syntax boundary.
type Attribute struct { Name, Characters string }
func ConstructElement(ctx context.Context, name string, attributes []Attribute, content []string) (Result,error) {
 if err:=ctx.Err();err!=nil{return Result{},err}
 if name=="" {return Result{},fmt.Errorf("empty XML element name")}
 parts:=make([]string,0,4+5*len(attributes)+len(content))
 parts=append(parts,"<",name)
 for _,a:=range attributes {if err:=ctx.Err();err!=nil{return Result{},err};if a.Name==""{return Result{},fmt.Errorf("empty XML attribute name")};parts=append(parts," ",a.Name,"=\"",a.Characters,"\"")}
 if len(content)==0 {parts=append(parts,"/>")}else{parts=append(parts,">");parts=append(parts,content...);parts=append(parts,"</",name,">")}
 return constructed(ctx,parts,Declaration{})
}

type Field struct { Name, Characters string }
func ConstructForest(ctx context.Context, fields []Field) (Result,error) {
 if err:=ctx.Err();err!=nil{return Result{},err}
 parts:=make([]string,0,7*len(fields))
 for _,field:=range fields {if err:=ctx.Err();err!=nil{return Result{},err};if field.Name==""{return Result{},fmt.Errorf("empty XML forest name")};parts=append(parts,"<",field.Name,">",field.Characters,"</",field.Name,">")}
 return constructed(ctx,parts,Declaration{})
}

// ConstructPI validates the target before observing an explicit SQL NULL data
// operand. The caller evaluates all SQL operands before invoking this operation.
func ConstructPI(ctx context.Context, target string, data *string, explicit bool) (Result,bool,error) {
 if err:=ctx.Err();err!=nil{return Result{},false,err}
 if target==""||!explicit&&data!=nil{return Result{},false,fmt.Errorf("invalid XML processing instruction operands")}
 if strings.EqualFold(target,"xml") {return Result{},false,&ConstructError{Message:"invalid XML processing instruction",Detail:"XML processing instruction target name cannot be \""+target+"\".",PI:true}}
 if explicit&&data==nil{return Result{},true,nil}
 parts:=[]string{"<?",target}
 if explicit {
  text:=*data
  for at:=0;at<len(text); {if err:=ctx.Err();err!=nil{return Result{},false,err};end:=min(len(text),at+4096);for i:=at;i<end;i++{if text[i]=='?'&&i+1<len(text)&&text[i+1]=='>'{return Result{},false,&ConstructError{Message:"invalid XML processing instruction",Detail:"XML processing instruction cannot contain \"?>\".",PI:true}}};at=end}
  at:=0;for at<len(text)&&text[at]==' ' {if at%4096==0 {if err:=ctx.Err();err!=nil{return Result{},false,err}};at++}
  parts=append(parts," ",text[at:])
 }
 parts=append(parts,"?>");r,err:=constructed(ctx,parts,Declaration{});return r,false,err
}
