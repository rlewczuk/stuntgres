package parser

// VisitQuerySources visits each actual SELECT before its children, including
// queries without table leaves, and physical leaf occurrences in binding order.
// The enclosing statement remains authorization authority; query is the actual
// lexical query containing the leaf. Repeated references are not deduplicated.
// Either callback may be nil; a callback error stops traversal.
func VisitQuerySources(statement Statement, visitQuery func(*Select) error, visitTable func(*TableReference, *Select) error) error {
	var query func(*Select) error
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
	expression = func(raw Expr) error {
		switch e := raw.(type) {
		case SubqueryExpr:
			if err := expression(e.Left); err != nil {
				return err
			}
			return query(e.Query)
		case RowExpr:
			return expressions(e.Fields)
		case OperatorExpr:
			return expressions(e.Args)
		case FunctionExpr:
			return expressions(e.Args)
		case AggregateExpr:
			if err := expressions(e.Args); err != nil {
				return err
			}
			if err := orderKeys(e.Order); err != nil {
				return err
			}
			return expression(e.Filter)
		case ConditionalExpr:
			return expressions(e.Args)
		case CallExpr:
			return expressions(e.Args)
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
		case *TableReference:
			if visitTable != nil {
				return visitTable(n, s)
			}
		case *DerivedQuery:
			return query(n.Query)
		case *ValuesQuery:
			return values(n)
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
	query = func(s *Select) error {
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
		if err := expressions(s.GroupBy); err != nil {
			return err
		}
		if err := expression(s.Having); err != nil {
			return err
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
	switch s := statement.(type) {
	case *Select:
		return query(s)
	case *CreateView:
		return query(s.Query)
	case *CreateQueryTable:
		return query(s.Query)
	case *Insert:
		if s.Source != nil {
			if s.Source.Select != nil {
				return query(s.Source.Select)
			}
			return values(s.Source.Values)
		}
		for _, row := range s.Rows {
			if err := expressions(row); err != nil {
				return err
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
