package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func isStringSet(expr BoundExpr) bool {
	e, ok := expr.(stringExpression)
	return ok && e.plan.Name() == "string_to_table"
}
func isQuerySet(expr BoundExpr) bool {
	switch expr.(type) {
	case regexpSetExpression, jsonKeysExpression, seriesExpression:
		return true
	}
	return isStringSet(expr)
}
func validFunctionSourceCall(expr BoundExpr) bool {
	switch call := expr.(type) {
	case stringExpression:
		return isStringSet(call)
	case regexpSetExpression:
		return call.plan.ReturnsSet()
	case jsonKeysExpression:
		return true
	case binaryExpression:
		return parser.JSONFunctionName(call.plan.Name())
	case regexpExpression:
		return !call.plan.ReturnsSet()
	default:
		return false
	}
}
func functionCallArguments(expr BoundExpr) []BoundExpr {
	switch call := expr.(type) {
	case seriesExpression:
		return call.args
	case stringExpression:
		return call.args
	case regexpSetExpression:
		return call.args
	case jsonKeysExpression:
		return call.args
	case binaryExpression:
		return call.args
	case regexpExpression:
		return call.args
	default:
		return nil
	}
}
func withFunctionCallArguments(expr BoundExpr, args []BoundExpr) (BoundExpr, error) {
	switch call := expr.(type) {
	case seriesExpression:
		if len(args) != len(call.args) {
			return nil, fmt.Errorf("stuntgres: invalid series arguments")
		}
		call.args = args
		return call, nil
	case stringExpression:
		call.args = args
		return call, nil
	case regexpSetExpression:
		call.args = args
		return call, nil
	case jsonKeysExpression:
		if len(args) != 1 {
			return nil, fmt.Errorf("stuntgres: invalid JSON keys arguments")
		}
		call.args = args
		return call, nil
	case binaryExpression:
		call.args = args
		return call, nil
	case regexpExpression:
		call.args = args
		return call, nil
	default:
		return nil, fmt.Errorf("stuntgres: invalid closed function call")
	}
}

func (b *expressionBinder) stringSetAllowed(pos parser.Position) error {
	kind := b.aggregateClause
	if b.defaultConsumer {
		kind = "DEFAULT expressions"
	}
	if b.checkConsumer {
		kind = "check constraints"
	}
	if b.generatedOwner != nil {
		kind = "column generation expressions"
	}
	if b.indexConsumer {
		kind = "index expressions"
	}
	if b.indexPredicateConsumer {
		kind = "index predicates"
	}
	hint := ""
	if b.stringSetGuard != "" {
		kind = b.stringSetGuard
		hint = "You might be able to move the set-returning function into a LATERAL FROM item."
	}
	message := "set-returning functions are not allowed in " + kind
	if b.argumentOwner.active && kind != "FILTER" {
		message = "aggregate function calls cannot contain set-returning function calls"
		if b.argumentOwner.window {
			message = "window function calls cannot contain set-returning function calls"
		}
		hint = "You might be able to move the set-returning function into a LATERAL FROM item."
	} else if b.stringSetGuard == "" {
		switch kind {
		case "SELECT", "GROUP BY", "ORDER BY", "DISTINCT ON", "window definitions", "FROM":
			return nil
		case "window RANGE", "window GROUPS", "window ROWS":
			message = "set-returning functions are not allowed in window definitions"
		case "INSERT VALUES":
			if b.stringSingleInsert {
				return nil
			}
			message = "set-returning functions are not allowed in VALUES"
		}
	}
	err := expressionError("0A000", message, pos).(*sqlstate.Error)
	err.Hint = hint
	return err
}
func stringLateralError(q queryQualifier, pos parser.Position, target bool) error {
	err := expressionError("42P10", fmt.Sprintf(`invalid reference to FROM-clause entry for table "%s"`, q.name), pos).(*sqlstate.Error)
	if target {
		err.Hint = fmt.Sprintf(`There is an entry for table "%s", but it cannot be referenced from this part of the query.`, q.name)
	} else {
		err.Detail = "The combining JOIN type must be INNER or LEFT for a LATERAL reference."
	}
	return err
}
func (b *expressionBinder) bindStringTable(raw *parser.FunctionTable) (LogicalNode, queryNamespace, error) {
	old, clause := b.queryNamespace, b.aggregateClause
	if b.queryNamespace == nil {
		b.queryNamespace = &queryNamespace{}
	}
	b.aggregateClause = "FROM"
	defer func() { b.queryNamespace, b.aggregateClause = old, clause }()
	calls := make([]BoundExpr, len(raw.Calls))
	schema := make(querySchema, len(calls))
	ordinal := b.viewFunctionCount
	b.viewFunctionCount += len(raw.Calls)
	for i, call := range raw.Calls {
		expr, err := b.bindFunction(call)
		if err != nil {
			return nil, queryNamespace{}, err
		}
		var nested BoundExpr
		boundExpressionChildren(expr, func(arg BoundExpr) {
			walkBoundExpression(arg, func(e BoundExpr) {
				if isQuerySet(e) {
					nested = e
				}
			})
		})
		if !isStringSet(expr) || nested != nil {
			if nested != nil {
				return nil, queryNamespace{}, expressionError("0A000", "set-returning functions must appear at top level of FROM", nested.SourcePosition())
			}
			return nil, queryNamespace{}, expressionError("0A000", "function table source is unsupported", call.Pos)
		}
		calls[i] = expr
		schema[i] = ColumnDescriptor{Name: "string_to_table", Usage: expr.ResultUsage()}
		if b.noParameters || b.materializedDefinition {
			if err := b.captureViewLiterals(call, expr, viewFunctionLiteral, ordinal+i); err != nil {
				return nil, queryNamespace{}, err
			}
		}
	}
	if raw.Ordinality {
		schema = append(schema, ColumnDescriptor{Name: "ordinality", Usage: b.typ(sqltype.Int8).Usage()})
	}
	columns := make([]queryColumn, len(schema))
	for i, col := range schema {
		columns[i] = queryColumn{descriptor: col, slot: i, leaf: -1, physical: -1}
	}
	name := raw.Calls[0].Name.Parts[len(raw.Calls[0].Name.Parts)-1].Text
	// One scalar call remains scalar even in ROWS FROM; ordinality and
	// multiple calls declare tuples independently of their exposed labels.
	qualifier := queryQualifier{name: name, original: name, columns: columns, scalarAlias: len(calls) == 1 && !raw.Ordinality}
	b.queryHidden = append(b.queryHidden, qualifier)
	captures := b.functionCaptures(calls)
	node := &functionFromNode{querySchema: schema, calls: calls, ordinality: raw.Ordinality, correlated: len(captures) != 0, frame: b.frameID(), viewOrdinal: ordinal, captures: captures}
	return node, queryNamespace{columns: columns, qualifiers: []queryQualifier{qualifier}, frame: schema}, nil
}

