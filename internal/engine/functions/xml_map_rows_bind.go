package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func xmlMappingOperation(name string)XMLMappingOperation{for i,n:=range xmlMappingNames{if n==name{return XMLMappingOperation(i)}};return XMLMappingOperation(255)}
func namedXMLMapping(b CallTypeContext,raw parser.FunctionExpr,args []Argument,declared []sqltype.Usage)([]sqltype.Usage,[]sqltype.Usage,bool){
	op:=xmlMappingOperation(raw.Name.Parts[len(raw.Name.Parts)-1].Text)
	if op.Name()==""||len(declared)!=op.Arity(){return nil,nil,false}
	for i,d:=range declared{if d.Type==nil||d.Type.ID()!=op.formal(i){return nil,nil,false}}
	return fixedNamedInputs(b,raw,args,declared,op.labels(),op.Arity())
}
func bindXMLMapping(b Binder,raw parser.FunctionExpr)(expr.Expr,error){
	op:=xmlMappingOperation(raw.Name.Parts[len(raw.Name.Parts)-1].Text)
	var small [5]Argument
	operands:=small[:0]
	if len(raw.Args)<=len(small){operands=small[:len(raw.Args)]}else{operands=make([]Argument,len(raw.Args))}
	for i,child:=range raw.Args{arg,err:=b.AnalyzeArgument(child,AnalysisRequest{Intent:ComparisonArgument});if err!=nil{return nil,err};operands[i]=arg}
	if err:=ValidateCallSyntax(b,raw);err!=nil{return nil,err}
	visible,err:=b.BuiltinVisible(raw.Name);if err!=nil{return nil,err}
	valid:=visible&&op.Name()!=""&&len(operands)==op.Arity()&&!raw.Variadic&&!raw.ResolutionOnly
	var slots [7]int
	if valid{slots,valid=namedSlots(raw,op.labels(),op.Arity())}
	if valid{for i,arg:=range operands{if arg.Usage.Type!=nil{if _,err:=sqltype.PlanCoercion(arg.Usage,b.Type(op.formal(slots[i])).Usage(),sqltype.Implicit);err!=nil{valid=false}}}}
	if !valid{names:=make([]string,len(operands));for i,arg:=range operands{names[i]="unknown";if arg.Usage.Type!=nil{names[i]=b.DiagnosticTypeName(arg.Usage.Type)};names[i]=NamedArgumentType(raw,i,names[i])};return nil,b.SignatureError(raw.Name,raw.Pos,names)}
	if err:=CallDecorationError(raw);err!=nil{return nil,err}
	n:=XMLMapping{Operation:op};properties:=expr.Properties{Volatility:n.IntrinsicVolatility()};var formals [5]sqltype.ID
	for i,arg:=range operands{slot:=slots[i];n.Order[i]=slot;formals[slot]=op.formal(slot);n.Args[i],err=b.CoerceArgument(arg,CoercionRequest{Intent:ConditionalArgument,Target:b.Type(formals[slot]).Usage(),Label:op.Name()});if err!=nil{return nil,err};properties=expr.MergeProperties(properties,n.Args[i].Properties())}
	n.Info,err=b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.XML).Usage(),raw.Pos,properties),XMLMappingRoutineProvider,op.Name(),formals[:op.Arity()]...)
	if err!=nil{return nil,err};return n,n.Validate()
}
