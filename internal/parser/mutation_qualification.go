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
	case InListExpr:
		absorb(e.Left)
		children = e.Items
	case ArrayComparisonExpr:
		absorb(e.Left)
		absorb(e.Right)
	case SubqueryExpr:
		absorb(e.Left) // Child queries own their qualification policy.
	case FieldExpr:
		absorb(e.Base)
	case RowExpr:
		children = e.Fields
	case GroupingExpr:
		children = e.Args
	case AggregateExpr:
		inCast = true
		for _, arg := range e.Args {
			absorb(arg)
		}
		for _, key := range e.Order {
			absorb(key.Expr)
		}
		absorb(e.Filter)
		if e.Window != nil {
			for _, key := range e.Window.Partition {
				absorb(key)
			}
			for _, key := range e.Window.Order {
				absorb(key.Expr)
			}
			for _, offset := range e.Window.Offsets() {
				absorb(offset)
			}
		}
		return result
	case ColumnExpr:
		if len(e.Name.Parts) > 1 {
			result.qualifier, result.qualified = e.QualificationPos, true
		}
		if inCast && result.qualified {
			result.reject(".", e.QualificationPos)
		} else if len(e.Name.Parts) > 2 {
			result.reject(".", e.ExtraQualificationPos)
		}
	case TimeZoneExpr:
		absorb(e.Value)
		if !e.Local {
			absorb(e.Zone)
		}
	case CollateExpr:
		absorb(e.Child)
	case CastExpr:
		if column, ok := e.Child.(ColumnExpr); ok && len(column.Name.Parts) > 1 && e.Syntax == PostfixCast {
			result = scanDeleteQualification(e.Child, false)
			result.reject("::", e.Pos)
			return result
		}
		return scanDeleteQualification(e.Child, true)
	case RegclassExpr:
		return scanDeleteQualification(e.Child, true)
	case ArrayExpr:
		for _, element := range e.Elements {
			absorb(element)
		}
	case SubscriptExpr:
		absorb(e.Base)
		for _, s := range e.Subscripts {
			absorb(s.Lower)
			absorb(s.Upper)
		}
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
	case SQLJSONExpr:
		inCast = true
		children = e.Args
	case JSONPredicateExpr:
		children = []Expr{e.Child}
	case FunctionExpr:
		inCast = true
		children = e.Args
		if e.Window != nil {
			for _, key := range e.Window.Partition {
				absorb(key)
			}
			for _, key := range e.Window.Order {
				absorb(key.Expr)
			}
			for _, offset := range e.Window.Offsets() {
				absorb(offset)
			}
		}
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
// UPDATE assignments used capability errors at the dot.
// Conditional arguments used DELETE operand grammar, retaining their later
// consumer diagnostic instead of a blanket qualification rejection.
func nonqueryQualification(expr Expr, assignment bool) (*lexeme, bool) {
	var children []Expr
	switch e := expr.(type) {
	case InListExpr:
		if token, reject := nonqueryQualification(e.Left, assignment); token != nil {
			return token, reject
		}
		children = e.Items
	case ArrayComparisonExpr:
		if token, reject := nonqueryQualification(e.Left, assignment); token != nil {
			return token, reject
		}
		return nonqueryQualification(e.Right, assignment)
	case SubqueryExpr:
		return nonqueryQualification(e.Left, assignment)
	case FieldExpr:
		return nonqueryQualification(e.Base, assignment)
	case RowExpr:
		children = e.Fields
	case GroupingExpr:
		children = e.Args
	case AggregateExpr:
		for _, arg := range e.Args {
			if token, reject := nonqueryQualification(arg, assignment); token != nil {
				return token, reject
			}
		}
		for _, key := range e.Order {
			if token, reject := nonqueryQualification(key.Expr, assignment); token != nil {
				return token, reject
			}
		}
		if e.Window != nil {
			for _, key := range e.Window.Partition {
				if token, reject := nonqueryQualification(key, assignment); token != nil {
					return token, reject
				}
			}
			for _, key := range e.Window.Order {
				if token, reject := nonqueryQualification(key.Expr, assignment); token != nil {
					return token, reject
				}
			}
			for _, offset := range e.Window.Offsets() {
				if token, reject := nonqueryQualification(offset, assignment); token != nil {
					return token, reject
				}
			}
		}
		return nonqueryQualification(e.Filter, assignment)
	case ArrayExpr:
		for _, element := range e.Elements {
			if token, reject := nonqueryQualification(element, assignment); token != nil {
				return token, reject
			}
		}
	case SubscriptExpr:
		var storage [13]Expr
		for _, child := range e.Children(&storage) {
			if token, reject := nonqueryQualification(child, assignment); token != nil {
				return token, reject
			}
		}
	case ColumnExpr:
		if len(e.Name.Parts) > 1 {
			return &lexeme{raw: ".", pos: e.QualificationPos}, assignment && !e.QualificationGrouped
		}
	case TimeZoneExpr:
		if token, reject := nonqueryQualification(e.Value, assignment); token != nil {
			return token, reject
		}
		if !e.Local {
			return nonqueryQualification(e.Zone, assignment)
		}
	case CollateExpr:
		return nonqueryQualification(e.Child, assignment)
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
	case SQLJSONExpr:
		for _, child := range e.Args {
			if token := deleteQualification(child, true); token != nil {
				return token, false
			}
		}
	case JSONPredicateExpr:
		return nonqueryQualification(e.Child, assignment)
	case FunctionExpr:
		if e.Window != nil {
			for _, key := range e.Window.Partition {
				if token := deleteQualification(key, true); token != nil {
					return token, false
				}
			}
			for _, key := range e.Window.Order {
				if token := deleteQualification(key.Expr, true); token != nil {
					return token, false
				}
			}
			for _, offset := range e.Window.Offsets() {
				if token := deleteQualification(offset, true); token != nil {
					return token, false
				}
			}
		}
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

// Preserve grouping provenance only for raw qualifiers newly parsed by the
// shared scalar grammar. Conditional arguments already admitted qualification;
// their DELETE-operand policy remains authoritative and ignores this marker.
func groupedQualification(expr Expr) Expr {
	marked, _ := markGroupedQualification(expr)
	return marked
}

func markGroupedQualification(expr Expr) (Expr, bool) {
	switch e := expr.(type) {
	case InListExpr:
		left, changed := markGroupedQualification(e.Left)
		e.Left = left
		var items []Expr
		for i, item := range e.Items {
			next, did := markGroupedQualification(item)
			if did {
				if items == nil {
					items = append([]Expr(nil), e.Items...)
				}
				items[i] = next
				changed = true
			}
		}
		if items != nil {
			e.Items = items
		}
		if changed {
			return e, true
		}
	case ArrayComparisonExpr:
		left, lc := markGroupedQualification(e.Left)
		right, rc := markGroupedQualification(e.Right)
		if lc || rc {
			e.Left, e.Right = left, right
			return e, true
		}
	case SubqueryExpr:
		left, changed := markGroupedQualification(e.Left)
		if changed {
			e.Left = left
			return e, true
		}
	case FieldExpr:
		base, changed := markGroupedQualification(e.Base)
		if changed {
			e.Base = base
			return e, true
		}
	case RowExpr:
		for i, field := range e.Fields {
			e.Fields[i], _ = markGroupedQualification(field)
		}
	case GroupingExpr:
		e.Args = append([]Expr(nil), e.Args...)
		for i, child := range e.Args {
			e.Args[i], _ = markGroupedQualification(child)
		}
		return e, true
	case AggregateExpr:
		for i, child := range e.Args {
			e.Args[i], _ = markGroupedQualification(child)
		}
		for i, key := range e.Order {
			e.Order[i].Expr, _ = markGroupedQualification(key.Expr)
		}
		if e.Window != nil {
			for i, key := range e.Window.Partition {
				e.Window.Partition[i], _ = markGroupedQualification(key)
			}
			for i, key := range e.Window.Order {
				e.Window.Order[i].Expr, _ = markGroupedQualification(key.Expr)
			}
			markWindowFrameQualification(e.Window)
		}
		if e.Filter != nil {
			filter, changed := markGroupedQualification(e.Filter)
			if changed {
				e.Filter = filter
				return e, true
			}
		}
	case SQLJSONExpr:
		for i, child := range e.Args {
			e.Args[i], _ = markGroupedQualification(child)
		}
	case JSONPredicateExpr:
		e.Child, _ = markGroupedQualification(e.Child)
		return e, true
	case FunctionExpr:
		for i, child := range e.Args {
			e.Args[i], _ = markGroupedQualification(child)
		}
		if e.Window != nil {
			for i, child := range e.Window.Partition {
				e.Window.Partition[i], _ = markGroupedQualification(child)
			}
			for i, key := range e.Window.Order {
				e.Window.Order[i].Expr, _ = markGroupedQualification(key.Expr)
			}
			markWindowFrameQualification(e.Window)
		}
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
	case TimeZoneExpr:
		value, changed := markGroupedQualification(e.Value)
		e.Value = value
		if !e.Local {
			zone, did := markGroupedQualification(e.Zone)
			e.Zone = zone
			changed = changed || did
		}
		if changed {
			return e, true
		}
	case CollateExpr:
		child, changed := markGroupedQualification(e.Child)
		if changed {
			e.Child = child
			return e, true
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
	case ArrayExpr:
		var elements []Expr
		for i, element := range e.Elements {
			next, changed := markGroupedQualification(element)
			if changed {
				if elements == nil {
					elements = append([]Expr(nil), e.Elements...)
				}
				elements[i] = next
			}
		}
		if elements != nil {
			e.Elements = elements
			return e, true
		}
	case SubscriptExpr:
		base, changed := markGroupedQualification(e.Base)
		e.Base = base
		var subs []ArraySubscript
		for i, s := range e.Subscripts {
			lower, lc := markGroupedQualification(s.Lower)
			upper, uc := markGroupedQualification(s.Upper)
			if lc || uc {
				if subs == nil {
					subs = append([]ArraySubscript(nil), e.Subscripts...)
				}
				s.Lower, s.Upper = lower, upper
				subs[i] = s
				changed = true
			}
		}
		if subs != nil {
			e.Subscripts = subs
		}
		if changed {
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
