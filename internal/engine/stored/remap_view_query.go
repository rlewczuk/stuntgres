package stored

import (
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Schema origins and alias datums contain projected addresses, not retained
// ObjectRef authority. Routine replacement supplies their distinct mapper so
// actual captures still pass the checked provenance boundary.
func remapAddress(address ids.ObjectAddress, ref func(ids.ObjectRef) ids.ObjectRef, addressMapper func(ids.ObjectAddress) ids.ObjectAddress) ids.ObjectAddress {
	if addressMapper != nil {
		return addressMapper(address)
	}
	return ref(ids.ObjectRef{Address: address}).Address
}

func RemapViewQuery(view catalog.Read, node plan.Node, ref func(ids.ObjectRef) ids.ObjectRef, typ func(*sqltype.Descriptor) *sqltype.Descriptor, collations ...typedef.CollationRemapper) (plan.Node, error) {
	typ = analyze.RecordTypeMapper(typ, collations...)
	return remapViewQueryMapped(view, node, ref, typ, collations...)
}

func remapViewQueryMapped(view catalog.Read, node plan.Node, ref func(ids.ObjectRef) ids.ObjectRef, typ func(*sqltype.Descriptor) *sqltype.Descriptor, collations ...typedef.CollationRemapper) (plan.Node, error) {
	var collation typedef.CollationRemapper
	if len(collations) != 0 {
		collation = collations[0]
	}
	mapper := &ViewQueryRemapper{Catalog: view, Ref: ref, Type: typ, Collation: collation}
	if _, ok := node.(*plan.ProjectNode); ok {
		return mapper.Nested(node)
	}
	return mapper.Query(node)
}

// ViewQueryRemapper owns one logical remapping operation, which may traverse
// several query and mutation roots. Keep its policy and source graph fixed for
// that operation; use Fresh before changing any policy or catalog snapshot.
type ViewQueryRemapper struct {
	Ref            func(ids.ObjectRef) ids.ObjectRef
	Type           func(*sqltype.Descriptor) *sqltype.Descriptor
	queries        map[*plan.ProjectNode]plan.Node
	queryDepth     int // Only the outermost failure discards the operation memo.
	Collation      typedef.CollationRemapper
	Catalog        catalog.Read
	Procedure      catalog.ProcedureRemapper
	referencesOnly bool
	ctes           map[*plan.CTEDefinition]*plan.CTEDefinition
	Mutation       func(*plan.CTEDefinition) (analyze.Binding, error)
	routineFormals int // Private provenance from a frozen routine presentation.
	addressMapper  func(ids.ObjectAddress) ids.ObjectAddress
}

// Fresh copies the mapping policy into a new operation without accepted roots.
// In particular, a changed destination must pass the ordinary source/current
// provider checks rather than inherit another operation's accepted graph.
func (m *ViewQueryRemapper) Fresh() ViewQueryRemapper {
	next := *m
	next.queries, next.ctes, next.queryDepth = nil, nil, 0
	return next
}

func (m *ViewQueryRemapper) Nested(node plan.Node) (result plan.Node, err error) {
	m.queryDepth++
	defer func() {
		m.queryDepth--
		if m.queryDepth == 0 && err != nil {
			m.queries = nil
			m.ctes = nil
		}
	}()
	project, ok := node.(*plan.ProjectNode)
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing remapped query frame")
	}
	if root := m.queries[project]; root != nil {
		return root, nil
	}
	if m.queries == nil {
		m.queries = make(map[*plan.ProjectNode]plan.Node)
	}
	// Register the query identity before recursive-definition remapping can
	// encounter its canonical project again. Self scans remain opaque leaves.
	copy := *project
	m.queries[project] = &copy
	root, err := m.Query(node)
	if err != nil {
		delete(m.queries, project)
		return nil, err
	}
	copy = *root.(*plan.ProjectNode)
	return &copy, nil
}

