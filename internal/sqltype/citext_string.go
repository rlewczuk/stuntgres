package sqltype

import (
 "context"
 "fmt"
 "strings"
 "unicode/utf8"
 "stuntgres/internal/sqltype/unicode16"
)

// BindCitextString binds the selected installed signature, not a name-based
// extension of ordinary TEXT evaluation. Formals retain their real generations.
func BindCitextString(name string,result,text Usage,args []Usage) (StringPlan,error) {
 invalid:=func()(StringPlan,error){return StringPlan{},fmt.Errorf("invalid citext string signature")}
 n:=3;if name=="strpos" {n=2}
 if len(args)!=n||!text.Valid()||text!=text.Type.Usage()||text.Type.ID()!=Text||!result.Valid() {return invalid()}
 if !args[0].Valid()||!args[0].Type.IsCitext()||!args[1].Valid()||args[1].Type!=args[0].Type {return invalid()}
 if n==3 {
  if !args[2].Valid() {return invalid()}
  switch name {
  case "replace":if args[2].Type!=args[0].Type {return invalid()}
  case "split_part":if args[2].Type.ID()!=Int4||args[2]!=args[2].Type.Usage() {return invalid()}
  case "translate":if args[2].Type!=text.Type {return invalid()}
  default:return invalid()
  }
 }
 textual:=[4]bool{true,true,n==3&&name!="split_part"}
 p,err:=BindString(name,result,text,args,textual,false);if err!=nil {return p,err}
 p.citext=true
 for i,arg:=range args {p.formal[i]=arg}
 return p,err
}
func (p StringPlan) Citext() bool {return p.citext}

// quoteCitextSearch emits the released wrapper's fixed inner regexp_replace:
// every codepoint outside [a-zA-Z_0-9] is preceded by one regex backslash.
func quoteCitextSearch(ctx context.Context,s string) (string,error) {
 extra:=0
 work:=binarySearchWork{ctx:ctx,remaining:4096}
 for _,r:=range s {
  if err:=work.step();err!=nil {return "",err}
  if !(r>='a'&&r<='z'||r>='A'&&r<='Z'||r>='0'&&r<='9'||r=='_') {extra++}
 }
 if extra==0 {return s,ctx.Err()}
 size,err:=stringOutputSize(len(s),extra);if err!=nil {return "",err}
 var out strings.Builder;out.Grow(size)
 for at:=0;at<len(s); {
  if err:=work.step();err!=nil {return "",err}
  r,n:=utf8.DecodeRuneInString(s[at:])
  if !(r>='a'&&r<='z'||r>='A'&&r<='Z'||r>='0'&&r<='9'||r=='_') {out.WriteByte('\\')}
  out.WriteString(s[at:at+n]);at+=n
 }
 return out.String(),ctx.Err()
}

func (p StringPlan) EvaluateCitext(ctx context.Context,values [4]Value,input InputEnvironment,output OutputEnvironment,state *PGRegexExecution) (Value,error) {
 if !p.citext {return Value{},fmt.Errorf("ordinary string plan used as citext")}
 if err:=ctx.Err();err!=nil {return Value{},err}
 for i:=range int(p.count) {if values[i].Type()!=p.args[i].Type {return Value{},fmt.Errorf("foreign citext string value")}}
 for i:=range int(p.count) {if values[i].IsNull() {return Null(p.result.Type),nil}}
 texts,err:=p.stringInputs(ctx,values,input,output);if err!=nil {return Value{},err}
 mode:=p.text.Collation.CaseMode()
 switch p.name {
 case "strpos":
  source,err:=unicode16.ConvertCase(ctx,texts[0],unicode16.Lower,mode);if err!=nil {return Value{},err}
  needle,err:=unicode16.ConvertCase(ctx,texts[1],unicode16.Lower,mode);if err!=nil {return Value{},err}
  index,err:=stringPosition(ctx,source,needle);if err!=nil {return Value{},err};return Integer(p.result.Type,index)
 case "translate":
  lower,err:=unicode16.ConvertCase(ctx,texts[1],unicode16.Lower,mode);if err!=nil {return Value{},err}
  first,err:=stringTranslate(ctx,texts[0],lower,texts[2]);if err!=nil {return Value{},err}
  upper,err:=unicode16.ConvertCase(ctx,texts[1],unicode16.Upper,mode);if err!=nil {return Value{},err}
  result,err:=stringTranslate(ctx,first,upper,texts[2]);return String(p.result.Type,result),err
 case "replace","split_part":
  if state==nil {return Value{},fmt.Errorf("missing citext string regex execution")}
  // Fixed wrapper metadata is invocation-local: ordinary StringPlan values
  // retain no large regex plan or separately allocated provider payload.
  text:=p.text
  plan:=RegexpPlan{kind:RegexpSplitTable,argCount:3,args:[7]Usage{text,text,text},result:text,collation:text.Collation,replacement:-1,start:-1,occurrence:-1,endOption:-1,flags:2,subexpr:-1}
  if p.name=="replace" {plan.kind,plan.argCount,plan.replacement,plan.flags=RegexpReplace,4,2,3;plan.args[3]=text}
  if p.name=="split_part" {plan.splitField=values[2].integer;if plan.splitField<=0 {return Null(p.result.Type),nil}}
  search,err:=quoteCitextSearch(ctx,texts[1]);if err!=nil {state.Reset();return Value{},err}
  args:=[7]Value{String(p.text.Type,texts[0]),String(p.text.Type,search),String(p.text.Type,"i")}
  if p.name=="replace" {args[2]=String(p.text.Type,texts[2]);args[3]=String(p.text.Type,"gi")}
  value,_,err:=plan.evaluate(ctx,args,state)
  return value,err
 }
 return Value{},fmt.Errorf("invalid selected citext string operation")
}