// Only local expressions are rewritten; expression-owned queries retain their
// own stage/frame ownership through the existing mapper.
func (b *expressionBinder) mapQuerySetStage(expr BoundExpr, leaf func(BoundExpr) (BoundExpr, bool, error)) (BoundExpr, error) {
	mapped, _, err := mapBoundExpression(b.bindingCatalog(), expr, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, func(n LogicalNode) (LogicalNode, error) { return n, nil }, func(e BoundExpr) (BoundExpr, bool, error) {
		// Window slots are assigned through shared binder-owned pointers. A
		// local SRF rewrite must not clone them before their final slot shift.
		if _, ok := e.(*windowExpression); ok {
			return e, true, nil
		}
		return leaf(e)
	})
	return mapped, err
}

func functionFrameReference(expr BoundExpr, frame uint32) bool {
	found := false
	walkQueryArgumentExpression(expr, func(node BoundExpr) {
		if column, ok := node.(columnExpression); ok && column.frame == frame {
			found = true
		}
	})
	return found
}
func stringForbiddenColumn(ns queryNamespace, slot int, pos parser.Position) error {
	for _, qualifier := range ns.qualifiers {
		for _, column := range qualifier.columns {
			if column.slot == slot {
				return stringLateralError(qualifier, pos, false)
			}
		}
	}
	return fmt.Errorf("stuntgres: missing forbidden function predecessor namespace")
}

func (b *expressionBinder) stringFromContext() bool {
	if b.aggregateClause == "FROM" {
		return true
	}
	for _, scope := range b.queryScopes {
		if scope.clause == "FROM" {
			return true
		}
	}
	return false
}

func (b *expressionBinder) functionCaptures(calls []BoundExpr) []functionCorrelation {
	var captures []functionCorrelation
	for _, call := range calls {
		for _, arg := range functionCallArguments(call) {
			walkQueryArgumentExpression(arg, func(expr BoundExpr) {
				column, ok := expr.(columnExpression)
				if !ok || column.frame != b.frameID() {
					return
				}
				name := ""
				for _, qualifier := range b.queryNamespace.qualifiers {
					for _, candidate := range qualifier.columns {
						if candidate.slot == column.slot {
							name = qualifier.name
							break
						}
					}
					if name != "" {
						break
					}
				}
				captures = append(captures, functionCorrelation{frame: column.frame, slot: column.slot, name: name, pos: column.pos})
			})
		}
	}
	return captures
}
