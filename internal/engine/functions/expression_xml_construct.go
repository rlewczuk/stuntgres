package functions

import (
 "context"
 "fmt"
 "slices"
 "strings"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqltype"
 "stuntgres/internal/xml"
)

// XMLConstruct owns its checked names/options/operand vector. Evaluation first
// finishes every SQL child, then begins type mapping and provider construction.
type XMLConstruct struct {
 expr.Info
 kind parser.XMLConstructKind
 name string
 names []string
 children []expr.Expr
 version bool
 standalone uint8
 syntax parser.XMLConstructExpr
}
func NewXMLConstruct(ctx context.Context,info expr.Info,raw parser.XMLConstructExpr,name string,names []string,children []expr.Expr)(XMLConstruct,error){
 if len(raw.Children())!=len(children)||len(raw.Named)!=len(names){return XMLConstruct{},fmt.Errorf("mismatched XML syntax operands")}
 if raw.Kind==parser.XMLElement||raw.Kind==parser.XMLPI {
  mapped,err:=xml.MapName(ctx,raw.Name.Text,false)
  if err!=nil{return XMLConstruct{},err};if mapped!=name{return XMLConstruct{},fmt.Errorf("mismatched XML syntax name")}
 }
 for i,named:=range raw.Named {
  inferred:=named.Name==nil;written:=""
  if inferred {column,ok:=named.Expr.(parser.ColumnExpr);if !ok||len(column.Name.Parts)==0{return XMLConstruct{},fmt.Errorf("invalid inferred XML name")};written=column.Name.Parts[len(column.Name.Parts)-1].Text}else{written=named.Name.Text}
  mapped,err:=xml.MapName(ctx,written,inferred);if err!=nil{return XMLConstruct{},err};if mapped!=names[i]{return XMLConstruct{},fmt.Errorf("mismatched XML syntax label")}
 }
 raw.Args=slices.Clone(raw.Args);raw.Named=slices.Clone(raw.Named)
 for i:=range raw.Named{if raw.Named[i].Name!=nil{name:=*raw.Named[i].Name;raw.Named[i].Name=&name}}
 e:=XMLConstruct{Info:info,kind:raw.Kind,name:name,names:slices.Clone(names),children:slices.Clone(children),version:raw.Version!=nil,standalone:raw.Standalone,syntax:raw}
 return e,e.ValidateStoredProvider()
}
func(e XMLConstruct) Kind()parser.XMLConstructKind{return e.kind}
// MatchesSyntax authenticates this constructor's own syntax and occurrence
// slots. Descendant annotations are finalized by ordinary literal capture and
// authenticated by that owner at their exact preorder paths.
func(e XMLConstruct) MatchesSyntax(raw parser.XMLConstructExpr)bool{
 saved:=e.syntax
 if raw.Kind!=saved.Kind||raw.Pos!=saved.Pos||raw.Name!=saved.Name||
  raw.VersionPos!=saved.VersionPos||raw.Standalone!=saved.Standalone||
  raw.StandalonePos!=saved.StandalonePos||len(raw.Named)!=len(saved.Named)||
  len(raw.Args)!=len(saved.Args)||(raw.Version==nil)!=(saved.Version==nil){return false}
 for i,named:=range raw.Named{
  original:=saved.Named[i]
  if named.Pos!=original.Pos||(named.Name==nil)!=(original.Name==nil){return false}
  if named.Name!=nil&&*named.Name!=*original.Name{return false}
  if named.Expr==nil||original.Expr==nil||named.Expr.SourcePosition()!=original.Expr.SourcePosition(){return false}
 }
 for i,child:=range raw.Args{if child==nil||saved.Args[i]==nil||child.SourcePosition()!=saved.Args[i].SourcePosition(){return false}}
 return raw.Version==nil||raw.Version.SourcePosition()==saved.Version.SourcePosition()
}
func(e XMLConstruct) Name()string{return e.name}
func(e XMLConstruct) Names()[]string{return slices.Clone(e.names)}
func(e XMLConstruct) HasVersion()bool{return e.version}
func(e XMLConstruct) Standalone()uint8{return e.standalone}
func(e XMLConstruct) Children()[]expr.Expr{return slices.Clone(e.children)}
func(e XMLConstruct) ValidateStoredProvider()error{
 if e.Usage.Type==nil||e.Usage.Type.ID()!=sqltype.XML||e.kind>parser.XMLRoot||e.standalone>3{return fmt.Errorf("invalid XML construction recipe")}
 for _,child:=range e.children{if child==nil||child.ResultUsage().Type==nil{return fmt.Errorf("invalid XML construction child")}}
 if e.kind!=parser.XMLRoot&&(e.version||e.standalone!=0){return fmt.Errorf("invalid XML construction options")}
 if e.kind!=parser.XMLElement&&e.kind!=parser.XMLPI&&e.name!=""{return fmt.Errorf("unexpected XML construction name")}
 switch e.kind {
 case parser.XMLConcat:
  if len(e.children)==0||len(e.names)!=0{return fmt.Errorf("invalid XMLCONCAT operands")}
  for _,child:=range e.children{if child.ResultUsage().Type.BaseType().ID()!=sqltype.XML{return fmt.Errorf("invalid XMLCONCAT child")}}
 case parser.XMLElement:
  if e.name==""||len(e.names)>len(e.children){return fmt.Errorf("invalid XMLELEMENT operands")}
  for i,name:=range e.names{if name==""||slices.Contains(e.names[:i],name){return fmt.Errorf("invalid XML attribute names")}}
 case parser.XMLForest:
  if len(e.names)==0||len(e.names)!=len(e.children){return fmt.Errorf("invalid XMLFOREST operands")};for _,name:=range e.names{if name==""{return fmt.Errorf("invalid XML forest name")}}
 case parser.XMLPI:
  if e.name==""||len(e.names)!=0||len(e.children)>1{return fmt.Errorf("invalid XMLPI operands")};if len(e.children)==1&&e.children[0].ResultUsage().Type.ID()!=sqltype.Text{return fmt.Errorf("invalid XMLPI data")}
 case parser.XMLRoot:
  count:=1;if e.version{count++};if len(e.names)!=0||len(e.children)!=count||e.children[0].ResultUsage().Type.BaseType().ID()!=sqltype.XML{return fmt.Errorf("invalid XMLROOT operands")};if e.version&&e.children[1].ResultUsage().Type.ID()!=sqltype.Text{return fmt.Errorf("invalid XMLROOT version")}
 }
 return nil
}
func(e XMLConstruct) Eval(c expr.Context)(sqltype.Value,error){
 ctx,err:=evaluationContext(c);if err!=nil{return sqltype.Value{},err}
 values:=make([]sqltype.Value,len(e.children))
 for i,child:=range e.children{values[i],err=child.Eval(c);if err!=nil{return sqltype.Value{},err}}
 switch e.kind {
 case parser.XMLConcat:return sqltype.XMLConcat(ctx,e.Usage.Type,values)
 case parser.XMLElement:return sqltype.XMLElement(ctx,e.Usage.Type,e.name,e.names,values,c.FunctionXMLOutput())
 case parser.XMLForest:return sqltype.XMLForest(ctx,e.Usage.Type,e.names,values,c.FunctionXMLOutput())
 case parser.XMLPI:
  var data *string
  if len(values)>0&&!values[0].IsNull(){text,err:=values[0].Text();if err!=nil{return sqltype.Value{},err};data=&text}
  return sqltype.XMLPI(ctx,e.Usage.Type,e.name,data,len(values)>0)
 case parser.XMLRoot:
  var version *string
  if e.version&&!values[1].IsNull(){text,err:=values[1].Text();if err!=nil{return sqltype.Value{},err};version=&text}
  return sqltype.XMLRoot(ctx,e.Usage.Type,values[0],version,xml.RootStandalone(e.standalone))
 }
 return sqltype.Value{},fmt.Errorf("invalid XML construction kind")
}
func(e XMLConstruct) Rebuild(info expr.Info,children []expr.Expr)(expr.Expr,error){if err:=e.ValidateStoredProvider();err!=nil{return nil,err};if len(children)!=len(e.children){return nil,fmt.Errorf("invalid XML construction child count")};for i,child:=range children{if child==nil||child.ResultUsage()!=e.children[i].ResultUsage(){return nil,fmt.Errorf("invalid XML construction child usage")}};e.Info,e.children=info,slices.Clone(children);return e,e.ValidateStoredProvider()}
func(e XMLConstruct) IntrinsicVolatility()expr.Volatility{if e.kind==parser.XMLElement||e.kind==parser.XMLForest{return expr.Stable};return expr.Immutable}
func(e XMLConstruct) PlanningFold()expr.Fold{return expr.FoldNever}
func(e XMLConstruct) PreflightFold()expr.Preflight{return expr.PreflightChildren}
func(e XMLConstruct) ExecutionDemand(){}
func(e XMLConstruct) LooksLikeFunction()bool{return true}
func(e XMLConstruct) ClosureTransparent(){}
func(e XMLConstruct) Capture(kind expr.CaptureKind)(expr.CaptureOperands,bool,error){return expr.CaptureOperands{Count:len(e.children),Indexed:true,XMLSources:true},kind==expr.CaptureXMLConstruct,nil}
func(e XMLConstruct) CaptureOperand(written int)(expr.Expr,error){if written<0||written>=len(e.children){return nil,fmt.Errorf("invalid XML written operand")};return e.children[written],nil}
func(e XMLConstruct) SameGroup(other expr.Expr,same func(expr.Expr,expr.Expr)bool,definition bool)bool{o,ok:=other.(XMLConstruct);if !ok||e.kind!=o.kind||e.name!=o.name||e.version!=o.version||e.standalone!=o.standalone||!slices.Equal(e.names,o.names)||len(e.children)!=len(o.children){return false};for i:=range e.children{if !same(e.children[i],o.children[i]){return false}};return true}
func(e XMLConstruct) Render()(expr.Rendering,error){
 pieces:=[]expr.Piece{expr.TextPiece(strings.ToUpper(e.kind.String())+"(")};args:=make([]expr.RenderArg,len(e.children))
 add:=func(i int){args[i]=expr.RenderArg{Expr:e.children[i],HideImplicit:true,XMLSource:e.Pos};pieces=append(pieces,expr.ArgPiece(i))}
 quote:=func(s string)string{return "\""+strings.ReplaceAll(s,"\"","\"\"")+"\""}
 switch e.kind {
 case parser.XMLConcat:for i:=range e.children{if i>0{pieces=append(pieces,expr.TextPiece(", "))};add(i)}
 case parser.XMLElement:
  pieces=append(pieces,expr.TextPiece("NAME "+quote(xml.UnmapName(e.name))))
  if len(e.names)>0{pieces=append(pieces,expr.TextPiece(", XMLATTRIBUTES("));for i:=range e.names{if i>0{pieces=append(pieces,expr.TextPiece(", "))};add(i);pieces=append(pieces,expr.TextPiece(" AS "+quote(xml.UnmapName(e.names[i]))))};pieces=append(pieces,expr.TextPiece(")"))}
  for i:=len(e.names);i<len(e.children);i++{pieces=append(pieces,expr.TextPiece(", "));add(i)}
 case parser.XMLForest:for i:=range e.names{if i>0{pieces=append(pieces,expr.TextPiece(", "))};add(i);pieces=append(pieces,expr.TextPiece(" AS "+quote(xml.UnmapName(e.names[i]))))}
 case parser.XMLPI:pieces=append(pieces,expr.TextPiece("NAME "+quote(xml.UnmapName(e.name))));if len(e.children)>0{pieces=append(pieces,expr.TextPiece(", "));add(0)}
 case parser.XMLRoot:add(0);pieces=append(pieces,expr.TextPiece(", VERSION "));if e.version{add(1)}else{pieces=append(pieces,expr.TextPiece("NO VALUE"))};switch e.standalone{case 1:pieces=append(pieces,expr.TextPiece(", STANDALONE NO VALUE"));case 2:pieces=append(pieces,expr.TextPiece(", STANDALONE YES"));case 3:pieces=append(pieces,expr.TextPiece(", STANDALONE NO"))}
 }
 pieces=append(pieces,expr.TextPiece(")"));return expr.Rendering{Tree:e.kind.String(),Inputs:e.Children(),Form:expr.FormTemplate,Args:args,Pieces:pieces},e.ValidateStoredProvider()
}

func(e XMLConstruct) Retag(info expr.Info,children []expr.Expr,changed bool,env expr.Retag)(expr.Expr,bool,error){
 if err:=e.ValidateStoredProvider();err!=nil{return nil,false,err}
 if len(children)!=len(e.children){return nil,false,fmt.Errorf("invalid XML retag child count")}
 for i,child:=range children{
  expected:=e.children[i].ResultUsage();expected.Type=env.Type(expected.Type)
  var err error;expected.Collation,err=env.Collation(expected.Collation);if err!=nil{return nil,false,err}
  if child==nil||child.ResultUsage()!=expected{return nil,false,fmt.Errorf("invalid XML retag child usage")}
 }
 if !changed{return e,false,nil}
 e.Info,e.children=info,slices.Clone(children)
 return e,true,e.ValidateStoredProvider()
}
