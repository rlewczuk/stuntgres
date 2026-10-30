package parser

import (
	"fmt"
	"strconv"
	"stuntgres/internal/sqlstate"
)

// VisitQuerySources visits each actual SELECT before its children, including
// queries without table leaves, and physical leaf occurrences in binding order.
// The enclosing statement remains authorization authority; query is the actual
// lexical query containing the leaf. Repeated references are not deduplicated.
// Either callback may be nil; a callback error stops traversal.
func VisitQuerySources(statement Statement, visitQuery func(*Select) error, visitTable func(*TableReference, *Select) error, classify ...func(*TableReference, bool, string)) error {
	return visitQuerySources(statement, visitQuery, visitTable, classify, nil, nil, "", false)
}

// VisitQueryExpressions visits every raw scalar, including nested query bodies.
func VisitQueryExpressions(statement Statement, inspect func(Expr) error) error {
	return visitQuerySources(statement, nil, nil, nil, nil, nil, "", false, inspect)
}

// recursiveLegality changes only SubLink context/order, never ordinary source admission.
func visitQuerySources(statement Statement, visitQuery func(*Select) error, visitTable func(*TableReference, *Select) error, classify []func(*TableReference, bool, string), resolved func(*TableReference, *CommonTableExpression, string) error, visible []*CommonTableExpression, context string, recursiveLegality bool, inspect ...func(Expr) error) error {
	if explanation, ok := statement.(*Explain); ok {
		statement = explanation.Statement
	}
	var pending []string
	var query func(*Select) error
	var queryBody func(*Select) error
	var walk func(Statement) error
	depth := 0
	var expression func(Expr) error
	var from func(FromItem, *Select) error
	expressions := func(items []Expr) error {
		for _, item := range items {
			if err := expression(item); err != nil {
				return err
			}
		}
		return nil
	}
	bounds := func(b *QueryBounds) error {
		if b == nil {
			return nil
		}
		if err := expression(b.Offset); err != nil {
			return err
		}
		return expression(b.Count)
	}
	orderKeys := func(keys []OrderKey) error {
		for _, key := range keys {
			if err := expression(key.Expr); err != nil {
				return err
			}
		}
		return nil
	}
	order := func(o *Order) error {
		if o == nil {
			return nil
		}
		return orderKeys(o.Keys)
	}
	values := func(v *ValuesQuery) error {
		for _, row := range v.Rows {
			if err := expressions(row); err != nil {
				return err
			}
		}
		if err := order(v.Order); err != nil {
			return err
		}
		return bounds(v.Bounds)
	}
	window := func(spec *WindowSpec) error {
		if spec == nil {
			return nil
		}
		if err := expressions(spec.Partition); err != nil {
			return err
		}
		if err := orderKeys(spec.Order); err != nil {
			return err
		}
		for _, offset := range spec.Offsets() {
			if err := expression(offset); err != nil {
				return err
			}
		}
		return nil
	}
	expression = func(raw Expr) error {
		if raw != nil && len(inspect) != 0 {
			if err := inspect[0](raw); err != nil {
				return err
			}
		}
		switch e := raw.(type) {
		case XMLDocumentPredicateExpr:
			return expression(e.Child)
		case XMLParseExpr:
			return expression(e.Child)
		case XMLSerializeExpr:
			return expression(e.Child)
		case GroupingExpr:
			return expressions(e.Args)
		case InListExpr:
			if err := expression(e.Left); err != nil {
				return err
			}
			return expressions(e.Items)
		case ArrayComparisonExpr:
			if err := expression(e.Left); err != nil {
				return err
			}
			return expression(e.Right)
		case SubqueryExpr:
			if recursiveLegality {
				old := context
				context = "subquery"
				err := query(e.Query)
				context = old
				if err != nil {
					return err
				}
				return expression(e.Left)
			}
			if err := expression(e.Left); err != nil {
				return err
			}
			old := context
			if context == "" {
				context = "subquery"
			}
			defer func() { context = old }()
			return query(e.Query)
		case FieldExpr:
			return expression(e.Base)
		case RowExpr:
			return expressions(e.Fields)
		case OperatorExpr:
			return expressions(e.Args)
		case SQLJSONQueryExpr:
			return expressions(e.Children())
		case SQLJSONExpr:
			if err := expressions(e.Args); err != nil {
				return err
			}
			if e.Query != nil {
				return expression(SubqueryExpr{Query: e.Query, Pos: e.Pos})
			}
			return nil
		case JSONPredicateExpr:
			return expression(e.Child)
		case FunctionExpr:
			if err := expressions(e.Args); err != nil {
				return err
			}
			if err := orderKeys(e.Order); err != nil {
				return err
			}
			if err := expression(e.Filter); err != nil {
				return err
			}
			return window(e.Window)
		case AggregateExpr:
			if err := expressions(e.Args); err != nil {
				return err
			}
			if err := orderKeys(e.Order); err != nil {
				return err
			}
			if err := expression(e.Filter); err != nil {
				return err
			}
			return window(e.Window)
		case ConditionalExpr:
			return expressions(e.Args)
		case CallExpr:
			return expressions(e.Args)
		case ArrayExpr:
			return expressions(e.Elements)
		case DeclarationArraySubqueryExpr:
			return expression(SubqueryExpr{Query: e.Query, Pos: e.Pos})
		case SubscriptExpr:
			var storage [13]Expr
			return expressions(e.Children(&storage))
		case CastExpr:
			return expression(e.Child)
		case RegclassExpr:
			return expression(e.Child)
		case CollateExpr:
			return expression(e.Child)
		case TimeZoneExpr:
			if err := expression(e.Value); err != nil {
				return err
			}
			return expression(e.Zone)
		case LogicalExpr:
			if err := expression(e.Left); err != nil {
				return err
			}
			return expression(e.Right)
		case PredicateExpr:
			if err := expression(e.Left); err != nil {
				return err
			}
			return expression(e.Right)
		case CaseExpr:
			if err := expression(e.Test); err != nil {
				return err
			}
			for _, arm := range e.Arms {
				if err := expression(arm.Condition); err != nil {
					return err
				}
				if err := expression(arm.Result); err != nil {
					return err
				}
			}
			return expression(e.Else)
		}
		return nil
	}
	from = func(item FromItem, s *Select) error {
		switch n := item.(type) {
		case *XMLTable:
			for _, child := range n.Expressions() {
				if err := expression(child); err != nil { return err }
			}
		case *JSONTable:
			for _, child := range n.Expressions() {
				if err := expression(child); err != nil {
					return err
				}
			}
		case *FunctionTable:
			for _, call := range n.Calls {
				if err := expression(call); err != nil {
					return err
				}
			}
		case *TableReference:
			if len(n.Name.Parts) == 1 {
				name := n.Name.Parts[0].Text
				for i := len(visible) - 1; i >= 0; i-- {
					if visible[i].Name.Text == name {
						if len(classify) != 0 {
							classify[0](n, true, "")
						}
						if resolved != nil {
							return resolved(n, visible[i], context)
						}
						return nil
					}
				}
				pendingName := ""
				for i := len(pending) - 1; i >= 0; i-- {
					if pending[i] == name {
						pendingName = name
						break
					}
				}
				if len(classify) != 0 {
					classify[0](n, false, pendingName)
				}
			} else if len(classify) != 0 {
				classify[0](n, false, "")
			}
			if visitTable != nil {
				return visitTable(n, s)
			}
		case *DerivedQuery:
			return query(n.Query)
		case *ValuesQuery:
			return values(n)
		case *FunctionFrom:
			for _, call := range n.Calls {
				if err := expression(call); err != nil {
					return err
				}
			}
		case *AliasReference:
			return from(n.Input, s)
		case *JoinReference:
			old := context
			if context == "" && (n.Kind == JoinRight || n.Kind == JoinFull) {
				context = "outer join"
			}
			if err := from(n.Left, s); err != nil {
				context = old
				return err
			}
			context = old
			if context == "" && (n.Kind == JoinLeft || n.Kind == JoinFull) {
				context = "outer join"
			}
			if err := from(n.Right, s); err != nil {
				context = old
				return err
			}
			context = old
			return expression(n.On)
		}
		return nil
	}
	query = func(s *Select) error { return walk(s) }
	queryBody = func(s *Select) error {
		if visitQuery != nil {
			if err := visitQuery(s); err != nil {
				return err
			}
		}
		if s.Set != nil {
			old := context
			if context == "" && (s.Set.Operator == "intersect" && s.Set.All || s.Set.Operator == "except" && s.Set.All) {
				context = s.Set.Operator
			}
			if err := query(s.Set.Left); err != nil {
				context = old
				return err
			}
			context = old
			if context == "" && (s.Set.Operator == "intersect" && s.Set.All || s.Set.Operator == "except") {
				context = s.Set.Operator
			}
			if err := query(s.Set.Right); err != nil {
				context = old
				return err
			}
			context = old
			if err := order(s.Order); err != nil {
				return err
			}
			return bounds(s.Bounds)
		}
		if s.Values != nil {
			return values(s.Values)
		}
		if err := from(s.From, s); err != nil {
			return err
		}
		if err := expressions(s.Targets); err != nil {
			return err
		}
		if err := expression(s.Where); err != nil {
			return err
		}
		if err := s.GroupBy.VisitExpressions(expression); err != nil {
			return err
		}
		if err := expression(s.Having); err != nil {
			return err
		}
		if s.Distinct != nil {
			if err := expressions(s.Distinct.On); err != nil {
				return err
			}
		}
		for _, definition := range s.Windows {
			if err := window(&definition.Spec); err != nil {
				return err
			}
		}
		if err := order(s.Order); err != nil {
			return err
		}
		return bounds(s.Bounds)
	}
	returning := func(r *Returning) error {
		if r == nil {
			return nil
		}
		return expressions(r.Targets)
	}
	walk = func(statement Statement) error {
		oldVisible, oldPending := len(visible), len(pending)
		defer func() { visible = visible[:oldVisible]; pending = pending[:oldPending]; depth-- }()
		top := depth == 0
		depth++
		definitions := StatementWith(statement)
		if _, ok := statement.(*Merge); ok && StatementWithRecursive(statement) {
			return recursiveCTEError("42601", "WITH RECURSIVE is not supported for MERGE statement", Position{})
		}
		for i, definition := range definitions {
			for _, prior := range definitions[:i] {
				if definition.Name.Text == prior.Name.Text {
					return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42712", Message: fmt.Sprintf(`WITH query name "%s" specified more than once`, definition.Name.Text), Position: strconv.Itoa(definition.Name.Pos.Character)}
				}
			}
			pending = append(pending, definition.Name.Text)
		}
		recursive := StatementWithRecursive(statement)
		if resolved == nil && recursive {
			if _, err := AnalyzeRecursiveCTEs(statement); err != nil {
				return err
			}
		}
		if recursive {
			for i := range definitions {
				visible = append(visible, &definitions[i])
			}
		}
		for i, definition := range definitions {
			if !top && ModifyingStatement(definition.Query) {
				return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "WITH clause containing a data-modifying statement must be at the top level", Position: strconv.Itoa(definition.Name.Pos.Character)}
			}
			pending = pending[:oldPending]
			for _, later := range definitions[i:] {
				pending = append(pending, later.Name.Text)
			}
			if err := walk(definition.Query); err != nil {
				return err
			}
			if !recursive {
				visible = append(visible, &definitions[i])
			}
		}
		pending = pending[:oldPending]
		switch s := statement.(type) {
		case *Call:
			return expression(s.Function)
		case *CreateTrigger:
			if err := expression(s.When); err != nil {
				return err
			}
		case *CreateRule:
			if err := expression(s.Condition); err != nil {
				return err
			}
			for _, action := range s.Actions {
				if err := VisitQuerySources(action, visitQuery, visitTable, classify...); err != nil {
					return err
				}
			}
			return nil
		case *CreatePolicy:
			if err := expression(s.Using); err != nil {
				return err
			}
			return expression(s.Check)
		case *AlterPolicy:
			if err := expression(s.Using); err != nil {
				return err
			}
			return expression(s.Check)
		case *PrepareSQL:
			return VisitQuerySources(s.Body, visitQuery, visitTable, classify...)
		case *Select:
			return queryBody(s)
		case *DeclareCursor:
			return query(s.Query)
		case *CreateView:
			return query(s.Query)
		case *CreateQueryTable:
			return query(s.Query)
		case *Insert:
			for _, target := range s.Targets {
				for _, step := range target.Steps {
					if step.Subscript == nil {
						continue
					}
					bound := step.Subscript
					if err := expression(bound.Lower); err != nil {
						return err
					}
					if err := expression(bound.Upper); err != nil {
						return err
					}
				}
			}
			if s.Source != nil {
				var err error
				if s.Source.Select != nil {
					err = query(s.Source.Select)
				} else {
					err = values(s.Source.Values)
				}
				if err != nil {
					return err
				}
			} else {
				for _, row := range s.Rows {
					if err := expressions(row); err != nil {
						return err
					}
				}
			}
			if s.Conflict != nil && s.Conflict.Update {
				// Match shared assignment binding: every RHS precedes destination
				// subscript analysis, and action WHERE follows the complete SET list.
				for _, assignment := range s.Conflict.Assignments {
					if err := expression(assignment.Value); err != nil {
						return err
					}
				}
				for _, assignment := range s.Conflict.Assignments {
					for _, step := range assignment.Steps {
						if step.Subscript == nil {
							continue
						}
						bound := step.Subscript
						if err := expression(bound.Lower); err != nil {
							return err
						}
						if err := expression(bound.Upper); err != nil {
							return err
						}
					}
				}
				if err := expression(s.Conflict.Where); err != nil {
					return err
				}
			}
			return returning(s.Returning)
		case *Merge:
			if err := from(s.Source, &Select{From: s.Source}); err != nil {
				return err
			}
			if err := expression(s.On); err != nil {
				return err
			}
			if err := returning(s.Returning); err != nil {
				return err
			}
			for _, arm := range s.Arms {
				if err := expression(arm.Condition); err != nil {
					return err
				}
				if err := expressions(arm.Values); err != nil {
					return err
				}
				for _, assignment := range arm.Assignments {
					for _, step := range assignment.Steps {
						if step.Subscript == nil {
							continue
						}
						bound := step.Subscript
						if err := expression(bound.Lower); err != nil {
							return err
						}
						if err := expression(bound.Upper); err != nil {
							return err
						}
					}
					if err := expression(assignment.Value); err != nil {
						return err
					}
				}
			}
		case *Update:
			if err := from(s.From, &Select{From: s.From}); err != nil {
				return err
			}
			if err := expression(s.Where); err != nil {
				return err
			}
			if err := returning(s.Returning); err != nil {
				return err
			}
			for _, assignment := range s.Assignments {
				for _, target := range assignment.Targets {
					for _, step := range target.Steps {
						if step.Subscript != nil {
							if err := expression(step.Subscript.Lower); err != nil {
								return err
							}
							if err := expression(step.Subscript.Upper); err != nil {
								return err
							}
						}
					}
				}
				for _, step := range assignment.Steps {
					if step.Subscript == nil {
						continue
					}
					bound := step.Subscript
					if err := expression(bound.Lower); err != nil {
						return err
					}
					if err := expression(bound.Upper); err != nil {
						return err
					}
				}
				if err := expression(assignment.Value); err != nil {
					return err
				}
			}
		case *Delete:
			if err := from(s.Using, &Select{From: s.Using}); err != nil {
				return err
			}
			if err := expression(s.Where); err != nil {
				return err
			}
			return returning(s.Returning)
		}
		return nil
	}
	switch s := statement.(type) {
	case *CreateView:
		statement = s.Query
	case *CreateQueryTable:
		statement = s.Query
	case *DeclareCursor:
		statement = s.Query
	}
	return walk(statement)
}
