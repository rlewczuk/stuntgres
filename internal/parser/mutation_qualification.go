package parser

// Query qualification must not widen the separately selected DELETE grammar.
// Retained token positions reproduce the original parser boundary diagnostics.
func deleteQualification(expr Expr, inCast bool) *lexeme {
	var children []Expr
	switch e := expr.(type) {
	case ColumnExpr:
		if len(e.Name.Parts) > 2 { return &lexeme{raw:".",pos:e.ExtraQualificationPos} }
		if inCast && len(e.Name.Parts) > 1 { return &lexeme{raw:".",pos:e.QualificationPos} }
	case CastExpr:
		if column,ok := e.Child.(ColumnExpr); ok && len(column.Name.Parts) > 1 && e.Syntax == PostfixCast {
			return &lexeme{raw:"::",pos:e.Pos}
		}
		return deleteQualification(e.Child,true)
	case RegclassExpr: return deleteQualification(e.Child,true)
	case OperatorExpr: children = e.Args
	case FunctionExpr: children = e.Args
	case CallExpr: children = e.Args
	case LogicalExpr:
		if token := deleteQualification(e.Left,inCast); token != nil { return token }
		return deleteQualification(e.Right,inCast)
	case PredicateExpr:
		if token := deleteQualification(e.Left,inCast); token != nil { return token }
		return deleteQualification(e.Right,inCast)
	case ConditionalExpr:
		// Conditional arguments already used the DELETE operand grammar.
		inCast = false
		children = e.Args
	case CaseExpr:
		inCast = false
		if token := deleteQualification(e.Test,inCast); token != nil { return token }
		for _, arm := range e.Arms {
			if token := deleteQualification(arm.Condition,inCast); token != nil { return token }
			if token := deleteQualification(arm.Result,inCast); token != nil { return token }
		}
		return deleteQualification(e.Else,inCast)
	}
	for _, child := range children { if token := deleteQualification(child,inCast); token != nil { return token } }
	return nil
}

// Nonquery scalar entry points retain their raw dot/cast boundary. Direct
// UPDATE assignments and no-FROM projections used capability errors at the dot.
// Conditional arguments used DELETE operand grammar, retaining their later
// consumer diagnostic instead of a blanket qualification rejection.
func nonqueryQualification(expr Expr, assignment bool) (*lexeme,bool) {
	var children []Expr
	switch e:=expr.(type) {
	case ColumnExpr:
		if len(e.Name.Parts)>1 {return &lexeme{raw:".",pos:e.QualificationPos},assignment && !e.QualificationGrouped}
	case CastExpr:
		if column,ok:=e.Child.(ColumnExpr);ok && len(column.Name.Parts)>1 && e.Syntax==PostfixCast {
			if assignment && !column.QualificationGrouped {return &lexeme{raw:".",pos:column.QualificationPos},true}
			return &lexeme{raw:".",pos:column.QualificationPos},false
		}
		return deleteQualification(e,false),false
	case RegclassExpr:
		return deleteQualification(e.Child,true),false
	case ConditionalExpr,CaseExpr:
		return deleteQualification(expr,false),false
	case FunctionExpr:
		for _,child:=range e.Args {if token:=deleteQualification(child,true);token!=nil {return token,false}}
	case CallExpr:
		for _,child:=range e.Args {if token:=deleteQualification(child,true);token!=nil {return token,false}}
	case OperatorExpr:
		children=e.Args
	case LogicalExpr:
		if token,unsupported:=nonqueryQualification(e.Left,assignment);token!=nil {return token,unsupported}
		return nonqueryQualification(e.Right,assignment)
	case PredicateExpr:
		if token,unsupported:=nonqueryQualification(e.Left,assignment);token!=nil {return token,unsupported}
		return nonqueryQualification(e.Right,assignment)
	}
	for _,child:=range children {if token,unsupported:=nonqueryQualification(child,assignment);token!=nil {return token,unsupported}}
	return nil,false
}

func (l *lexer) nonqueryProjection(targets []Expr) bool {
	for _,target:=range targets {
		var token *lexeme
		var capability bool
		if star,ok:=target.(StarExpr);ok {
			if len(star.Qualifier.Parts)>0 {token=&lexeme{raw:".",pos:star.QualificationPos};capability=true}
		} else {token,capability=nonqueryQualification(target,true)}
		if token==nil {continue}
		if capability {l.failAt("0A000","qualified column references are unsupported",token.pos.Offset)} else {l.syntax(*token)}
		return false
	}
	return true
}

// Preserve grouping provenance only for raw qualifiers newly parsed by the
// shared scalar grammar. Conditional arguments already admitted qualification;
// their DELETE-operand policy remains authoritative and ignores this marker.
func groupedQualification(expr Expr) Expr {
	marked,_:=markGroupedQualification(expr)
	return marked
}

func markGroupedQualification(expr Expr) (Expr,bool) {
	switch e:=expr.(type) {
	case ColumnExpr:
		if len(e.Name.Parts)>1 && !e.QualificationGrouped {e.QualificationGrouped=true;return e,true}
	case OperatorExpr:
		// Parser-owned argument slices need no copy or parent replacement.
		for i,child:=range e.Args {e.Args[i],_=markGroupedQualification(child)}
	case CastExpr:
		child,changed:=markGroupedQualification(e.Child)
		if changed {e.Child=child;return e,true}
	case RegclassExpr:
		child,changed:=markGroupedQualification(e.Child)
		if changed {e.Child=child;return e,true}
	case LogicalExpr:
		left,lc:=markGroupedQualification(e.Left);right,rc:=markGroupedQualification(e.Right)
		if lc || rc {e.Left,e.Right=left,right;return e,true}
	case PredicateExpr:
		left,lc:=markGroupedQualification(e.Left);right,rc:=markGroupedQualification(e.Right)
		if lc || rc {e.Left,e.Right=left,right;return e,true}
	}
	return expr,false
}
