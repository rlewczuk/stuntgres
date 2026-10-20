package engine

import (
	"fmt"
	"sort"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Replacement copies the closed bound tree; source and expression identities
// are reminted together. Syntax and frozen layouts are immutable and shared.
func remapBoundExpression(node expr.Expr, ref func(ids.ObjectRef) ids.ObjectRef, slots map[int32]int) (expr.Expr, error) {
	return remapBoundExpressionWithQuery(node, ref, slots, func(root LogicalNode) (LogicalNode, error) {
		return (&viewQueryRemapper{ref: ref, typ: func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }, referencesOnly: true}).query(root)
	}, nil)
}

func remapBoundExpressionWithQuery(node expr.Expr, ref func(ids.ObjectRef) ids.ObjectRef, slots map[int32]int, queryMapper func(LogicalNode) (LogicalNode, error), rowRef func(ids.ObjectRef) ids.ObjectRef) (expr.Expr, error) {
	if rowRef == nil {
		rowRef = ref
	}
	return remapScalarExpression(node, ref, rowRef, slots, queryMapper)
}

// Row copies retain explicit object constants and external calls. Replacement
// callers map both roles, whereas LIKE and partition inheritance supply a
// distinct owner-row mapper.
func remapScalarExpression(node expr.Expr, ref, rowRef func(ids.ObjectRef) ids.ObjectRef, slots map[int32]int, queryMapper func(LogicalNode) (LogicalNode, error)) (expr.Expr, error) {
	if node == nil {
		return nil, nil
	}
	var err error
	child := func(old expr.Expr) expr.Expr {
		if err != nil {
			return old
		}
		var next expr.Expr
		next, err = remapScalarExpression(old, ref, rowRef, slots, queryMapper)
		return next
	}
	args := func(old []expr.Expr) []expr.Expr {
		next := make([]expr.Expr, len(old))
		for i, n := range old {
			next[i] = child(n)
		}
		return next
	}
	var next expr.Expr
	switch n := node.(type) {
	case sqlRoutineExpression:
		n.Args = args(n.Args)
		if ref != nil {
			n.Ref = ref(n.Ref)
		}
		next = n
	case assignmentValueExpression:
		n.Base, n.Value = child(n.Base), child(n.Value)
		targets, failure := mapRuleAssignmentTargets([]*arrayAssignmentTarget{n.Target}, func(expr expr.Expr) (expr.Expr, bool, error) {
			if expr == nil {
				return nil, false, nil
			}
			return child(expr), true, err
		}, func(usage sqltype.Usage) (sqltype.Usage, error) { return usage, nil })
		if failure != nil {
			return nil, failure
		}
		n.Target = targets[0]
		next = n
	case unknownOutput:
		// Captured contextual analysis carries immutable literal syntax only;
		// it is not a persisted scalar value or a remappable object reference.
		literal, ok := n.Raw.(parser.LiteralExpr)
		if !ok || n.ResultUsage() != (sqltype.Usage{}) || literal.Value.Kind != parser.StringValue && literal.Value.Kind != parser.NullValue {
			return nil, fmt.Errorf("stuntgres: invalid contextual view output")
		}
		next = n
	case rowExpression:
		n, _, err = mapRowAnalysis(n, func(old expr.Expr) (expr.Expr, bool, error) { next := child(old); return next, true, err })
		next = n
	case fieldExpression:
		n.Base = child(n.Base)
		next = n
	case arrayExpression:
		n.Elements = args(n.Elements)
		next = n
	case subscriptExpression:
		n, _, err = mapSubscript(n, func(old expr.Expr) (expr.Expr, bool, error) {
			if old == nil {
				return nil, false, nil
			}
			next := child(old)
			return next, true, err
		})
		next = n
	case subqueryExpression:
		if queryMapper != nil {
			root, failure := queryMapper(n.QueryPlan.Root)
			if failure != nil {
				return nil, failure
			}
			copy := *n.QueryPlan
			copy.Root = root
			n.QueryPlan = &copy
		}
		n.Comparisons = append([]comparisonExpression(nil), n.Comparisons...)
		for i, c := range n.Comparisons {
			mapped := child(c)
			if err != nil {
				return nil, err
			}
			n.Comparisons[i] = mapped.(comparisonExpression)
		}
		n.LeftExpansions, _, err = mapRowComparisonExpansions(n.LeftExpansions, func(old expr.Expr) (expr.Expr, bool, error) {
			next := child(old)
			return next, true, err
		})
		if n.LeftConstructor != nil {
			n.LeftConstructor = child(n.LeftConstructor)
		}
		next = n
	case updateOutputExpression:
		next = n
	case domainValueExpression:
		next = n
	case literalExpression:
		if ref == nil {
			next = n
			break
		}
		if n.RegclassRef != (ids.ObjectRef{}) {
			n.RegclassRef = ref(n.RegclassRef)
		}
		if n.Usage.Type.ContainsRegclass() && !n.Value.IsNull() {
			var failure error
			n.Value, failure = remapRegclassDatum(n.Value, func(id ids.OID) ids.OID {
				return ref(ids.ObjectRef{Address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}}).Address.ObjectID
			})
			if failure != nil {
				return nil, failure
			}
		}
		if (n.Usage.Type.ContainsRegtype() || n.Usage.Type.ContainsRegObject()) && !n.Value.IsNull() {
			var failure error
			n.Value, failure = remapCatalogAliasDatum(n.Value, func(address ids.ObjectAddress) ids.ObjectAddress {
				return ref(ids.ObjectRef{Address: address}).Address
			})
			if failure != nil {
				return nil, failure
			}
		}
		if n.Usage.Type.ContainsRegproc() && !n.Value.IsNull() {
			var failure error
			n.Value, failure = remapRoutineDatum(n.Value, func(id ids.OID) ids.OID {
				return ref(ids.ObjectRef{Address: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}}).Address.ObjectID
			})
			if failure != nil {
				return nil, failure
			}
		}
		if n.Usage.Type.ContainsOperatorAlias() && !n.Value.IsNull() {
			var failure error
			n.Value, failure = remapOperatorDatum(n.Value, func(id ids.OID) ids.OID {
				return ref(ids.ObjectRef{Address: ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: id}}).Address.ObjectID
			})
			if failure != nil {
				return nil, failure
			}
		}
		if n.Usage.Type.ContainsRegTS() && !n.Value.IsNull() {
			var failure error
			n.Value, failure = remapRegTSDatum(n.Value, func(address ids.ObjectAddress) ids.ObjectAddress {
				return ref(ids.ObjectRef{Address: address}).Address
			})
			if failure != nil {
				return nil, failure
			}
		}
		next = n
	case tableOIDExpression:
		n.Owner = rowRef(n.Owner)
		next = n
	case returningColumnExpression:
		mapped := child(n.columnExpression)
		if err != nil {
			return nil, err
		}
		column, ok := mapped.(columnExpression)
		if !ok {
			return nil, fmt.Errorf("stuntgres: invalid remapped RETURNING column")
		}
		n.columnExpression = column
		n.Value = child(n.Value)
		next = n
	case returningWholeRowExpression:
		n.Members = args(n.Members)
		next = n
	case mergeActionExpression:
		next = n
	case columnExpression:
		n.Origin = rowRef(n.Origin)
		if ref == nil && slots != nil {
			// Owner-row copies carry attribute identity only. CREATE attaches
			// the completed destination's recipe and full layout afterward.
			n.Virtual = nil
			n.RowStart, n.RowWidth, n.QueryOccurrence = 0, 0, 0
		} else {
			n.Virtual = child(n.Virtual)
		}
		if slots != nil {
			slot, ok := slots[n.Origin.Address.SubID]
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing remapped row attribute")
			}
			n.Slot = slot
			if n.Virtual != nil {
				n.RowStart = 0
				n.RowWidth = len(slots)
			}
		}
		next = n
	case groupExpression:
		next = n
	case aggregateExpression:
		next = n
	case groupingExpression:
		n.Args = args(n.Args)
		next = n
	case *windowExpression:
		copy := *n
		next = &copy
	case cachedOrderExpression:
		next = n // The cache slot belongs to this immutable query plan.
	case collationExpression:
		n.Child = child(n.Child)
		next = n
	case coercionExpression:
		n.Child = child(n.Child)
		next = n
	case bitwiseExpression:
		n.Left, n.Right = child(n.Left), child(n.Right)
		next = n
	case selectedOperatorExpression:
		n.Provider = child(n.Provider)
		if ref != nil {
			n.Ref = ref(n.Ref)
		}
		next = n
	case arithmeticExpression:
		n.Left, n.Right = child(n.Left), child(n.Right)
		next = n
	case unnestExpression:
		n.Args = args(n.Args)
		if ref != nil && n.Procedure != nil {
			n.Ref = ref(n.Ref)
		}
		next = n
	case likeExpression:
		n.Left, n.Right = child(n.Left), child(n.Right)
		next = n
	case rowComparisonExpression:
		n.Comparisons = append([]comparisonExpression(nil), n.Comparisons...)
		for i, pair := range n.Comparisons {
			n.Comparisons[i] = child(pair).(comparisonExpression)
		}
		if err != nil {
			return nil, err
		}
		for side, expansions := range n.Expansions {
			n.Expansions[side], _, err = mapRowComparisonExpansions(expansions, func(old expr.Expr) (expr.Expr, bool, error) {
				next := child(old)
				return next, true, err
			})
			if err != nil {
				return nil, err
			}
		}
		next = n
	case scalarArrayComparisonExpression:
		n.Left = child(n.Left)
		if n.Array != nil {
			n.Array = child(n.Array)
		}
		n.Items = append([]expr.Expr(nil), n.Items...)
		for i := range n.Items {
			n.Items[i] = child(n.Items[i])
		}
		next = n
	case comparisonExpression:
		n.Left, n.Right = child(n.Left), child(n.Right)
		next = n
	case nullTestExpression:
		n.Child = child(n.Child)
		next = n
	case truthTestExpression:
		n.Child = child(n.Child)
		next = n
	case distinctExpression:
		n.Left, n.Right = child(n.Left), child(n.Right)
		next = n
	case logicalExpression:
		n.Left, n.Right = child(n.Left), child(n.Right)
		next = n
	case conditionalExpression:
		n.Args = args(n.Args)
		next = n
	case extensionPathsExpression:
		n.arg = child(n.arg)
		next = n
	case installedSoundexFunctionExpression:
		n.Args = args(n.Args)
		if ref != nil {
			n.ref = ref(n.ref)
		}
		if err == nil {
			n.Soundex, err = n.Reconstruct(n.Info, n.Args)
		}
		next = n
	case installedMetaphoneFunctionExpression:
		n.Args = args(n.Args)
		if ref != nil {
			n.ref = ref(n.ref)
		}
		if err == nil {
			n.Metaphone, err = n.Reconstruct(n.Info, n.Args)
		}
		next = n
	case installedLevenshteinFunctionExpression:
		n.Args = args(n.Args)
		if ref != nil {
			n.ref = ref(n.ref)
		}
		if err == nil {
			n.Levenshtein, err = n.Reconstruct(n.Info, n.Args)
		}
		next = n
	case installedUUIDOsspFunctionExpression:
		if ref != nil {
			n.ref = ref(n.ref)
		}
		next = n
	case installedCryptoFunctionExpression:
		n.Args = args(n.Args)
		if ref != nil {
			n.ref = ref(n.ref)
		}
		if err == nil {
			n.Pgcrypto, err = n.Reconstruct(n.Info, n.Args)
		}
		next = n
	case installedFunctionExpression:
		n.Arg = child(n.Arg)
		if ref != nil {
			n.ref = ref(n.ref)
		}
		next = n
	case functions.SequenceCall:
		if ref != nil {
			n.Ref = ref(n.Ref)
		}
		n.Args = args(n.Args)
		next = n
	case caseExpression:
		n.Test, n.Otherwise = child(n.Test), child(n.Otherwise)
		n.Arms = append([]caseBranch(nil), n.Arms...)
		for i := range n.Arms {
			n.Arms[i].Condition, n.Arms[i].Result = child(n.Arms[i].Condition), child(n.Arms[i].Result)
		}
		next = n
	case expr.Composite:
		// Last, so the explicit engine wrappers above keep precedence. Remap
		// copies the whole tree, so every child counts as replaced.
		next, _, err = expr.Map(n, func(old expr.Expr) (expr.Expr, bool, error) {
			next, failure := remapScalarExpression(old, ref, rowRef, slots, queryMapper)
			return next, true, failure
		})
		if err == nil && ref != nil {
			next = remapQuoteOwner(next, ref)
		}
	default:
		return nil, fmt.Errorf("stuntgres: unpersistable scalar expression %T", node)
	}
	return next, err
}

