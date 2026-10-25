package sqltype

import (
 "context"
 "fmt"
 "stuntgres/internal/engine/fts"
)

type FTSVectorFunction uint8
const (
 FTSVectorLength FTSVectorFunction = iota
 FTSSetWeight
 FTSSetSelectedWeight
 FTSStrip
 FTSDelete
 FTSDeleteArray
 FTSFilter
 FTSVectorToArray
)
func (o FTSVectorFunction) Name() string {
 switch o {case FTSVectorLength:return "length";case FTSSetWeight,FTSSetSelectedWeight:return "setweight";case FTSStrip:return "strip";case FTSDelete,FTSDeleteArray:return "ts_delete";case FTSFilter:return "ts_filter";case FTSVectorToArray:return "tsvector_to_array"};return ""
}
func (o FTSVectorFunction) Arity() int {switch o {case FTSVectorLength,FTSStrip,FTSVectorToArray:return 1;case FTSSetSelectedWeight:return 3;case FTSSetWeight,FTSDelete,FTSDeleteArray,FTSFilter:return 2};return 0}
func (o FTSVectorFunction) Formal(i int) ID {
 if i==0{return TSVector};if i==2{return 1009}
 switch o {case FTSSetWeight,FTSSetSelectedWeight:return Char;case FTSDelete:return Text;case FTSDeleteArray:return 1009;case FTSFilter:return 1002};return 0
}
func (o FTSVectorFunction) ResultID() ID {switch o {case FTSVectorLength:return Int4;case FTSVectorToArray:return 1009};return TSVector}
type FTSVectorPlan struct {operation FTSVectorFunction; inputs [3]*Descriptor;result *Descriptor}
func vectorCompanion(source *Descriptor,id ID)*Descriptor {
 switch id {case TSVector:return source;case Int4:return source.ftsDistance;case Char:return source.ftsChar;case Text:return source.ftsText;case 1009:return source.ftsTextArray;case 1002:return source.ftsCharArray};return nil
}
func BindFTSVector(op FTSVectorFunction,result Usage,args [3]Usage)(FTSVectorPlan,error) {
 if op.Name()==""||args[0].Type==nil||args[0].Type.id!=TSVector {return FTSVectorPlan{},fmt.Errorf("invalid vector function signature")}
 source:=args[0].Type
 p:=FTSVectorPlan{operation:op,result:result.Type}
 if result.Type==nil||result.Type!=vectorCompanion(source,op.ResultID())||result!=result.Type.Usage(){return FTSVectorPlan{},fmt.Errorf("foreign vector result authority")}
 if op==FTSVectorToArray&&(!result.Type.Concrete()||result.Type.element!=source.ftsText){return FTSVectorPlan{},fmt.Errorf("foreign vector result element authority")}
 for i,arg:=range args {
  if i>=op.Arity(){if arg.Type!=nil{return FTSVectorPlan{},fmt.Errorf("extra vector operand")};continue}
  expected:=vectorCompanion(source,op.Formal(i))
  // Selector collations are valid usage annotations, not descriptor authority;
  // matching itself remains byte-exact for both TEXT and TEXT[].
  if expected==nil||arg.Type!=expected||arg.Typmod!=expected.Usage().Typmod||!arg.Valid(){return FTSVectorPlan{},fmt.Errorf("foreign vector operand authority")}
  if expected.IsArray() {element:=source.ftsText;if op.Formal(i)==1002{element=source.ftsChar};if expected.element!=element{return FTSVectorPlan{},fmt.Errorf("foreign vector array element authority")}}
  p.inputs[i]=arg.Type
 }
 return p,nil
}
func(p FTSVectorPlan) Operation() FTSVectorFunction{return p.operation}
func(p FTSVectorPlan) ValidateValues(values [3]Value)error {
 if p.result==nil||p.operation.Name()==""{return fmt.Errorf("invalid vector function plan")}
 var args [3]Usage
 for i:=range p.operation.Arity(){v:=values[i];if v.typ==nil||v.typ!=p.inputs[i]{return fmt.Errorf("foreign vector value")};if !v.null {if v.typ.id==TSVector&&!v.validFTS(){return fmt.Errorf("invalid vector payload")};if v.typ.IsArray()&&v.array==nil{return fmt.Errorf("invalid vector array payload")}};args[i]=v.typ.Usage()}
 checked,err:=BindFTSVector(p.operation,p.result.Usage(),args);if err!=nil||checked!=p{return fmt.Errorf("invalid vector plan authority")};return nil
}
func(p FTSVectorPlan) Evaluate(ctx context.Context,values [3]Value)(Value,error) {
 if err:=ctx.Err();err!=nil{return Value{},err};if err:=p.ValidateValues(values);err!=nil{return Value{},err}
 for _,v:=range values[:p.operation.Arity()]{if v.null{return Null(p.result),nil}}
 source:=values[0].ftsVector
 result:=Value{typ:p.result}
 var err error
 selector:=func(index int)func(int)(string,bool){return func(i int)(string,bool){v:=values[index].ArrayElement(i);return v.text,!v.null}}
 weight:=func(v Value)(byte,error){var b byte;if len(v.text)>0{b=v.text[0]};return fts.ParseWeight(b)}
 switch p.operation {
 case FTSVectorLength:result.integer=int64(source.LexemeCount())
 case FTSSetWeight,FTSSetSelectedWeight:
  var w byte;w,err=weight(values[1]);if err!=nil{return Value{},err}
  if p.operation==FTSSetWeight{result.ftsVector,err=source.SetWeight(ctx,w)}else{result.ftsVector,err=source.SetSelectedWeight(ctx,w,values[2].ArrayLen(),selector(2))}
 case FTSStrip:result.ftsVector,err=source.Strip(ctx)
 case FTSDelete:result.ftsVector,err=source.Delete(ctx,1,func(int)(string,bool){return values[1].text,true})
 case FTSDeleteArray:result.ftsVector,err=source.Delete(ctx,values[1].ArrayLen(),selector(1))
 case FTSFilter:
  var mask byte
  for i:=range values[1].ArrayLen(){if i&255==0{if err:=ctx.Err();err!=nil{return Value{},err}};v:=values[1].ArrayElement(i);if v.null{return Value{},failure("22004","weight array may not contain nulls")};w,e:=weight(v);if e!=nil{return Value{},e};mask|=1<<w}
  result.ftsVector,err=source.Filter(ctx,mask)
 case FTSVectorToArray:
  elements:=make([]Value,source.LexemeCount())
  for i:=range elements{if i&255==0{if err:=ctx.Err();err!=nil{return Value{},err}};elements[i]=String(p.result.element,source.LexemeAt(i))}
  var dims []ArrayDimension;if len(elements)>0{dims=[]ArrayDimension{{Lower:1,Length:int32(len(elements))}}}
  result,err=OwnedArrayValue(p.result,dims,elements)
 }
 if err!=nil{return Value{},err};return result,ctx.Err()
}
