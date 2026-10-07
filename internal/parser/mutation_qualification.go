package parser

// Query qualification must not widen the separately selected DELETE grammar.
// Retained token positions reproduce the original parser boundary diagnostics.
func deleteQualification(expr Expr, inCast bool) *lexeme {
	result := scanDeleteQualification(expr, inCast)
	if result.errorRaw == "" {
		return nil
	}
	return &lexeme{raw: result.errorRaw, pos: result.errorPos}
}

type deleteQualificationResult struct {
	errorRaw            string
	errorPos, qualifier Position
	qualified           bool
}

func (r *deleteQualificationResult) reject(raw string, pos Position) {
	if r.errorRaw == "" || pos.Offset < r.errorPos.Offset {
		r.errorRaw, r.errorPos = raw, pos
	}
}
func scanDeleteQualification(expr Expr, inCast bool) deleteQualificationResult {
	var result deleteQualificationResult
	absorb := func(child Expr) {
		r := scanDeleteQualification(child, inCast)
		if r.errorRaw != "" {
			result.reject(r.errorRaw, r.errorPos)
		}
		if r.qualified && (!result.qualified || r.qualifier.Offset < result.qualifier.Offset) {
			result.qualifier, result.qualified = r.qualifier, true
		}
	}
	var children []Expr
	hideQualification := false
	switch e := expr.(type) {
	case ColumnExpr:
		if len(e.Name.Parts) > 1 {
			result.qualifier, result.qualified = e.QualificationPos, true
		}
		if inCast && result.qualified {
			result.reject(".", e.QualificationPos)
		} else if len(e.Name.Parts) > 2 {
			result.reject(".", e.ExtraQualificationPos)
		}
	case CastExpr:
		if column, ok := e.Child.(ColumnExpr); ok && len(column.Name.Parts) > 1 && e.Syntax == PostfixCast {
			result = scanDeleteQualification(e.Child, false)
			result.reject("::", e.Pos)
			return result
		}
		return scanDeleteQualification(e.Child, true)
	case RegclassExpr:
		return scanDeleteQualification(e.Child, true)
	case OperatorExpr:
		for _, child := range e.Args {
			absorb(child)
		}
		// Qualified DELETE operands were separate from scalar arithmetic.
		// A leading qualified operand failed at the operator; qualification
		// within an already-started scalar failed at its first dot.
		if len(e.Name.Parts) == 1 && result.qualified {
			symbol := e.Name.Parts[0].Text
			switch symbol {
			case "=", "<>", "!=", "<", ">", "<=", ">=":
			default:
				if result.qualifier.Offset < e.Pos.Offset {
					result.reject(symbol, e.Pos)
				} else {
					result.reject(".", result.qualifier)
				}
			}
		}
	case FunctionExpr:
		inCast = true
		children = e.Args
	case CallExpr:
		inCast = true
		children = e.Args
	case LogicalExpr:
		absorb(e.Left)
		absorb(e.Right)
	case PredicateExpr:
		absorb(e.Left)
		absorb(e.Right)
	case ConditionalExpr:
		inCast = false
		children = e.Args
		hideQualification = true
	case CaseExpr:
		inCast = false
		hideQualification = true
		absorb(e.Test)
		for _, arm := range e.Arms {
			absorb(arm.Condition)
			absorb(arm.Result)
		}
		absorb(e.Else)
	}
	for _, child := range children {
		absorb(child)
	}
	if hideQualification {
		result.qualified = false
	}
	return result
}

// Nonquery scalar entry points retain their raw dot/cast boundary. Direct
// UPDATE assignments and no-FROM projections used capability errors at the dot.
// Conditional arguments used DELETE operand grammar, retaining their later
// consumer diagnostic instead of a blanket qualification rejection.
func nonqueryQualification(expr Expr, assignment bool) (*lexeme, bool) {
	var children []Expr
	switch e := expr.(type) {
	case ColumnExpr:
		if len(e.Name.Parts) > 1 {
			return &lexeme{raw: ".", pos: e.QualificationPos}, assignment && !e.QualificationGrouped
		}
	case CastExpr:
		if column, ok := e.Child.(ColumnExpr); ok && len(column.Name.Parts) > 1 && e.Syntax == PostfixCast {
			if assignment && !column.QualificationGrouped {
				return &lexeme{raw: ".", pos: column.QualificationPos}, true
			}
			return &lexeme{raw: ".", pos: column.QualificationPos}, false
		}
		return deleteQualification(e, false), false
	case RegclassExpr:
		return deleteQualification(e.Child, true), false
	case ConditionalExpr, CaseExpr:
		return deleteQualification(expr, false), false
	case FunctionExpr:
		for _, child := range e.Args {
			if token := deleteQualification(child, true); token != nil {
				return token, false
			}
		}
	case CallExpr:
		for _, child := range e.Args {
			if token := deleteQualification(child, true); token != nil {
				return token, false
			}
		}
	case OperatorExpr:
		children = e.Args
	case LogicalExpr:
		if token, unsupported := nonqueryQualification(e.Left, assignment); token != nil {
			return token, unsupported
		}
		return nonqueryQualification(e.Right, assignment)
	case PredicateExpr:
		if token, unsupported := nonqueryQualification(e.Left, assignment); token != nil {
			return token, unsupported
		}
		return nonqueryQualification(e.Right, assignment)
	}
	for _, child := range children {
		if token, unsupported := nonqueryQualification(child, assignment); token != nil {
			return token, unsupported
		}
	}
	return nil, false
}

func (l *lexer) nonqueryProjection(targets []Expr) bool {
	for _, target := range targets {
		var token *lexeme
		var capability bool
		if star, ok := target.(StarExpr); ok {
			if len(star.Qualifier.Parts) > 0 {
				token = &lexeme{raw: ".", pos: star.QualificationPos}
				capability = true
			}
		} else {
			token, capability = nonqueryQualification(target, true)
		}
		if token == nil {
			continue
		}
		if capability {
			l.failAt("0A000", "qualified column references are unsupported", token.pos.Offset)
		} else {
			l.syntax(*token)
		}
		return false
	}
	return true
}

// Preserve grouping provenance only for raw qualifiers newly parsed by the
// shared scalar grammar. Conditional arguments already admitted qualification;
// their DELETE-operand policy remains authoritative and ignores this marker.
func groupedQualification(expr Expr) Expr {
	marked, _ := markGroupedQualification(expr)
	return marked
}

func markGroupedQualification(expr Expr) (Expr, bool) {
	switch e := expr.(type) {
	case ColumnExpr:
		if len(e.Name.Parts) > 1 && !e.QualificationGrouped {
			e.QualificationGrouped = true
			return e, true
		}
	case OperatorExpr:
		// Parser-owned argument slices need no copy or parent replacement.
		for i, child := range e.Args {
			e.Args[i], _ = markGroupedQualification(child)
		}
	case CastExpr:
		child, changed := markGroupedQualification(e.Child)
		if changed {
			e.Child = child
			return e, true
		}
	case RegclassExpr:
		child, changed := markGroupedQualification(e.Child)
		if changed {
			e.Child = child
			return e, true
		}
	case LogicalExpr:
		left, lc := markGroupedQualification(e.Left)
		right, rc := markGroupedQualification(e.Right)
		if lc || rc {
			e.Left, e.Right = left, right
			return e, true
		}
	case PredicateExpr:
		left, lc := markGroupedQualification(e.Left)
		right, rc := markGroupedQualification(e.Right)
		if lc || rc {
			e.Left, e.Right = left, right
			return e, true
		}
	}
	return expr, false
}
