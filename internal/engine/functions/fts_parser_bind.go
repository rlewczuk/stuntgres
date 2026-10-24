package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func parserLabels(name string, oid bool) []string {
	first := "parser_name"
	if oid { first = "parser_oid" }
	if name == "ts_parse" { return []string{first,"txt"} }
	return []string{first}
}

func namedFTSParser(b CallTypeContext, raw parser.FunctionExpr, args []Argument, declared []sqltype.Usage) ([]sqltype.Usage, []sqltype.Usage, bool) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	count := 1
	if name == "ts_parse" { count = 2 }
	if raw.Variadic || len(args) != count || len(declared) != count || declared[0].Type == nil { return nil,nil,false }
	id := declared[0].Type.ID()
	if id != sqltype.Text && id != sqltype.Oid || count == 2 && (declared[1].Type == nil || declared[1].Type.ID() != sqltype.Text) { return nil,nil,false }
	return fixedNamedInputs(b,raw,args,declared,parserLabels(name,id==sqltype.Oid),count)
}

func bindFTSParser(b Binder, raw parser.FunctionExpr) (expr.Expr,error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	count := 1
	if name == "ts_parse" { count = 2 }
	var small [2]Argument
	operands := small[:0]
	if len(raw.Args) <= len(small) {
		operands = small[:len(raw.Args)]
	} else {
		operands = make([]Argument,len(raw.Args))
	}
	for i, child := range raw.Args {
		arg,err := b.AnalyzeArgument(child,AnalysisRequest{Intent:ComparisonArgument})
		if err != nil { return nil,err }
		operands[i] = arg
	}
	if err := ValidateCallSyntax(b,raw); err != nil { return nil,err }
	visible,err := b.BuiltinVisible(raw.Name)
	if err != nil { return nil,err }
	formal := sqltype.Text
	for _, label := range raw.ArgNames { if label.Text == "parser_oid" { formal = sqltype.Oid } }
	slots,valid := namedSlots(raw,parserLabels(name,formal==sqltype.Oid),count)
	if valid && len(operands)==count {
		for i,arg := range operands {
			if slots[i] == 0 && arg.Usage.Type != nil {
				if _,failure := sqltype.PlanCoercion(arg.Usage,b.Type(sqltype.Text).Usage(),sqltype.Implicit); failure != nil {
					formal = sqltype.Oid
				}
			}
		}
		slots,valid = namedSlots(raw,parserLabels(name,formal==sqltype.Oid),count)
	}
	valid = valid && visible && !raw.Variadic && !raw.ResolutionOnly && len(operands)==count
	if valid {
		for i,arg := range operands {
			id := sqltype.Text
			if slots[i] == 0 { id = formal }
			if arg.Usage.Type != nil {
				if _,failure := sqltype.PlanCoercion(arg.Usage,b.Type(id).Usage(),sqltype.Implicit); failure != nil { valid = false }
			}
		}
	}
	if !valid {
		names := make([]string,len(operands))
		for i,arg := range operands {
			names[i] = "unknown"
			if arg.Usage.Type != nil { names[i] = b.DiagnosticTypeName(arg.Usage.Type) }
			names[i] = NamedArgumentType(raw,i,names[i])
		}
		return nil,b.SignatureError(raw.Name,raw.Pos,names)
	}
	if raw.Star { return nil,expr.ErrorAt("42809",name+"(*) specified, but "+name+" is not an aggregate function",raw.Pos) }
	if err := CallDecorationError(raw); err != nil { return nil,err }
	if err := b.AdmitSetReturning(raw.Pos); err != nil { return nil,err }
	properties := expr.Properties{Volatility:expr.Immutable}
	if formal == sqltype.Text { properties.Volatility = expr.Stable }
	var args [2]expr.Expr
	var order [2]int
	for i,arg := range operands {
		id := sqltype.Text
		if slots[i] == 0 { id = formal }
		args[i],err = b.CoerceArgument(arg,CoercionRequest{Intent:ConditionalArgument,Target:b.Type(id).Usage(),Label:name})
		if err != nil { return nil,err }
		order[i] = slots[i]
		properties = expr.MergeProperties(properties,args[i].Properties())
	}
	members := []sqltype.CompositeMember{{Attribute:1,Name:"tokid",Usage:b.Type(sqltype.Int4).Usage()},{Attribute:2,Name:"token",Usage:b.Type(sqltype.Text).Usage()}}
	if count == 1 {
		members[1].Name = "alias"
		members = append(members,sqltype.CompositeMember{Attribute:3,Name:"description",Usage:b.Type(sqltype.Text).Usage()})
	}
	shape,err := sqltype.RecordShape(members)
	if err != nil { return nil,err }
	formals := [2]sqltype.ID{formal,sqltype.Text}
	info,err := b.BuiltinRoutineInfo(b.Info(shape.Usage(),raw.Pos,properties),FTSParserRoutineProvider,name,formals[:count]...)
	if err != nil { return nil,err }
	if formal == sqltype.Text {
		if err := b.CaptureFTSParserInput(); err != nil { return nil,err }
	}
	b.NoteCatalogSnapshot()
	b.NoteCatalogText()
	return FTSParser{Info:info,Args:args,Order:order,Parse:count==2,OID:formal==sqltype.Oid},nil
}