func (m *ViewQueryRemapper) Query(node plan.Node) (result plan.Node, err error) {
	m.queryDepth++
	defer func() {
		m.queryDepth--
		if m.queryDepth == 0 && err != nil {
			m.queries = nil
			m.ctes = nil
		}
	}()
	ref, typ := m.Ref, m.Type
	schema := append(plan.QuerySchema(nil), node.Schema()...)
	for i := range schema {
		schema[i].Usage.Type = typ(schema[i].Usage.Type)
		if m.Collation != nil {
			capability, err := m.Collation(schema[i].Usage.Collation)
			if err != nil {
				return nil, err
			}
			schema[i].Usage.Collation = capability
		}
		if schema[i].OriginTable != 0 {
			origin := remapAddress(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(schema[i].OriginTable)}, ref, m.addressMapper)
			schema[i].OriginTable = uint32(origin.ObjectID)
		}
	}
	child := func(old plan.Node) plan.Node {
		if err != nil {
			return old
		}
		var next plan.Node
		next, err = m.Query(old)
		return next
	}
	expression := func(old expr.Expr) expr.Expr {
		if err != nil {
			return old
		}
		next, failure := remapScalar(old, ref, ref, nil, m.Nested, m.routineFormals, m.addressMapper)
		if failure != nil {
			err = failure
			return old
		}
		if !m.referencesOnly {
			// Nested owns both reference and type replacement. Its output is
			// opaque to this scalar-only type pass, even for routine formals.
			next, _, failure = m.retag(next, typ)
			if failure != nil {
				err = failure
				return old
			}
		}
		return next
	}
	expressions := func(old []expr.Expr) []expr.Expr {
		next := make([]expr.Expr, len(old))
		for i, n := range old {
			next[i] = expression(n)
		}
		return next
	}
	grouping := func(old plan.Grouping) plan.Grouping {
		if err != nil {
			return old
		}
		next := old
		next.Routine = ref(old.Routine)
		next.Procedure, err = m.procedure(old.Procedure)
		return next
	}
	equality := func(old plan.SortKey) plan.SortKey {
		if err != nil || old.EqualityRoutine == (ids.ObjectRef{}) {
			return old
		}
		next := old
		next.EqualityRoutine = ref(old.EqualityRoutine)
		next.EqualityProcedure, err = m.procedure(old.EqualityProcedure)
		return next
	}
	var next plan.Node
	switch n := node.(type) {
	case *plan.FunctionFromNode:
		copy := *n
		copy.QuerySchema = schema
		copy.Calls = append([]expr.Expr(nil), n.Calls...)
		for i, call := range copy.Calls {
			next := expression(call)
			if err == nil {
				copy.Calls[i] = next
			}
		}
		next = &copy
	case *plan.SetProjectNode:
		copy := *n
		copy.QuerySchema = schema
		copy.Child = child(n.Child)
		copy.Scalars = expressions(n.Scalars)
		if n.Template != nil {
			template := *n.Template
			template.Expressions, template.Origins, template.Scalars = expressions(n.Template.Expressions), expressions(n.Template.Origins), expressions(n.Template.Scalars)
			copy.Template = &template
		}
		copy.Calls = append([]expr.Expr(nil), n.Calls...)
		for i, call := range copy.Calls {
			next := expression(call)
			if err == nil {
				copy.Calls[i] = next
			}
		}
		next = &copy
	case *plan.ValuesNode:
		rows := make([][]expr.Expr, len(n.Rows))
		for i, row := range n.Rows {
			rows[i] = expressions(row)
		}
		next = &plan.ValuesNode{QuerySchema: schema, Rows: rows, ViewOrdinal: n.ViewOrdinal, IdentityOrderDemand: append([]bool(nil), n.IdentityOrderDemand...), PresenceSlot: n.PresenceSlot}
	case *plan.LimitNode:
		copy := &plan.LimitNode{QuerySchema: schema, Child: child(n.Child), Offset: expression(n.Offset), Count: expression(n.Count), WithTies: n.WithTies, PresenceOnly: n.PresenceOnly}
		if err == nil {
			copy.FinishTies(copy.Child)
		}
		next = copy
	case *plan.EvaluationNode:
		next = &plan.EvaluationNode{QuerySchema: schema, Child: child(n.Child), Expressions: expressions(n.Expressions), Base: n.Base}
	case *plan.RowLockNode:
		next = &plan.RowLockNode{QuerySchema: schema, Child: child(n.Child), Marks: n.Marks, FirstMode: n.FirstMode}
	case *plan.SourceNode:
		next = &plan.SourceNode{QuerySchema: schema, Ref: ref(n.Ref), Occurrence: n.Occurrence, PresenceSlot: n.PresenceSlot, RangeName: n.RangeName, ColumnAliases: n.ColumnAliases}
	case *plan.JoinNode:
		next = &plan.JoinNode{QuerySchema: schema, Kind: n.Kind, Left: child(n.Left), Right: child(n.Right), Predicate: expression(n.Predicate), LeftWidth: n.LeftWidth, RightWidth: n.RightWidth, Merges: expressions(n.Merges), ViewOrdinal: n.ViewOrdinal, CorrelatedRight: n.CorrelatedRight, Frame: n.Frame, CorrelationPrefix: n.CorrelationPrefix, CorrelationIdentityPrefix: n.CorrelationIdentityPrefix, Captures: n.Captures, PresenceSlot: n.PresenceSlot}
	case *plan.SingletonNode:
		next = &plan.SingletonNode{QuerySchema: schema}
	case *plan.WorktableScanNode:
		copy := *n
		copy.QuerySchema = schema
		copy.CTE, err = m.CTE(n.CTE)
		next = &copy
	case *plan.DerivedNode:
		copy := *n
		copy.QuerySchema = schema
		if n.CTE != nil {
			copy.CTE, err = m.CTE(n.CTE)
		}
		if err == nil && n.Query != nil {
			query := *n.Query
			query.Root, err = m.Nested(query.Root)
			copy.Query = &query
		}
		next = &copy
	case *plan.ProjectNode:
		next = &plan.ProjectNode{QuerySchema: schema, Child: child(n.Child), Expressions: expressions(n.Expressions), AnalysisExpressions: expressions(n.AnalysisExpressions), FrozenValues: n.FrozenValues, Frame: n.Frame, Syntax: n.Syntax, CTEOwner: n.CTEOwner, HasTargetSet: n.HasTargetSet, RangeName: n.RangeName, ColumnAliases: n.ColumnAliases}
	case *plan.SetNode, *plan.RecursiveUnionNode:
		set := QuerySetNode(node)
		copy := *set
		copy.QuerySchema = schema
		var definition *plan.CTEDefinition
		if recursive, ok := node.(*plan.RecursiveUnionNode); ok {
			definition, err = m.CTE(recursive.Definition)
		}
		if err == nil {
			var left plan.Node
			left, err = m.Nested(set.Left)
			if err == nil {
				copy.Left = left.(*plan.ProjectNode)
			}
		}
		if err == nil {
			var right plan.Node
			right, err = m.Nested(set.Right)
			if err == nil {
				copy.Right = right.(*plan.ProjectNode)
			}
		}
		copy.LeftExpressions = expressions(set.LeftExpressions)
		copy.RightExpressions = expressions(set.RightExpressions)
		copy.Grouping = nil
		if !(copy.Operation == "union" && copy.All) {
			width := len(set.Grouping)
			copy.Grouping = make([]plan.Grouping, width)
			for i := range copy.Grouping {
				slot := i
				if definition != nil && definition.Cycle != nil && i == definition.UserWidth {
					slot = definition.Cycle.MarkSlot
				}
				copy.Grouping[i] = grouping(set.Grouping[i])
				if err == nil {
					copy.Grouping[i].Grouping, err = sqltype.BindGrouping(schema[slot].Usage)
				}
			}
		}
		if definition != nil {
			cycle := definition.Cycle
			if original := node.(*plan.RecursiveUnionNode).Cycle; original != nil && original != node.(*plan.RecursiveUnionNode).Definition.Cycle {
				cycle, err = m.cycle(original)
			}
			next = &plan.RecursiveUnionNode{SetNode: copy, Definition: definition, Search: definition.Search, Cycle: cycle}
		} else {
			next = &copy
		}
	case *plan.DistinctNode:
		copy := *n
		copy.QuerySchema = schema
		copy.Child = child(n.Child)
		copy.Expressions = expressions(n.Expressions)
		copy.Grouping = make([]plan.Grouping, len(copy.Expressions))
		for i, expression := range copy.Expressions {
			copy.Grouping[i] = grouping(n.Grouping[i])
			if err == nil {
				copy.Grouping[i].Grouping, err = sqltype.BindGrouping(expression.ResultUsage())
			}
		}
		next = &copy
	case *plan.DistinctOnNode:
		copy := *n
		copy.QuerySchema = schema
		copy.Child = child(n.Child)
		copy.Expressions = expressions(n.Expressions)
		copy.Grouping = make([]plan.Grouping, len(copy.Expressions))
		for i, expression := range copy.Expressions {
			copy.Grouping[i] = grouping(n.Grouping[i])
			if err == nil {
				copy.Grouping[i].Grouping, err = sqltype.BindGrouping(expression.ResultUsage())
			}
		}
		next = &copy
	case *plan.FilterNode:
		next = &plan.FilterNode{QuerySchema: schema, Child: child(n.Child), Predicate: expression(n.Predicate)}
	case *plan.SortNode:
		copy := *n
		copy.QuerySchema = schema
		copy.Child = child(n.Child)
		copy.Keys = append([]plan.SortKey(nil), n.Keys...)
		copy.Outputs = expressions(n.Outputs)
		for i, key := range copy.Keys {
			copy.Keys[i] = equality(key)
			copy.Keys[i].Expression = expression(key.Expression)
			if err == nil && !copy.Keys[i].Expression.CollationIndeterminate() {
				copy.Keys[i].Comparison,copy.Keys[i].Ordering,err = m.ordering(key,copy.Keys[i].Expression.ResultUsage())
			}
		}
		next = &copy
	case *plan.WindowNode:
		copy := *n
		copy.QuerySchema = schema
		copy.Child = child(n.Child)
		copy.Specs = append([]plan.WindowSpec(nil), n.Specs...)
		for i, spec := range copy.Specs {
			spec.Partition = expressions(spec.Partition)
			spec.Grouping = make([]plan.Grouping, len(spec.Partition))
			for j, key := range spec.Partition {
				spec.Grouping[j] = grouping(n.Specs[i].Grouping[j])
				if err == nil {
					spec.Grouping[j].Grouping, err = sqltype.BindGrouping(key.ResultUsage())
				}
			}
			spec.Order = append([]plan.SortKey(nil), spec.Order...)
			for j, key := range spec.Order {
				spec.Order[j] = equality(key)
				spec.Order[j].Expression = expression(key.Expression)
				if err == nil {
					spec.Order[j].Comparison,spec.Order[j].Ordering,err = m.ordering(key,spec.Order[j].Expression.ResultUsage())
				}
			}
			for _, bound := range []*plan.WindowBound{&spec.Frame.Start, &spec.Frame.End} {
				bound.Offset = expression(bound.Offset)
				if err == nil && spec.Frame.Unit == parser.FrameRange {
					bound.Support, err = bound.Support.Remap(m.Type)
				}
			}
			copy.Specs[i] = spec
		}
		copy.Calls = append([]plan.WindowCall(nil), n.Calls...)
		for i, call := range copy.Calls {
			copy.Calls[i].Routine = ref(call.Routine)
			if err == nil {
				copy.Calls[i].Procedure, err = m.procedure(call.Procedure)
			}
			copy.Calls[i].Expression = expression(call.Expression).(*plan.WindowExpr)
			copy.Calls[i].Argument = expression(call.Argument)
			copy.Calls[i].Arguments = expressions(call.Arguments)
			copy.Calls[i].Ordinal = expression(call.Ordinal)
			copy.Calls[i].DefaultValue = expression(call.DefaultValue)
			if provider, ok := call.Provider.(functions.RemappableProvider); ok && err == nil {
				err = m.provider(&copy.Calls[i].Provider, provider, nil)
			}
			if selected, ok := copy.Calls[i].Provider.(*exec.TransitionAggregate); ok && err == nil {
				if i >= len(copy.SpecIndices) || copy.SpecIndices[i] < 0 || copy.SpecIndices[i] >= len(copy.Specs) {
					err = fmt.Errorf("stuntgres: invalid owned window specification")
				} else if selected.Moving != analyze.MovingAggregateSelected(selected.Aggregate.Aggregate.Definition, copy.Specs[copy.SpecIndices[i]].Frame, copy.Calls[i].Arguments) || len(selected.Inputs) != len(copy.Calls[i].Arguments)+1 || !exec.SameRoutineDeclaration(selected.Aggregate, copy.Calls[i].Procedure) || selected.Result != copy.Calls[i].Expression.ResultUsage() {
					err = fmt.Errorf("stuntgres: mismatched owned window implementation")
				} else {
					for j, argument := range copy.Calls[i].Arguments {
						if argument.ResultUsage().Type != selected.Inputs[j+1].Type {
							err = fmt.Errorf("stuntgres: mismatched owned window argument")
							break
						}
					}
				}
			}
		}
		next = &copy
	case *plan.AggregateNode:
		copy := *n
		copy.QuerySchema = schema
		copy.Child = child(n.Child)
		copy.Keys = expressions(n.Keys)
		copy.Grouping = make([]plan.Grouping, len(copy.Keys))
		for i, key := range copy.Keys {
			copy.Grouping[i] = grouping(n.Grouping[i])
			if err == nil {
				copy.Grouping[i].Grouping, err = sqltype.BindGrouping(key.ResultUsage())
			}
		}
		copy.Calls = make([]plan.AggregateCall, len(n.Calls))
		for i, call := range n.Calls {
			copy.Calls[i] = call
			if call.Ref != (ids.ObjectRef{}) && ref != nil {
				copy.Calls[i].Ref = ref(call.Ref)
			}
			if err == nil {
				copy.Calls[i].Procedure, err = m.procedure(call.Procedure)
			}
			for slot, support := range call.PrimaryStringSupport {
				if err == nil {
					copy.Calls[i].PrimaryStringSupport[slot], err = m.procedure(support)
				}
			}
			copy.Calls[i].Routine = ref(call.Routine)
			provider, ok := call.Provider.(functions.RemappableProvider)
			if !ok {
				return nil, fmt.Errorf("stuntgres: unpersistable view aggregate")
			}
			if err == nil {
				var first expr.Expr
				if len(call.Arguments) > 0 {
					first = call.Arguments[0]
				}
				err = m.provider(&copy.Calls[i].Provider, provider, first)
			}
			copy.Calls[i].Arguments = expressions(call.Arguments)
			copy.Calls[i].Direct = expressions(call.Direct)
			if call.Filter != nil {
				copy.Calls[i].Filter = expression(call.Filter)
			}
			copy.Calls[i].Distinct = make([]plan.Grouping, len(call.Distinct))
			for k := range call.Distinct {
				copy.Calls[i].Distinct[k] = grouping(call.Distinct[k])
				arg := copy.Calls[i].Arguments[k]
				if err == nil {
					copy.Calls[i].Distinct[k].Grouping, err = sqltype.BindGrouping(arg.ResultUsage())
				}
				if err == nil {
					_, err = functions.AggregateOrdering(arg.ResultUsage(), arg.SourcePosition(), true)
				}
			}
			copy.Calls[i].Order = append([]plan.SortKey(nil), call.Order...)
			for k, key := range call.Order {
				copy.Calls[i].Order[k] = equality(key)
				copy.Calls[i].Order[k].Expression = expression(key.Expression)
				if err == nil {
					usage := copy.Calls[i].Order[k].Expression.ResultUsage()
					if key.Slot >= 0 && key.Slot < len(call.Arguments) {
						usage = copy.Calls[i].Arguments[key.Slot].ResultUsage()
					} else if key.Slot >= len(call.Arguments) {
						usage = copy.Calls[i].Order[key.Slot-len(call.Arguments)].Expression.ResultUsage()
					}
					copy.Calls[i].Order[k].Comparison, err = functions.AggregateOrdering(usage, key.Expression.SourcePosition(), false)
				}
			}
		}
		next = &copy
	default:
		return nil, fmt.Errorf("stuntgres: unpersistable view query %T", node)
	}
	return next, err
}
