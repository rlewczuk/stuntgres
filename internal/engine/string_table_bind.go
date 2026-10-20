package engine

import (
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Selected rendering and binding share a lane; typed admission remains owned
// by the node's SourceAdmission after ordinary function resolution.
func selectedFunctionSourceName(name string) bool {
	return name == "jsonb_path_query" || parser.RegexpFunctionName(name) || parser.JSONFunctionName(name) ||
		parser.CatalogRelationFunctionName(name) ||
		parser.ObjectAddressFunctionName(name) || parser.CommitTimestampFunctionName(name) || parser.TransactionIdentityFunctionName(name) ||
		name == "age" || name == "aclexplode" || name == "acldefault" || name == "makeaclitem" || name == "int_array_enum" || name == "generate_series" || name == "generate_subscripts" || name == "unnest" || name == "multirange"
}

// A selected scalar OUT name controls the column label, not the range alias.
func sourceOutName(e expr.Expr) string { return expr.SourceOutName(e) }

// FROM placement follows successful call completion, not lexical offsets.
// enterQuery isolates this observation from expression-owned child queries.
type fromSetObservation struct {
	generation uint64
	pos        parser.Position
}

func nestedFromSetError(pos parser.Position) error {
	return expr.ErrorAt("0A000", "set-returning functions must appear at top level of FROM", pos)
}

func (b *expressionBinder) bindFunctionSource(raw parser.FunctionExpr) (expr.Expr, error) {
	before := b.clause.fromSetObservation
	node, err := b.bindFunction(raw)
	if err != nil {
		return nil, err
	}
	if inquiry, ok := node.(expr.SnapshotDemander); ok {
		demand, _, _ := inquiry.SnapshotDemand(bareInquiryLiteralNull)
		b.consumer.catalogSnapshot = b.consumer.catalogSnapshot || demand
	}
	if !exec.SourceReturnsSet(node) && b.clause.fromSetObservation != before {
		return nil, nestedFromSetError(b.clause.fromSetObservation.pos)
	}
	return node, nil
}

func (b *expressionBinder) stringSetAllowed(pos parser.Position) error {
	kind := b.clause.aggregateClause
	if b.consumer.defaultConsumer {
		kind = "DEFAULT expressions"
	}
	if b.consumer.checkConsumer {
		kind = "check constraints"
	}
	if b.target.generatedOwner != nil {
		kind = "column generation expressions"
	}
	if b.consumer.indexConsumer {
		kind = "index expressions"
	}
	if b.consumer.indexPredicateConsumer {
		kind = "index predicates"
	}
	hint := ""
	if b.clause.stringSetGuard != "" {
		kind = b.clause.stringSetGuard
		hint = "You might be able to move the set-returning function into a LATERAL FROM item."
	}
	message := "set-returning functions are not allowed in " + kind
	if b.grouping.argumentOwner.active && kind != "FILTER" {
		message = "aggregate function calls cannot contain set-returning function calls"
		if b.grouping.argumentOwner.window {
			message = "window function calls cannot contain set-returning function calls"
		}
		hint = "You might be able to move the set-returning function into a LATERAL FROM item."
	} else if b.clause.stringSetGuard == "" {
		switch kind {
		case "SELECT", "GROUP BY", "ORDER BY", "DISTINCT ON", "window definitions", "FROM":
			return nil
		case "window RANGE", "window GROUPS", "window ROWS":
			message = "set-returning functions are not allowed in window definitions"
		case "INSERT VALUES":
			if b.clause.stringSingleInsert {
				return nil
			}
			message = "set-returning functions are not allowed in VALUES"
		}
	}
	err := expr.ErrorAt("0A000", message, pos).(*sqlstate.Error)
	err.Hint = hint
	return err
}
func stringLateralError(q queryQualifier, pos parser.Position, target bool) error {
	err := expr.ErrorAt("42P10", fmt.Sprintf(`invalid reference to FROM-clause entry for table "%s"`, q.name), pos).(*sqlstate.Error)
	if target {
		err.Hint = fmt.Sprintf(`There is an entry for table "%s", but it cannot be referenced from this part of the query.`, q.name)
	} else {
		err.Detail = "The combining JOIN type must be INNER or LEFT for a LATERAL reference."
	}
	return err
}
func (b *expressionBinder) bindStringTable(raw *parser.FunctionTable) (plan.Node, queryNamespace, error) {
	old, clause := b.query.namespace, b.clause.aggregateClause
	if b.query.namespace == nil {
		b.query.namespace = &queryNamespace{}
	}
	b.clause.aggregateClause = "FROM"
	defer func() { b.query.namespace, b.clause.aggregateClause = old, clause }()
	calls := make([]expr.Expr, len(raw.Calls))
	schema := make(plan.QuerySchema, len(calls))
	ordinal := b.stored.functionCount
	b.stored.functionCount += len(raw.Calls)
	for i, call := range raw.Calls {
		bound, err := b.bindFunctionSource(call)
		if err != nil {
			return nil, queryNamespace{}, err
		}
		if len(call.ColumnDefinitions) != 0 {
			return nil, queryNamespace{}, expr.ErrorAt("42601", functionColumnDefinitionMessage(bound), call.ColumnDefinitions[0].Name.Pos)
		}
		stringCall, stringOwner := bound.(functions.String)
		if !stringOwner || stringCall.Plan.Name() != "string_to_table" {
			return nil, queryNamespace{}, expr.ErrorAt("0A000", "function table source is unsupported", call.Pos)
		}
		calls[i] = bound
		schema[i] = catalog.Column{Name: "string_to_table", Usage: bound.ResultUsage()}
		if b.params.noParameters || b.consumer.materializedDefinition {
			if err := b.captureViewLiterals(call, bound, analyze.ViewFunctionLiteral, ordinal+i); err != nil {
				return nil, queryNamespace{}, err
			}
		}
	}
	if raw.Ordinality {
		schema = append(schema, catalog.Column{Name: "ordinality", Usage: b.typ(sqltype.Int8).Usage()})
	}
	columns := make([]queryColumn, len(schema))
	for i, col := range schema {
		columns[i] = queryColumn{descriptor: col, slot: i, leaf: -1, physical: -1}
	}
	name := raw.Calls[0].Name.Parts[len(raw.Calls[0].Name.Parts)-1].Text
	// One scalar call remains scalar even in ROWS FROM; ordinality and
	// multiple calls declare tuples independently of their exposed labels.
	qualifier := queryQualifier{name: name, original: name, columns: columns, scalarAlias: len(calls) == 1 && !raw.Ordinality}
	b.query.hidden = append(b.query.hidden, qualifier)
	captures := b.functionCaptures(calls)
	node := &plan.FunctionFromNode{QuerySchema: schema, Calls: calls, Ordinality: raw.Ordinality, Correlated: len(captures) != 0, Frame: b.frameID(), ViewOrdinal: ordinal, Captures: captures}
	return node, queryNamespace{columns: columns, qualifiers: []queryQualifier{qualifier}, frame: schema}, nil
}

// Only local expressions are rewritten; expression-owned queries retain their
// own stage/frame ownership through the existing mapper.
func (b *expressionBinder) mapQuerySetStage(bound expr.Expr, leaf func(expr.Expr) (expr.Expr, bool, error)) (expr.Expr, error) {
	mapped, _, err := mapBoundExpressionReader(b.bindingCatalog(), nil, bound, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, func(n plan.Node) (plan.Node, error) { return n, nil }, nil, true, func(e expr.Expr) (expr.Expr, bool, error) {
		// Window slots are assigned through shared binder-owned pointers. A
		// local SRF rewrite must not clone them before their final slot shift.
		if _, ok := e.(*plan.WindowExpr); ok {
			return e, true, nil
		}
		return leaf(e)
	})
	return mapped, err
}

func functionFrameReference(bound expr.Expr, frame uint32) bool {
	found := false
	walkQueryArgumentExpression(bound, func(node expr.Expr) {
		if column, ok := node.(plan.ColumnExpr); ok && column.Frame == frame {
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
	if b.clause.aggregateClause == "FROM" {
		return true
	}
	for _, scope := range b.query.scopes {
		if scope.clause == "FROM" {
			return true
		}
	}
	return false
}

func (b *expressionBinder) functionCaptures(calls []expr.Expr) []plan.FunctionCorrelation {
	var captures []plan.FunctionCorrelation
	for _, call := range calls {
		for _, arg := range call.(expr.Composite).Children() {
			walkQueryArgumentExpression(arg, func(expr expr.Expr) {
				column, ok := expr.(plan.ColumnExpr)
				if !ok || column.Frame != b.frameID() {
					return
				}
				name := ""
				for _, qualifier := range b.query.namespace.qualifiers {
					for _, candidate := range qualifier.columns {
						if candidate.slot == column.Slot {
							name = qualifier.name
							break
						}
					}
					if name != "" {
						break
					}
				}
				captures = append(captures, plan.FunctionCorrelation{Frame: column.Frame, Slot: column.Slot, Name: name, Pos: column.Pos})
			})
		}
	}
	return captures
}