func remapViewQuery(view catalog.Read, node LogicalNode, ref func(ids.ObjectRef) ids.ObjectRef, typ func(*sqltype.Descriptor) *sqltype.Descriptor, collations ...typedef.CollationRemapper) (LogicalNode, error) {
	typ = recordTypeMapper(typ, collations...)
	return remapViewQueryMapped(view, node, ref, typ, collations...)
}

func remapViewQueryMapped(view catalog.Read, node LogicalNode, ref func(ids.ObjectRef) ids.ObjectRef, typ func(*sqltype.Descriptor) *sqltype.Descriptor, collations ...typedef.CollationRemapper) (LogicalNode, error) {
	var collation typedef.CollationRemapper
	if len(collations) != 0 {
		collation = collations[0]
	}
	mapper := &viewQueryRemapper{catalog: view, ref: ref, typ: typ, collation: collation}
	if _, ok := node.(*projectNode); ok {
		return mapper.nested(node)
	}
	return mapper.query(node)
}

type viewQueryRemapper struct {
	ref            func(ids.ObjectRef) ids.ObjectRef
	typ            func(*sqltype.Descriptor) *sqltype.Descriptor
	queries        map[*projectNode]LogicalNode
	collation      typedef.CollationRemapper
	catalog        catalog.Read
	referencesOnly bool
	ctes           map[*cteDefinition]*cteDefinition
	mutation       func(*cteDefinition) (binding, error)
}

