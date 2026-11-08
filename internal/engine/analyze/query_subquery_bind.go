package analyze

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *Binder) bindSubquery(raw parser.SubqueryExpr) (expr.Expr, error) {
	if b.Consumer.DefaultConsumer {
		return nil, expr.ErrorAt("0A000", "cannot use subquery in DEFAULT expression", raw.Pos)
	}
	if b.Consumer.CheckConsumer {
		return nil, expr.ErrorAt("0A000", "cannot use subquery in check constraint", raw.Pos)
	}
	if b.Consumer.IndexPredicateConsumer {
		return nil, expr.ErrorAt("0A000", "cannot use subquery in index predicate", raw.Pos)
	}
	if b.Consumer.IndexConsumer {
		return nil, expr.ErrorAt("0A000", "cannot use subquery in index expression", raw.Pos)
	}
	if b.Target.GeneratedOwner != nil {
		return nil, expr.ErrorAt("0A000", "cannot use subquery in column generation expression", raw.Pos)
	}
	b.Query.subqueryDemand = true
	var left []ComparisonOperand
	var leftExpansions map[int]plan.CompositeExpansionBinding
	var leftConstructor expr.Expr
	var erasedLeftType sqltype.ID
	if raw.Left != nil {
		var err error
		if row, ok := raw.Left.(parser.RowExpr); ok {
			left, leftExpansions, err = b.RowComparisonOperands(row)
		} else {
			var operand ComparisonOperand
			operand, err = b.comparisonOperand(raw.Left)
			if constructor, ok := operand.Node.(plan.RowExpr); ok && constructor.Raw != nil && !constructor.Usage.Type.IsDomain() {
				// A cast to a named composite can still analyze to a ROW
				// constructor. Quantified subqueries compare its fields;
				// a domain coercion or scalar subquery remains one value.
				left = make([]ComparisonOperand, len(constructor.Fields))
				for i, field := range constructor.Fields {
					left[i] = ComparisonOperand{Node: field}
				}
				leftExpansions = constructor.Expansions
				if b.Params.NoParameters || b.Consumer.frozenDefinition {
					erasedLeftType = constructor.Usage.Type.ID()
					leftConstructor, err = b.normalizeQuantifiedConstructor(raw.Left, constructor)
					if err != nil {
						return nil, err
					}
				}
			} else {
				left = []ComparisonOperand{operand}
			}
		}
		if err != nil {
			return nil, err
		}
	}
	query, err := b.BindSelect(raw.Query, false)
	if err != nil {
		return nil, err
	}
	project := query.Root.(*plan.ProjectNode)
	width := len(project.Schema())
	usage := b.Type(sqltype.Bool).Usage()
	if raw.Kind == parser.SubqueryArray {
		if width != 1 {
			return nil, expr.ErrorAt("42601", "subquery must return only one column", raw.Pos)
		}
		element := project.Schema()[0].Usage
		arrays := false
		if element.Type.IsArray() && !element.Type.IsCatalogVector() {
			// A true array output promotes to the same array type one dimension
			// higher (PostgreSQL get_promoted_array_type).
			usage = element
			arrays = true
		} else {
			array, ok := b.definitionArray(element.Type)
			if element.Type.ID() == 2249 && element.Type.IsComposite() {
				var failure error
				array, failure = sqltype.Array(2287, "_record", element.Type)
				if failure != nil {
					return nil, failure
				}
				ok = true
			}
			if !ok {
				return nil, expr.ErrorAt("42704", "could not find array type for data type "+b.DiagnosticTypeName(element.Type), parser.Position{})
			}
			if !array.Concrete() {
				return nil, expr.ErrorAt("0A000", "unsupported array element type", raw.Pos)
			}
			usage = element
			usage.Type = array
		}
		properties := QueryDemandProperties(project, false)
		properties.StageDependent = true
		e := plan.SubqueryExpr{
			Info:      b.Info(usage, raw.Pos, properties),
			Kind:      parser.SubqueryArray,
			QueryPlan: query,
			Array:     &plan.ArraySubqueryQuery{Plan: functions.NewArrayAggregateProvider(element.Type, usage, arrays)},
		}
		e.InitPlan = initPlanQuery(query, properties)
		return e, nil
	}
	if raw.Kind == parser.SubqueryScalar {
		if width != 1 {
			return nil, expr.ErrorAt("42601", "subquery must return only one column", raw.Pos)
		}
		usage = project.Schema()[0].Usage
	} else if raw.Kind != parser.SubqueryExists {
		if width < len(left) {
			return nil, expr.ErrorAt("42601", "subquery has too few columns", raw.Pos)
		}
		if width > len(left) {
			return nil, expr.ErrorAt("42601", "subquery has too many columns", raw.Pos)
		}
		if width == 0 {
			return nil, expr.ErrorAt("0A000", "cannot compare rows of zero length", raw.Pos)
		}
	}
	presenceOnly := raw.Kind == parser.SubqueryExists && CanSimplifyExists(query)
	if presenceOnly {
		query, err = PresenceQueryPlan(query)
		if err != nil {
			return nil, err
		}
		project = query.Root.(*plan.ProjectNode)
	}
	properties := QueryDemandProperties(project, presenceOnly)
	properties.StageDependent = true
	e := plan.SubqueryExpr{Info: b.Info(usage, raw.Pos, properties), Kind: raw.Kind, QueryPlan: query, Negate: raw.Negate, PresenceOnly: presenceOnly}
	e.LeftExpansions = leftExpansions
	e.LeftConstructor, e.ErasedLeftType = leftConstructor, erasedLeftType
	if raw.Kind == parser.SubqueryScalar || raw.Kind == parser.SubqueryExists {
		e.InitPlan = initPlanQuery(query, properties)
		return e, nil
	}
	e.Operator = raw.Operator.Parts[len(raw.Operator.Parts)-1].Text
	operator := sqltype.CompareEqual
	switch e.Operator {
	case "=":
	case "<>":
		operator = sqltype.CompareNotEqual
	case "<":
		operator = sqltype.CompareLess
	case "<=":
		operator = sqltype.CompareLessEqual
	case ">":
		operator = sqltype.CompareGreater
	case ">=":
		operator = sqltype.CompareGreaterEqual
	default:
		return nil, fmt.Errorf("stuntgres: invalid subquery comparison operator")
	}
	e.Comparisons = make([]plan.ComparisonExpr, width)
	if width > 1 && operator > sqltype.CompareNotEqual {
		e.Ordering = make([]sqltype.Comparison, width)
	}
	for i, column := range project.Schema() {
		right := plan.ColumnExpr{Info: b.Info(column.Usage, raw.Pos, expr.Properties{RowDependent: true}), Scope: 1, Frame: project.Frame, Slot: i}
		bound, err := b.BindComparisonOperands(parser.OperatorExpr{Name: raw.Operator, Pos: raw.Pos}, operator, left[i], ComparisonOperand{Node: right})
		if err != nil {
			return nil, err
		}
		e.Comparisons[i] = bound.(plan.ComparisonExpr)
		e.Property = expr.MergeProperties(e.Property, e.Comparisons[i].Left.Properties())
		if len(e.Ordering) != 0 {
			ref, conflict := e.Comparisons[i].Predicate.InputCollation()
			e.Ordering[i], err = sqltype.BindComparison(e.Comparisons[i].Left.ResultUsage().Type, e.Comparisons[i].Right.ResultUsage().Type, ref, conflict)
			if err != nil {
				return nil, err
			}
		}
	}
	if err := b.checkRowComparisonInterpretation(parser.OperatorExpr{Name: raw.Operator, Pos: raw.Pos}, e.Comparisons); err != nil {
		return nil, err
	}
	if raw.Kind == parser.SubqueryAny && operator == sqltype.CompareEqual && !properties.RowDependent {
		families := make([]plan.Grouping, width)
		for i, comparison := range e.Comparisons {
			kernel, failure := comparison.Predicate.EqualityGrouping()
			if failure != nil {
				families = nil
				break
			}
			source := comparison.Info
			source.Usage = comparison.Left.ResultUsage()
			group := plan.Grouping{Source: source}
			group.Routine, group.Procedure, group.PrivateTree, err = b.groupingRoutineReference(source.Usage)
			if err != nil {
				return nil, err
			}
			if selected, ok := comparison.RoutineReference.(*catalog.Procedure); ok {
				group.Procedure = selected
				group.Routine = b.BindingCatalog().Domain().Ref(b.BindingCatalog().Epoch(), selected.Address())
				group.RoutineRecipe = comparison.RoutineRecipeReference
			}
			kernel, err = b.captureGroupingCallbacks(kernel, source.Usage, source)
			if err != nil {
				return nil, err
			}
			if p, ok := comparison.RoutineReference.(*catalog.Procedure); ok && !source.Usage.Type.BaseType().IsArray() && !routine.FmgrBuiltin(p) && b.BindingCatalog().Owner().Sepgsql != nil {
				kernel, err = kernel.WithEqualityCallback(catalog.GroupingRoutineCallback{Type: source.Usage.Type, Procedure: p, Reference: group.Routine, Occurrence: source.Occurrence, ExpressionSource: source.Source})
				if err != nil {
					return nil, err
				}
			}
			group.Grouping = kernel
			families[i] = group
		}
		e.Membership = families
	}
	return e, nil
}

// initPlanQuery reports a subquery whose value is fixed for one execution, as
// for a PostgreSQL InitPlan: it reads no outer row and calls nothing volatile,
// so every evaluation reads the same captured sources. Demand properties do not
// look into WITH bodies, so a subquery that reads a CTE never qualifies.
func initPlanQuery(query *plan.Query, p expr.Properties) bool {
	if p.RowDependent || p.Volatility >= expr.Volatile {
		return false
	}
	cte := false
	plan.VisitExecutableQuery(query.Root, func(node plan.Node) {
		switch n := node.(type) {
		case *plan.DerivedNode:
			cte = cte || n.CTE != nil
		case *plan.WorktableScanNode, *plan.RecursiveUnionNode:
			cte = true
		}
	}, func(expr.Expr) {})
	return !cte
}
