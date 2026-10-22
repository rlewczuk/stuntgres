package analyze

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// FROM placement follows successful call completion, not lexical offsets.
// EnterQuery isolates this observation from expression-owned child queries.
type FromSetObservation struct {
	generation uint64
	pos        parser.Position
}

func nestedFromSetError(pos parser.Position) error {
	return expr.ErrorAt("0A000", "set-returning functions must appear at top level of FROM", pos)
}

func (b *Binder) BindFunctionSource(raw parser.FunctionExpr) (expr.Expr, error) {
	before := b.Clause.FromSetObservation
	node, err := b.BindFunction(raw)
	if err != nil {
		return nil, err
	}
	if inquiry, ok := node.(expr.SnapshotDemander); ok {
		demand, _, _ := inquiry.SnapshotDemand(plan.NullLiteral)
		b.Consumer.CatalogSnapshot = b.Consumer.CatalogSnapshot || demand
	}
	if !exec.SourceReturnsSet(node) && b.Clause.FromSetObservation != before {
		return nil, nestedFromSetError(b.Clause.FromSetObservation.pos)
	}
	return node, nil
}

func (b *Binder) stringSetAllowed(pos parser.Position) error {
	kind := b.Clause.AggregateClause
	if b.Consumer.DefaultConsumer {
		kind = "DEFAULT expressions"
	}
	if b.Consumer.CheckConsumer {
		kind = "check constraints"
	}
	if b.Target.GeneratedOwner != nil {
		kind = "column generation expressions"
	}
	if b.Consumer.IndexConsumer {
		kind = "index expressions"
	}
	if b.Consumer.IndexPredicateConsumer {
		kind = "index predicates"
	}
	hint := ""
	if b.Clause.StringSetGuard != "" {
		kind = b.Clause.StringSetGuard
		hint = "You might be able to move the set-returning function into a LATERAL FROM item."
	}
	message := "set-returning functions are not allowed in " + kind
	if b.Grouping.ArgumentOwner.Active && kind != "FILTER" {
		message = "aggregate function calls cannot contain set-returning function calls"
		if b.Grouping.ArgumentOwner.Window {
			message = "window function calls cannot contain set-returning function calls"
		}
		hint = "You might be able to move the set-returning function into a LATERAL FROM item."
	} else if b.Clause.StringSetGuard == "" {
		switch kind {
		case "SELECT", "GROUP BY", "ORDER BY", "DISTINCT ON", "window definitions", "FROM":
			return nil
		case "window RANGE", "window GROUPS", "window ROWS":
			message = "set-returning functions are not allowed in window definitions"
		case "INSERT VALUES":
			if b.Clause.stringSingleInsert {
				return nil
			}
			message = "set-returning functions are not allowed in VALUES"
		}
	}
	err := expr.ErrorAt("0A000", message, pos).(*sqlstate.Error)
	err.Hint = hint
	return err
}
func stringLateralError(q QueryQualifier, pos parser.Position, target bool) error {
	err := expr.ErrorAt("42P10", fmt.Sprintf(`invalid reference to FROM-clause entry for table "%s"`, q.Name), pos).(*sqlstate.Error)
	if target {
		err.Hint = fmt.Sprintf(`There is an entry for table "%s", but it cannot be referenced from this part of the query.`, q.Name)
	} else {
		err.Detail = "The combining JOIN type must be INNER or LEFT for a LATERAL reference."
	}
	return err
}
func (b *Binder) bindStringTable(raw *parser.FunctionTable) (plan.Node, QueryNamespace, error) {
	old, clause := b.Query.Namespace, b.Clause.AggregateClause
	if b.Query.Namespace == nil {
		b.Query.Namespace = &QueryNamespace{}
	}
	b.Clause.AggregateClause = "FROM"
	defer func() { b.Query.Namespace, b.Clause.AggregateClause = old, clause }()
	calls := make([]expr.Expr, len(raw.Calls))
	schema := make(plan.QuerySchema, len(calls))
	ordinal := b.Stored.functionCount
	b.Stored.functionCount += len(raw.Calls)
	for i, call := range raw.Calls {
		bound, err := b.BindFunctionSource(call)
		if err != nil {
			return nil, QueryNamespace{}, err
		}
		if len(call.ColumnDefinitions) != 0 {
			return nil, QueryNamespace{}, expr.ErrorAt("42601", FunctionColumnDefinitionMessage(bound), call.ColumnDefinitions[0].Name.Pos)
		}
		stringCall, stringOwner := bound.(functions.String)
		if !stringOwner || stringCall.Plan.Name() != "string_to_table" {
			return nil, QueryNamespace{}, expr.ErrorAt("0A000", "function table source is unsupported", call.Pos)
		}
		calls[i] = bound
		schema[i] = catalog.Column{Name: "string_to_table", Usage: bound.ResultUsage()}
		if b.Params.NoParameters || b.Consumer.frozenDefinition {
			if err := b.CaptureViewLiterals(call, bound, ViewFunctionLiteral, ordinal+i); err != nil {
				return nil, QueryNamespace{}, err
			}
		}
	}
	if raw.Ordinality {
		schema = append(schema, catalog.Column{Name: "ordinality", Usage: b.Type(sqltype.Int8).Usage()})
	}
	columns := make([]QueryColumn, len(schema))
	for i, col := range schema {
		columns[i] = QueryColumn{Descriptor: col, Slot: i, Leaf: -1, Physical: -1}
	}
	name := raw.Calls[0].Name.Parts[len(raw.Calls[0].Name.Parts)-1].Text
	// One scalar call remains scalar even in ROWS FROM; ordinality and
	// multiple calls declare tuples independently of their exposed labels.
	qualifier := QueryQualifier{Name: name, Original: name, Columns: columns, ScalarAlias: len(calls) == 1 && !raw.Ordinality}
	b.Query.Hidden = append(b.Query.Hidden, qualifier)
	captures := b.functionCaptures(calls)
	node := &plan.FunctionFromNode{QuerySchema: schema, Calls: calls, Ordinality: raw.Ordinality, Correlated: len(captures) != 0, Frame: b.FrameID(), ViewOrdinal: ordinal, Captures: captures}
	return node, QueryNamespace{Columns: columns, Qualifiers: []QueryQualifier{qualifier}, Frame: schema}, nil
}