func (m *viewQueryRemapper) nested(node LogicalNode) (LogicalNode, error) {
	project, ok := node.(*projectNode)
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing remapped query frame")
	}
	if root := m.queries[project]; root != nil {
		return root, nil
	}
	if m.queries == nil {
		m.queries = make(map[*projectNode]LogicalNode)
	}
	// Register the query identity before recursive-definition remapping can
	// encounter its canonical project again. Self scans remain opaque leaves.
	copy := *project
	m.queries[project] = &copy
	root, err := m.query(node)
	if err != nil {
		delete(m.queries, project)
		return nil, err
	}
	copy = *root.(*projectNode)
	return &copy, nil
}

func (m *viewQueryRemapper) query(node LogicalNode) (LogicalNode, error) {
	ref, typ := m.ref, m.typ
	schema := append(querySchema(nil), node.Schema()...)
	for i := range schema {
		schema[i].Usage.Type = typ(schema[i].Usage.Type)
		if m.collation != nil {
			capability, err := m.collation(schema[i].Usage.Collation)
			if err != nil {
				return nil, err
			}
			schema[i].Usage.Collation = capability
		}
		if schema[i].OriginTable != 0 {
			origin := ref(ids.ObjectRef{Address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(schema[i].OriginTable)}})
			schema[i].OriginTable = uint32(origin.Address.ObjectID)
		}
	}
	var err error
	usage := func(old sqltype.Usage) sqltype.Usage {
		old.Type = typ(old.Type)
		if m.collation != nil && err == nil {
			old.Collation, err = m.collation(old.Collation)
		}
		return old
	}
	child := func(old LogicalNode) LogicalNode {
		if err != nil {
			return old
		}
		var next LogicalNode
		next, err = m.query(old)
		return next
	}
	expression := func(old expr.Expr) expr.Expr {
		if err != nil {
			return old
		}
		next, failure := remapBoundExpressionWithQuery(old, ref, nil, m.nested, nil)
		if failure != nil {
			err = failure
			return old
		}
		if !m.referencesOnly {
			next, _, failure = retagBoundTypesWithQuery(m.catalog, next, typ, nil, m.collation)
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
	var next LogicalNode
	switch n := node.(type) {
	case *functionFromNode:
		copy := *n
		copy.querySchema = schema
		copy.Calls = append([]expr.Expr(nil), n.Calls...)
		for i, call := range copy.Calls {
			next := expression(call)
			if err == nil {
				copy.Calls[i] = next
			}
		}
		next = &copy
	case *setProjectNode:
		copy := *n
		copy.querySchema = schema
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
	case *valuesNode:
		rows := make([][]expr.Expr, len(n.Rows))
		for i, row := range n.Rows {
			rows[i] = expressions(row)
		}
		next = &valuesNode{querySchema: schema, Rows: rows, ViewOrdinal: n.ViewOrdinal, IdentityOrderDemand: append([]bool(nil), n.IdentityOrderDemand...), PresenceSlot: n.PresenceSlot}
	case *limitNode:
		copy := &limitNode{querySchema: schema, Child: child(n.Child), Offset: expression(n.Offset), Count: expression(n.Count), WithTies: n.WithTies, PresenceOnly: n.PresenceOnly}
		if err == nil {
			copy.FinishTies(copy.Child)
		}
		next = copy
	case *evaluationNode:
		next = &evaluationNode{querySchema: schema, Child: child(n.Child), Expressions: expressions(n.Expressions), Base: n.Base}
	case *rowLockNode:
		next = &rowLockNode{querySchema: schema, Child: child(n.Child), Marks: n.Marks, FirstMode: n.FirstMode}
	case *sourceNode:
		next = &sourceNode{querySchema: schema, Ref: ref(n.Ref), Occurrence: n.Occurrence, PresenceSlot: n.PresenceSlot}
	case *joinNode:
		next = &joinNode{querySchema: schema, Kind: n.Kind, Left: child(n.Left), Right: child(n.Right), Predicate: expression(n.Predicate), LeftWidth: n.LeftWidth, RightWidth: n.RightWidth, Merges: expressions(n.Merges), ViewOrdinal: n.ViewOrdinal, CorrelatedRight: n.CorrelatedRight, Frame: n.Frame, CorrelationPrefix: n.CorrelationPrefix, Captures: n.Captures, PresenceSlot: n.PresenceSlot}
	case *singletonNode:
		next = &singletonNode{querySchema: schema}
	case *worktableScanNode:
		copy := *n
		copy.querySchema = schema
		copy.CTE, err = m.cte(n.CTE)
		next = &copy
	case *derivedNode:
		copy := *n
		copy.querySchema = schema
		if n.CTE != nil {
			copy.CTE, err = m.cte(n.CTE)
		}
		if err == nil && n.Query != nil {
			query := *n.Query
			query.Root, err = m.nested(query.Root)
			copy.Query = &query
		}
		next = &copy
	case *projectNode:
		next = &projectNode{querySchema: schema, Child: child(n.Child), Expressions: expressions(n.Expressions), AnalysisExpressions: expressions(n.AnalysisExpressions), FrozenValues: n.FrozenValues, Frame: n.Frame, Syntax: n.Syntax, CTEOwner: n.CTEOwner, HasTargetSet: n.HasTargetSet}
	case *setNode, *recursiveUnionNode:
		set := querySetNode(node)
		copy := *set
		copy.querySchema = schema
		var definition *cteDefinition
		if recursive, ok := node.(*recursiveUnionNode); ok {
			definition, err = m.cte(recursive.Definition)
		}
		if err == nil {
			var left LogicalNode
			left, err = m.nested(set.Left)
			if err == nil {
				copy.Left = left.(*projectNode)
			}
		}
		if err == nil {
			var right LogicalNode
			right, err = m.nested(set.Right)
			if err == nil {
				copy.Right = right.(*projectNode)
			}
		}
		copy.LeftExpressions = expressions(set.LeftExpressions)
		copy.RightExpressions = expressions(set.RightExpressions)
		copy.Grouping = nil
		if !(copy.Operation == "union" && copy.All) {
			width := len(set.Grouping)
			copy.Grouping = make([]queryGrouping, width)
			for i := range copy.Grouping {
				slot := i
				if definition != nil && definition.Cycle != nil && i == definition.UserWidth {
					slot = definition.Cycle.MarkSlot
				}
				copy.Grouping[i] = set.Grouping[i]
				copy.Grouping[i].Routine = ref(set.Grouping[i].Routine)
				if err == nil {
					copy.Grouping[i].Grouping, err = sqltype.BindGrouping(schema[slot].Usage)
				}
			}
		}
		if definition != nil {
			cycle := definition.Cycle
			if original := node.(*recursiveUnionNode).Cycle; original != nil && original != node.(*recursiveUnionNode).Definition.Cycle {
				cycle, err = m.cycle(original)
			}
			next = &recursiveUnionNode{setNode: copy, Definition: definition, Search: definition.Search, Cycle: cycle}
		} else {
			next = &copy
		}
	case *distinctNode:
		copy := *n
		copy.querySchema = schema
		copy.Child = child(n.Child)
		copy.Expressions = expressions(n.Expressions)
		copy.Grouping = make([]queryGrouping, len(copy.Expressions))
		for i, expression := range copy.Expressions {
			copy.Grouping[i] = n.Grouping[i]
			copy.Grouping[i].Routine = ref(n.Grouping[i].Routine)
			if err == nil {
				copy.Grouping[i].Grouping, err = sqltype.BindGrouping(expression.ResultUsage())
			}
		}
		next = &copy
	case *distinctOnNode:
		copy := *n
		copy.querySchema = schema
		copy.Child = child(n.Child)
		copy.Expressions = expressions(n.Expressions)
		copy.Grouping = make([]queryGrouping, len(copy.Expressions))
		for i, expression := range copy.Expressions {
			copy.Grouping[i] = n.Grouping[i]
			copy.Grouping[i].Routine = ref(n.Grouping[i].Routine)
			if err == nil {
				copy.Grouping[i].Grouping, err = sqltype.BindGrouping(expression.ResultUsage())
			}
		}
		next = &copy
	case *filterNode:
		next = &filterNode{querySchema: schema, Child: child(n.Child), Predicate: expression(n.Predicate)}
	case *sortNode:
		copy := *n
		copy.querySchema = schema
		copy.Child = child(n.Child)
		copy.Keys = append([]sortKey(nil), n.Keys...)
		copy.Outputs = expressions(n.Outputs)
		for i, key := range copy.Keys {
			copy.Keys[i].Expression = expression(key.Expression)
			if err == nil && !copy.Keys[i].Expression.CollationIndeterminate() {
				copy.Keys[i].Comparison, err = sqltype.BindOrdering(copy.Keys[i].Expression.ResultUsage())
			}
		}
		next = &copy
	case *windowNode:
		copy := *n
		copy.querySchema = schema
		copy.Child = child(n.Child)
		copy.Specs = append([]boundWindowSpec(nil), n.Specs...)
		for i, spec := range copy.Specs {
			spec.Partition = expressions(spec.Partition)
			spec.Grouping = make([]queryGrouping, len(spec.Partition))
			for j, key := range spec.Partition {
				spec.Grouping[j] = n.Specs[i].Grouping[j]
				spec.Grouping[j].Routine = ref(n.Specs[i].Grouping[j].Routine)
				if err == nil {
					spec.Grouping[j].Grouping, err = sqltype.BindGrouping(key.ResultUsage())
				}
			}
			spec.Order = append([]sortKey(nil), spec.Order...)
			for j, key := range spec.Order {
				spec.Order[j].Expression = expression(key.Expression)
				spec.Order[j].EqualityRoutine = ref(key.EqualityRoutine)
				if err == nil {
					spec.Order[j].Comparison, err = sqltype.BindOrdering(spec.Order[j].Expression.ResultUsage())
				}
			}
			for _, bound := range []*boundWindowBound{&spec.Frame.Start, &spec.Frame.End} {
				bound.Offset = expression(bound.Offset)
				if err == nil && spec.Frame.Unit == parser.FrameRange {
					bound.Support, err = bound.Support.Remap(m.typ)
				}
			}
			copy.Specs[i] = spec
		}
		copy.Calls = append([]windowCall(nil), n.Calls...)
		for i, call := range copy.Calls {
			copy.Calls[i].Routine = ref(call.Routine)
			copy.Calls[i].Expression = expression(call.Expression).(*windowExpression)
			copy.Calls[i].Argument = expression(call.Argument)
			copy.Calls[i].Ordinal = expression(call.Ordinal)
			copy.Calls[i].DefaultValue = expression(call.DefaultValue)
			switch p := call.Provider.(type) {
			case functions.IntegralSum:
				copy.Calls[i].Provider = p.Reconstruct(typ(p.InputType()), usage(p.ResultUsage()))
			case functions.NumericSum:
				copy.Calls[i].Provider = p.Reconstruct(typ(p.InputType()), usage(p.ResultUsage()))
			case functions.FloatSum:
				copy.Calls[i].Provider = p.Reconstruct(typ(p.InputType()), usage(p.ResultUsage()))
			}
		}
		next = &copy
	case *aggregateNode:
		copy := *n
		copy.querySchema = schema
		copy.Child = child(n.Child)
		copy.Keys = expressions(n.Keys)
		copy.Grouping = make([]queryGrouping, len(copy.Keys))
		for i, key := range copy.Keys {
			copy.Grouping[i] = n.Grouping[i]
			copy.Grouping[i].Routine = ref(n.Grouping[i].Routine)
			if err == nil {
				copy.Grouping[i].Grouping, err = sqltype.BindGrouping(key.ResultUsage())
			}
		}
		copy.Calls = make([]aggregateCall, len(n.Calls))
		for i, call := range n.Calls {
			copy.Calls[i] = call
			if call.Procedure != nil && ref != nil {
				copy.Calls[i].Ref = ref(call.Ref)
				if !m.referencesOnly {
					proc, ok := m.catalog.Procedure(copy.Calls[i].Ref.Address)
					if !ok {
						return nil, fmt.Errorf("stuntgres: missing remapped intagg aggregate")
					}
					copy.Calls[i].Procedure = proc
					if failure := validateIntaggIdentity(m.catalog, proc, copy.Calls[i].Ref, "int_array_aggregate"); failure != nil {
						return nil, failure
					}
				}
			}
			copy.Calls[i].Routine = ref(call.Routine)
			switch p := call.Provider.(type) {
			case *functions.HypotheticalSet:
				copy.Calls[i].Provider = functions.NewHypotheticalSet(usage(p.ResultUsage()), p.Operation())
			case *functions.OrderedSet:
				result, input, fraction := usage(p.ResultUsage()), typ(p.InputType()), typ(p.FractionType())
				var comparison sqltype.Comparison
				if err == nil {
					comparison, err = sqltype.BindOrdering(usage(call.Argument.ResultUsage()))
				}
				if err == nil {
					copy.Calls[i].Provider, err = functions.NewOrderedSet(result, input, fraction, p.Operation(), p.Array(), comparison)
				}
			case functions.CountProvider:
				copy.Calls[i].Provider = functions.NewCountProvider(usage(p.ResultUsage()))
			case functions.BooleanAggregateProvider:
				copy.Calls[i].Provider = functions.NewBooleanAggregateProvider(usage(p.ResultUsage()), p.Conjunction())
			case functions.IntervalAvg:
				copy.Calls[i].Provider = p.Reconstruct(usage(p.ResultUsage()))
			case functions.NumericAvg:
				copy.Calls[i].Provider = p.Reconstruct(typ(p.InputType()), usage(p.ResultUsage()))
			case functions.FloatAvg:
				copy.Calls[i].Provider = p.Reconstruct(typ(p.InputType()), usage(p.ResultUsage()))
			case functions.StatisticalAggregate:
				copy.Calls[i].Provider = p.Reconstruct(typ(p.InputType()), usage(p.ResultUsage()))
			case functions.RegressionAggregate:
				copy.Calls[i].Provider = p.Reconstruct(typ(p.InputType()), usage(p.ResultUsage()))
			case functions.RegressionCountAggregate:
				copy.Calls[i].Provider = p.Reconstruct(typ(p.InputType()), usage(p.ResultUsage()))
			case functions.IntegralBitwiseAggregateProvider:
				u := usage(p.ResultUsage())
				if err == nil {
					var bound sqltype.Bitwise
					bound, err = sqltype.BindBitwise(p.Operator(), u.Type, u.Type)
					if err == nil {
						copy.Calls[i].Provider = functions.NewIntegralBitwiseAggregateProvider(u, p.Operator(), bound)
					}
				}
			case functions.MoneySum:
				u := usage(p.ResultUsage())
				if err == nil {
					var rebound functions.MoneySum
					rebound, err = p.Reconstruct(u)
					copy.Calls[i].Provider = rebound
				}
			case functions.IntervalSum:
				copy.Calls[i].Provider = p.Reconstruct(usage(p.ResultUsage()))
			case functions.BitStringAggregateProvider:
				copy.Calls[i].Provider = functions.NewBitStringAggregateProvider(usage(p.ResultUsage()), p.Operator())
			case functions.IntegralSum:
				copy.Calls[i].Provider = p.Reconstruct(typ(p.InputType()), usage(p.ResultUsage()))
			case functions.NumericSum:
				copy.Calls[i].Provider = p.Reconstruct(typ(p.InputType()), usage(p.ResultUsage()))
			case functions.FloatSum:
				copy.Calls[i].Provider = p.Reconstruct(typ(p.InputType()), usage(p.ResultUsage()))
			case functions.AnyValue:
				copy.Calls[i].Provider = p.Reconstruct(usage(p.ResultUsage()))
			case functions.JSONArrayAggregateProvider:
				input, result := usage(p.InputUsage()), usage(p.ResultUsage())
				if err == nil {
					var plan sqltype.JSONArrayAggregatePlan
					plan, err = sqltype.BindJSONArrayAggregate(input, result, p.Absent())
					if err == nil {
						copy.Calls[i].Provider = functions.NewJSONArrayAggregateProvider(input, result, p.Absent(), plan)
					}
				}
			case functions.JSONObjectAggregate:
				key, input, result := usage(p.KeyUsage()), usage(p.InputUsage()), usage(p.ResultUsage())
				if err == nil {
					var rebound functions.JSONObjectAggregate
					rebound, err = p.Reconstruct(key, input, result)
					copy.Calls[i].Provider = rebound
				}
			case functions.ExtremaProvider:
				result := usage(p.ResultUsage())
				if err == nil {
					_, conflict := p.Comparison().InputCollation()
					var comparison sqltype.Comparison
					comparison, err = sqltype.BindComparison(result.Type, result.Type, result.Collation, conflict)
					if err == nil {
						copy.Calls[i].Provider = functions.NewExtremaProvider(result, comparison, p.Maximum())
					}
				}
			case functions.StringAggregateProvider:
				copy.Calls[i].Provider = functions.NewStringAggregateProvider(usage(p.ResultUsage()))
			case *functions.ArrayAggregateProvider:
				copy.Calls[i].Provider = functions.NewArrayAggregateProvider(typ(p.ArgumentType()), usage(p.ResultUsage()), p.Arrays())
			case functions.RangeAggregateProvider:
				input, result := usage(p.InputUsage()), usage(p.ResultUsage())
				if err == nil {
					var bound sqltype.RangeAggregate
					bound, err = sqltype.BindRangeAggregate(input.Type, p.Operation())
					if err == nil && bound.ResultUsage() != result {
						err = fmt.Errorf("stuntgres: inconsistent remapped range aggregate result")
					}
					if err == nil {
						copy.Calls[i].Provider = functions.NewRangeAggregateProvider(input, result, p.Operation(), bound)
					}
				}
			default:
				return nil, fmt.Errorf("stuntgres: unpersistable view aggregate")
			}
			if call.Argument != nil {
				copy.Calls[i].Argument = expression(call.Argument)
			}
			if call.Separator != nil {
				copy.Calls[i].Separator = expression(call.Separator)
			}
			copy.Calls[i].Direct = expressions(call.Direct)
			if call.Filter != nil {
				copy.Calls[i].Filter = expression(call.Filter)
			}
			copy.Calls[i].Distinct = make([]queryGrouping, len(call.Distinct))
			for k := range call.Distinct {
				copy.Calls[i].Distinct[k] = call.Distinct[k]
				copy.Calls[i].Distinct[k].Routine = ref(call.Distinct[k].Routine)
				arg := copy.Calls[i].Argument
				if k == 1 {
					arg = copy.Calls[i].Separator
				}
				if err == nil {
					copy.Calls[i].Distinct[k].Grouping, err = sqltype.BindGrouping(arg.ResultUsage())
				}
				if err == nil {
					_, err = functions.AggregateOrdering(arg.ResultUsage(), arg.SourcePosition(), true)
				}
			}
			copy.Calls[i].Order = append([]sortKey(nil), call.Order...)
			for k, key := range call.Order {
				copy.Calls[i].Order[k].Expression = expression(key.Expression)
				if err == nil {
					usage := copy.Calls[i].Order[k].Expression.ResultUsage()
					if key.Slot == 0 {
						usage = copy.Calls[i].Argument.ResultUsage()
					} else if key.Slot == 1 {
						usage = copy.Calls[i].Separator.ResultUsage()
					} else if key.Slot >= 2 {
						usage = copy.Calls[i].Order[key.Slot-2].Expression.ResultUsage()
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

// Ordinary descriptor renaming reowns only consumers of changed type
// generations; replacement additionally remaps every imported object reference.
func catalogExpressionTypesChanged(d *catalog.Relation, typ func(*sqltype.Descriptor) *sqltype.Descriptor) bool {
	typ = recordTypeMapper(typ)
	changed := false
	expression := func(node expr.Expr) {
		walkQueryArgumentExpression(node, func(child expr.Expr) {
			old := child.ResultUsage().Type
			changed = changed || old != typ(old)
		})
	}
	for _, definition := range d.ColumnMeta.Defaults {
		expression(definition.Expression)
	}
	for _, definition := range d.ColumnMeta.Generations {
		expression(definition.Expression)
		expression(definition.Planned)
	}
	for _, constraint := range d.Constraints.Check {
		expression(constraint.Expression)
	}
	for _, policy := range d.Security.Policies {
		for _, condition := range []*policyExpression{policyExpressionOf(policy.Using), policyExpressionOf(policy.Check)} {
			if condition != nil {
				expression(condition.expression)
				changed = changed || queryDescriptorsChanged(condition.presentation.query.Root, typ)
				for _, usage := range condition.presentation.castUsages {
					changed = changed || usage.Type != typ(usage.Type)
				}
				for _, captured := range condition.presentation.composites {
					changed = changed || captured.base.Type != typ(captured.base.Type)
				}
			}
		}
	}
	if d.SQLIndex != nil {
		for _, key := range d.SQLIndex.Keys {
			if key.Derived != nil {
				expression(key.Derived.Definition)
				expression(key.Derived.Execution)
			}
		}
		if predicate := d.SQLIndex.Predicate; predicate != nil {
			expression(predicate.Definition)
			expression(predicate.Execution)
		}
	}
	if d.Partition != nil {
		for _, key := range d.Partition.Keys {
			changed = changed || key.Usage.Type != typ(key.Usage.Type)
			if key.Derived != nil {
				expression(key.Derived.Definition)
				expression(key.Derived.Execution)
			}
		}
	}
	if d.Membership != nil {
		for _, tuple := range [][]catalog.PartitionDatum{d.Membership.Lower, d.Membership.Upper} {
			for _, datum := range tuple {
				if datum.Kind == 0 {
					changed = changed || datum.Value.Type() != typ(datum.Value.Type())
				}
			}
		}
	}
	if d.View != nil {
		for _, usage := range viewOf(d).castUsages {
			changed = changed || usage.Type != typ(usage.Type)
		}
		for _, captured := range viewOf(d).composites {
			changed = changed || captured.base.Type != typ(captured.base.Type)
		}
		for _, literal := range viewOf(d).literals {
			expression(literal)
		}
		changed = changed || queryDescriptorsChanged(viewOf(d).query.Root, typ)
	}
	return changed
}

func (e *Engine) remapCatalogExpressions(next *catalog.State, mapping map[ids.ObjectAddress]ids.ObjectAddress) error {
	return e.reownCatalogExpressions(next, mapping, false)
}

// Imported rules have identities outside the descriptor address map. Reserve
// them before allocating replacements, including collisions with retained TEMP
// objects and relation identities remapped earlier in the private candidate.
func (e *Engine) remapViewRuleOIDs(next *catalog.State) error {
	const descriptorOID, ruleOID, claimedOID = uint8(1), uint8(2), uint8(4)
	reserved := make(map[ids.OID]uint8, next.DescriptorCount())
	var views []*catalog.Relation
	for address, descriptor := range next.Descriptors() {
		reserved[address.ObjectID] |= descriptorOID
		if view, ok := descriptor.(*catalog.Relation); ok && view.View != nil {
			if viewOf(view).rule == 0 {
				return fmt.Errorf("stuntgres: invalid imported view rule identity")
			}
			reserved[viewOf(view).rule] |= ruleOID
			views = append(views, view)
		}
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Address().ObjectID < views[j].Address().ObjectID })
	for _, view := range views {
		rule := viewOf(view).rule
		if reserved[rule]&(descriptorOID|claimedOID) != 0 {
			for {
				candidate, err := e.catalog.allocateOIDLocked()
				if err != nil {
					return err
				}
				if reserved[candidate] != 0 {
					continue
				}
				rule = candidate
				break
			}
			definition := *viewOf(view)
			definition.rule = rule
			replacement := *view
			replacement.View = &definition
			next.SetDescriptor(view.Address(), &replacement)
		}
		reserved[rule] |= ruleOID | claimedOID
	}
	return e.remapDefinitionOIDsLocked(next)
}

func (m *viewQueryRemapper) cte(old *cteDefinition) (*cteDefinition, error) {
	if mapped := m.ctes[old]; mapped != nil {
		return mapped, nil
	}
	if m.ctes == nil {
		m.ctes = make(map[*cteDefinition]*cteDefinition)
	}
	copy := *old
	m.ctes[old] = &copy
	copy.Schema = append(querySchema(nil), old.Schema...)
	for i := range copy.Schema {
		copy.Schema[i].Usage.Type = m.typ(copy.Schema[i].Usage.Type)
		if m.collation != nil {
			collation, err := m.collation(copy.Schema[i].Usage.Collation)
			if err != nil {
				return nil, err
			}
			copy.Schema[i].Usage.Collation = collation
		}
	}
	if old.Search != nil {
		var err error
		copy.Search, err = m.search(old.Search)
		if err != nil {
			return nil, err
		}
	}
	if old.Cycle != nil {
		var err error
		copy.Cycle, err = m.cycle(old.Cycle)
		if err != nil {
			return nil, err
		}
	}
	if old.Query != nil {
		query := *old.Query
		root, err := m.nested(query.Root)
		if err != nil {
			return nil, err
		}
		query.Root = root
		copy.Query = &query
	}
	if old.Mutation != nil && m.mutation != nil {
		body, err := m.mutation(old)
		if err != nil {
			return nil, err
		}
		copy.Mutation = &body
	}
	return &copy, nil
}

func (m *viewQueryRemapper) cycle(old *cycleRecipe) (*cycleRecipe, error) {
	copy := *old
	copy.Record, copy.Path = m.typ(old.Record), m.typ(old.Path)
	if !sqltype.OccurrenceRecordArray(copy.Path) || copy.Path.Element() != copy.Record {
		return nil, ids.ErrCatalogReplaced
	}
	expression := func(old expr.Expr) (expr.Expr, error) {
		next, err := remapBoundExpressionWithQuery(old, m.ref, nil, m.nested, nil)
		if err != nil {
			return nil, err
		}
		if !m.referencesOnly {
			next, _, err = retagBoundTypesWithQuery(m.catalog, next, m.typ, nil, m.collation)
		}
		return next, err
	}
	var err error
	copy.Mark, err = expression(old.Mark)
	if err != nil {
		return nil, err
	}
	copy.Fallback, err = expression(old.Fallback)
	if err != nil {
		return nil, err
	}
	if m.referencesOnly {
		return &copy, nil
	}
	for i, usage := range old.TypedConstants {
		if usage.Type == nil {
			continue
		}
		usage.Type = m.typ(usage.Type)
		if m.collation != nil {
			usage.Collation, err = m.collation(usage.Collation)
			if err != nil {
				return nil, err
			}
		}
		copy.TypedConstants[i] = usage
	}
	copy.Grouping = make([]sqltype.Grouping, len(old.Grouping))
	for i := range copy.Grouping {
		copy.Grouping[i], err = sqltype.BindGrouping(copy.Record.CompositeMember(i).Usage)
		if err != nil {
			return nil, err
		}
	}
	boolean, ok := m.catalog.LookupType(sqltype.Bool)
	if !ok {
		return nil, ids.ErrCatalogReplaced
	}
	usage := copy.Mark.ResultUsage()
	copy.Inequality, err = sqltype.BindComparisonPredicate(sqltype.CompareNotEqual, usage.Type, usage.Type, boolean, usage.Collation, copy.Mark.CollationIndeterminate())
	if err != nil {
		return nil, err
	}
	return &copy, nil
}

func (m *viewQueryRemapper) search(old *searchRecipe) (*searchRecipe, error) {
	copy := *old
	copy.Record, copy.Result, copy.Depth = m.typ(old.Record), m.typ(old.Result), m.typ(old.Depth)
	if copy.Record == nil || copy.Record.ID() != 2249 || !copy.Record.IsComposite() ||
		old.Syntax.Breadth && copy.Result != copy.Record ||
		!old.Syntax.Breadth && (!sqltype.OccurrenceRecordArray(copy.Result) || copy.Result.Element() != copy.Record) {
		return nil, ids.ErrCatalogReplaced
	}
	copy.Grouping = make([]sqltype.Grouping, len(old.Grouping))
	for i := range copy.Grouping {
		var err error
		copy.Grouping[i], err = sqltype.BindGrouping(copy.Record.CompositeMember(i).Usage)
		if err != nil {
			return nil, err
		}
	}
	return &copy, nil
}

// remapQuoteOwner retargets the owner of a node's quote-capture origin, which
// the node carries as an opaque annotation.
func remapQuoteOwner(node expr.Expr, ref func(ids.ObjectRef) ids.ObjectRef) expr.Expr {
	annotated, ok := node.(expr.Annotated)
	if !ok {
		return node
	}
	origin, _ := annotated.Annotation().(quoteOrigin)
	if origin.owner == (ids.ObjectRef{}) {
		return node
	}
	origin.owner = ref(origin.owner)
	return annotated.WithAnnotation(origin)
}
