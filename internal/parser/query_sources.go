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
	var visible []string
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
		switch e := raw.(type) {
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
			if err := expression(e.Left); err != nil {
				return err
			}
			return query(e.Query)
		case FieldExpr:
			return expression(e.Base)
		case RowExpr:
			return expressions(e.Fields)
		case OperatorExpr:
			return expressions(e.Args)
		case FunctionExpr:
			if err := expressions(e.Args); err != nil {
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
					if visible[i] == name {
						if len(classify) != 0 {
							classify[0](n, true, "")
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
			if err := from(n.Left, s); err != nil {
				return err
			}
			if err := from(n.Right, s); err != nil {
				return err
			}
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
			if err := query(s.Set.Left); err != nil {
				return err
			}
			if err := query(s.Set.Right); err != nil {
				return err
			}
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
		for i, definition := range definitions {
			for _, prior := range definitions[:i] {
				if definition.Name.Text == prior.Name.Text {
					return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42712", Message: fmt.Sprintf(`WITH query name "%s" specified more than once`, definition.Name.Text), Position: strconv.Itoa(definition.Name.Pos.Character)}
				}
			}
			pending = append(pending, definition.Name.Text)
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
			visible = append(visible, definition.Name.Text)
		}
		pending = pending[:oldPending]
		switch s := statement.(type) {
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