// Only local expressions are rewritten; expression-owned queries retain their
// own stage/frame ownership through the existing mapper.
func (b *Binder) MapQuerySetStage(bound expr.Expr, leaf func(expr.Expr) (expr.Expr, bool, error)) (expr.Expr, error) {
	return b.Host.MapBoundExpression(b.BindingCatalog(), nil, bound, true, func(e expr.Expr) (expr.Expr, bool, error) {
		// Window slots are assigned through shared binder-owned pointers. A
		// local SRF rewrite must not clone them before their final slot shift.
		if _, ok := e.(*plan.WindowExpr); ok {
			return e, true, nil
		}
		return leaf(e)
	})
}

func functionFrameReference(bound expr.Expr, frame uint32) bool {
	found := false
	WalkQueryArgumentExpression(bound, func(node expr.Expr) {
		if column, ok := node.(plan.ColumnExpr); ok && column.Frame == frame {
			found = true
		}
	})
	return found
}
func stringForbiddenColumn(ns QueryNamespace, slot int, pos parser.Position) error {
	for _, qualifier := range ns.Qualifiers {
		for _, column := range qualifier.Columns {
			if column.Slot == slot {
				return stringLateralError(qualifier, pos, false)
			}
		}
	}
	return fmt.Errorf("stuntgres: missing forbidden function predecessor namespace")
}

func (b *Binder) stringFromContext() bool {
	if b.Clause.AggregateClause == "FROM" {
		return true
	}
	for _, scope := range b.Query.Scopes {
		if scope.Clause == "FROM" {
			return true
		}
	}
	return false
}

func (b *Binder) functionCaptures(calls []expr.Expr) []plan.FunctionCorrelation {
	var captures []plan.FunctionCorrelation
	for _, call := range calls {
		for _, arg := range call.(expr.Composite).Children() {
			WalkQueryArgumentExpression(arg, func(expr expr.Expr) {
				column, ok := expr.(plan.ColumnExpr)
				if !ok || column.Frame != b.FrameID() {
					return
				}
				name := ""
				for _, qualifier := range b.Query.Namespace.Qualifiers {
					for _, candidate := range qualifier.Columns {
						if candidate.Slot == column.Slot {
							name = qualifier.Name
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
