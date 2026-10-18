package engine

import (
	"fmt"
	"sort"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Replacement copies the closed bound tree; source and expression identities
// are reminted together. Syntax and frozen layouts are immutable and shared.
func remapBoundExpression(node BoundExpr, ref func(ids.ObjectRef) ids.ObjectRef, slots map[int32]int) (BoundExpr, error) {
	return remapBoundExpressionWithQuery(node, ref, slots, func(root LogicalNode) (LogicalNode, error) {
		return (&viewQueryRemapper{ref: ref, typ: func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }, referencesOnly: true}).query(root)
	}, nil)
}

func remapBoundExpressionWithQuery(node BoundExpr, ref func(ids.ObjectRef) ids.ObjectRef, slots map[int32]int, queryMapper func(LogicalNode) (LogicalNode, error), rowRef func(ids.ObjectRef) ids.ObjectRef) (BoundExpr, error) {
	if rowRef == nil {
		rowRef = ref
	}
	return remapScalarExpression(node, ref, rowRef, slots, queryMapper)
}

// Row copies retain explicit object constants and external calls. Replacement
// callers map both roles, whereas LIKE and partition inheritance supply a
// distinct owner-row mapper.
func remapScalarExpression(node BoundExpr, ref, rowRef func(ids.ObjectRef) ids.ObjectRef, slots map[int32]int, queryMapper func(LogicalNode) (LogicalNode, error)) (BoundExpr, error) {
	if node == nil {
		return nil, nil
	}
	var err error
	child := func(old BoundExpr) BoundExpr {
		if err != nil {
			return old
		}
		var next BoundExpr
		next, err = remapScalarExpression(old, ref, rowRef, slots, queryMapper)
		return next
	}
	args := func(old []BoundExpr) []BoundExpr {
		next := make([]BoundExpr, len(old))
		for i, n := range old {
			next[i] = child(n)
		}
		return next
	}
	var next BoundExpr
	switch n := node.(type) {
	case assignmentValueExpression:
		n.base, n.value = child(n.base), child(n.value)
		targets, failure := mapRuleAssignmentTargets([]*arrayAssignmentTarget{n.target}, func(expr BoundExpr) (BoundExpr, bool, error) {
			if expr == nil {
				return nil, false, nil
			}
			return child(expr), true, err
		}, func(usage sqltype.Usage) (sqltype.Usage, error) { return usage, nil })
		if failure != nil {
			return nil, failure
		}
		n.target = targets[0]
		next = n
	case unknownOutput:
		// Captured contextual analysis carries immutable literal syntax only;
		// it is not a persisted scalar value or a remappable object reference.
		literal, ok := n.raw.(parser.LiteralExpr)
		if !ok || n.ResultUsage() != (sqltype.Usage{}) || literal.Value.Kind != parser.StringValue && literal.Value.Kind != parser.NullValue {
			return nil, fmt.Errorf("stuntgres: invalid contextual view output")
		}
		next = n
	case rowExpression:
		n, _, err = mapRowAnalysis(n, func(old BoundExpr) (BoundExpr, bool, error) { next := child(old); return next, true, err })
		next = n
	case fieldExpression:
		n.base = child(n.base)
		next = n
	case arrayExpression:
		n.elements = args(n.elements)
		next = n
	case subscriptExpression:
		n, _, err = mapSubscript(n, func(old BoundExpr) (BoundExpr, bool, error) {
			if old == nil {
				return nil, false, nil
			}
			next := child(old)
			return next, true, err
		})
		next = n
	case subqueryExpression:
		if queryMapper != nil {
			root, failure := queryMapper(n.queryPlan.Root)
			if failure != nil {
				return nil, failure
			}
			copy := *n.queryPlan
			copy.Root = root
			n.queryPlan = &copy
		}
		n.comparisons = append([]comparisonExpression(nil), n.comparisons...)
		for i, c := range n.comparisons {
			mapped := child(c)
			if err != nil {
				return nil, err
			}
			n.comparisons[i] = mapped.(comparisonExpression)
		}
		n.leftExpansions, _, err = mapRowComparisonExpansions(n.leftExpansions, func(old BoundExpr) (BoundExpr, bool, error) {
			next := child(old)
			return next, true, err
		})
		if n.leftConstructor != nil {
			n.leftConstructor = child(n.leftConstructor)
		}
		next = n
	case domainValueExpression, identityExpression:
		next = n
	case literalExpression:
		if ref == nil {
			next = n
			break
		}
		if n.RegclassRef != (ids.ObjectRef{}) {
			n.RegclassRef = ref(n.RegclassRef)
		}
		if n.Usage.Type.ContainsRegclass() && !n.value.IsNull() {
			var failure error
			n.value, failure = remapRegclassDatum(n.value, func(id ids.OID) ids.OID {
				return ref(ids.ObjectRef{Address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}}).Address.ObjectID
			})
			if failure != nil {
				return nil, failure
			}
		}
		if (n.Usage.Type.ContainsRegtype() || n.Usage.Type.ContainsRegObject()) && !n.value.IsNull() {
			var failure error
			n.value, failure = remapCatalogAliasDatum(n.value, func(address ids.ObjectAddress) ids.ObjectAddress {
				return ref(ids.ObjectRef{Address: address}).Address
			})
			if failure != nil {
				return nil, failure
			}
		}
		if n.Usage.Type.ContainsRegproc() && !n.value.IsNull() {
			var failure error
			n.value, failure = remapRoutineDatum(n.value, func(id ids.OID) ids.OID {
				return ref(ids.ObjectRef{Address: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}}).Address.ObjectID
			})
			if failure != nil {
				return nil, failure
			}
		}
		if n.Usage.Type.ContainsOperatorAlias() && !n.value.IsNull() {
			var failure error
			n.value, failure = remapOperatorDatum(n.value, func(id ids.OID) ids.OID {
				return ref(ids.ObjectRef{Address: ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: id}}).Address.ObjectID
			})
			if failure != nil {
				return nil, failure
			}
		}
		if n.Usage.Type.ContainsRegTS() && !n.value.IsNull() {
			var failure error
			n.value, failure = remapRegTSDatum(n.value, func(address ids.ObjectAddress) ids.ObjectAddress {
				return ref(ids.ObjectRef{Address: address}).Address
			})
			if failure != nil {
				return nil, failure
			}
		}
		next = n
	case tableOIDExpression:
		n.owner = rowRef(n.owner)
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
		n.value = child(n.value)
		next = n
	case returningWholeRowExpression:
		n.members = args(n.members)
		next = n
	case mergeActionExpression:
		next = n
	case columnExpression:
		n.origin = rowRef(n.origin)
		if ref == nil && slots != nil {
			// Owner-row copies carry attribute identity only. CREATE attaches
			// the completed destination's recipe and full layout afterward.
			n.virtual = nil
			n.rowStart, n.rowWidth, n.queryOccurrence = 0, 0, 0
		} else {
			n.virtual = child(n.virtual)
		}
		if slots != nil {
			slot, ok := slots[n.origin.Address.SubID]
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing remapped row attribute")
			}
			n.slot = slot
			if n.virtual != nil {
				n.rowStart = 0
				n.rowWidth = len(slots)
			}
		}
		next = n
	case groupExpression:
		next = n
	case aggregateExpression:
		next = n
	case groupingExpression:
		n.args = args(n.args)
		next = n
	case *windowExpression:
		copy := *n
		next = &copy
	case cachedOrderExpression:
		next = n // The cache slot belongs to this immutable query plan.
	case collationExpression:
		n.child = child(n.child)
		next = n
	case coercionExpression:
		n.child = child(n.child)
		next = n
	case bitwiseExpression:
		n.left, n.right = child(n.left), child(n.right)
		next = n
	case selectedOperatorExpression:
		n.provider = child(n.provider)
		if ref != nil {
			n.ref = ref(n.ref)
		}
		next = n
	case arithmeticExpression:
		n.left, n.right = child(n.left), child(n.right)
		next = n
	case seriesExpression:
		n.args = args(n.args)
		next = n
	case subscriptsExpression:
		n.args = args(n.args)
		next = n
	case unnestExpression:
		n.args = args(n.args)
		if ref != nil && n.procedure != nil {
			n.ref = ref(n.ref)
		}
		next = n
	case likeExpression:
		n.left, n.right = child(n.left), child(n.right)
		next = n
	case rowComparisonExpression:
		n.comparisons = append([]comparisonExpression(nil), n.comparisons...)
		for i, pair := range n.comparisons {
			n.comparisons[i] = child(pair).(comparisonExpression)
		}
		if err != nil {
			return nil, err
		}
		for side, expansions := range n.expansions {
			n.expansions[side], _, err = mapRowComparisonExpansions(expansions, func(old BoundExpr) (BoundExpr, bool, error) {
				next := child(old)
				return next, true, err
			})
			if err != nil {
				return nil, err
			}
		}
		next = n
	case scalarArrayComparisonExpression:
		n.left = child(n.left)
		if n.array != nil {
			n.array = child(n.array)
		}
		n.items = append([]BoundExpr(nil), n.items...)
		for i := range n.items {
			n.items[i] = child(n.items[i])
		}
		next = n
	case comparisonExpression:
		n.left, n.right = child(n.left), child(n.right)
		next = n
	case nullTestExpression:
		n.child = child(n.child)
		next = n
	case truthTestExpression:
		n.child = child(n.child)
		next = n
	case distinctExpression:
		n.left, n.right = child(n.left), child(n.right)
		next = n
	case logicalExpression:
		n.left, n.right = child(n.left), child(n.right)
		next = n
	case conditionalExpression:
		n.args = args(n.args)
		next = n
	case catalogUserNameExpression:
		n.arg = child(n.arg)
		next = n
	case catalogForeignKeysExpression:
		next = n
	case transactionIdentityExpression:
		if n.arg != nil {
			n.arg = child(n.arg)
		}
		next = n
	case commitTimestampExpression:
		if n.arg != nil {
			n.arg = child(n.arg)
		}
		next = n
	case currentSchemasExpression:
		n.arg = child(n.arg)
		next = n
	case objectAddressExpression:
		for i := range n.args {
			n.args[i] = child(n.args[i])
		}
		next = n
	case aclValueExpression:
		for i := range n.arguments() {
			n.args[i] = child(n.args[i])
		}
		next = n
	case objectACLExpression:
		for i := range n.args {
			n.args[i] = child(n.args[i])
		}
		next = n
	case accessInquiryExpression:
		for i := range n.count {
			n.args[i] = child(n.args[i])
		}
		next = n
	case namespaceInquiryExpression:
		if n.arg != nil {
			n.arg = child(n.arg)
		}
		next = n
	case blockingInquiryExpression:
		n.arg = child(n.arg)
		next = n
	case indexPropertyExpression:
		for i := range n.children() {
			n.args[i] = child(n.args[i])
		}
		next = n
	case deparseExpression:
		n.args = args(n.args)
		next = n
	case visibilityInquiryExpression:
		n.arg = child(n.arg)
		next = n
	case commentInquiryExpression:
		n.args = args(n.args)
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
	case sqlJSONExpression:
		n.args = args(n.args)
		if err == nil {
			next, err = n.reconstruct(n.expressionInfo, n.args)
		}
	case jsonPredicateExpression:
		n.child = child(n.child)
		next = n
	case advisoryExpression:
		n.args = args(n.args)
		next = n
	case sequenceCallExpression:
		if ref != nil {
			n.ref = ref(n.ref)
		}
		n.args = args(n.args)
		next = n
	case caseExpression:
		n.test, n.otherwise = child(n.test), child(n.otherwise)
		n.arms = append([]caseBranch(nil), n.arms...)
		for i := range n.arms {
			n.arms[i].condition, n.arms[i].result = child(n.arms[i].condition), child(n.arms[i].result)
		}
		next = n
	case expr.Composite:
		// Last, so the explicit engine wrappers above keep precedence. Remap
		// copies the whole tree, so every child counts as replaced.
		next, _, err = expr.Map(n, func(old BoundExpr) (BoundExpr, bool, error) {
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

func remapViewQuery(view CatalogRead, node LogicalNode, ref func(ids.ObjectRef) ids.ObjectRef, typ func(*sqltype.Descriptor) *sqltype.Descriptor, collations ...collationRemapper) (LogicalNode, error) {
	typ = recordTypeMapper(typ, collations...)
	return remapViewQueryMapped(view, node, ref, typ, collations...)
}

func remapViewQueryMapped(view CatalogRead, node LogicalNode, ref func(ids.ObjectRef) ids.ObjectRef, typ func(*sqltype.Descriptor) *sqltype.Descriptor, collations ...collationRemapper) (LogicalNode, error) {
	var collation collationRemapper
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
	collation      collationRemapper
	catalog        CatalogRead
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
	expression := func(old BoundExpr) BoundExpr {
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
	expressions := func(old []BoundExpr) []BoundExpr {
		next := make([]BoundExpr, len(old))
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
		copy.calls = append([]BoundExpr(nil), n.calls...)
		for i, call := range copy.calls {
			next := expression(call)
			if err == nil {
				copy.calls[i] = next
			}
		}
		next = &copy
	case *setProjectNode:
		copy := *n
		copy.querySchema = schema
		copy.child = child(n.child)
		copy.scalars = expressions(n.scalars)
		if n.template != nil {
			template := *n.template
			template.expressions, template.origins, template.scalars = expressions(n.template.expressions), expressions(n.template.origins), expressions(n.template.scalars)
			copy.template = &template
		}
		copy.calls = append([]BoundExpr(nil), n.calls...)
		for i, call := range copy.calls {
			next := expression(call)
			if err == nil {
				copy.calls[i] = next
			}
		}
		next = &copy
	case *valuesNode:
		rows := make([][]BoundExpr, len(n.rows))
		for i, row := range n.rows {
			rows[i] = expressions(row)
		}
		next = &valuesNode{querySchema: schema, rows: rows, viewOrdinal: n.viewOrdinal, identityOrderDemand: append([]bool(nil), n.identityOrderDemand...), presenceSlot: n.presenceSlot}
	case *limitNode:
		copy := &limitNode{querySchema: schema, child: child(n.child), offset: expression(n.offset), count: expression(n.count), withTies: n.withTies, presenceOnly: n.presenceOnly}
		if err == nil {
			copy.finishTies(copy.child)
		}
		next = copy
	case *evaluationNode:
		next = &evaluationNode{querySchema: schema, child: child(n.child), expressions: expressions(n.expressions), base: n.base}
	case *rowLockNode:
		next = &rowLockNode{querySchema: schema, child: child(n.child), marks: n.marks, firstMode: n.firstMode}
	case *sourceNode:
		next = &sourceNode{querySchema: schema, ref: ref(n.ref), occurrence: n.occurrence, presenceSlot: n.presenceSlot}
	case *joinNode:
		next = &joinNode{querySchema: schema, kind: n.kind, left: child(n.left), right: child(n.right), predicate: expression(n.predicate), leftWidth: n.leftWidth, rightWidth: n.rightWidth, merges: expressions(n.merges), viewOrdinal: n.viewOrdinal, correlatedRight: n.correlatedRight, frame: n.frame, correlationPrefix: n.correlationPrefix, captures: n.captures, presenceSlot: n.presenceSlot}
	case *singletonNode:
		next = &singletonNode{querySchema: schema}
	case *worktableScanNode:
		copy := *n
		copy.querySchema = schema
		copy.cte, err = m.cte(n.cte)
		next = &copy
	case *derivedNode:
		copy := *n
		copy.querySchema = schema
		if n.cte != nil {
			copy.cte, err = m.cte(n.cte)
		}
		if err == nil && n.query != nil {
			query := *n.query
			query.Root, err = m.nested(query.Root)
			copy.query = &query
		}
		next = &copy
	case *projectNode:
		next = &projectNode{querySchema: schema, child: child(n.child), expressions: expressions(n.expressions), analysisExpressions: expressions(n.analysisExpressions), frozenValues: n.frozenValues, frame: n.frame, syntax: n.syntax, cteOwner: n.cteOwner, hasTargetSet: n.hasTargetSet}
	case *setNode, *recursiveUnionNode:
		set := querySetNode(node)
		copy := *set
		copy.querySchema = schema
		var definition *cteDefinition
		if recursive, ok := node.(*recursiveUnionNode); ok {
			definition, err = m.cte(recursive.definition)
		}
		if err == nil {
			var left LogicalNode
			left, err = m.nested(set.left)
			if err == nil {
				copy.left = left.(*projectNode)
			}
		}
		if err == nil {
			var right LogicalNode
			right, err = m.nested(set.right)
			if err == nil {
				copy.right = right.(*projectNode)
			}
		}
		copy.leftExpressions = expressions(set.leftExpressions)
		copy.rightExpressions = expressions(set.rightExpressions)
		copy.grouping = nil
		if !(copy.operation == "union" && copy.all) {
			width := len(set.grouping)
			copy.grouping = make([]queryGrouping, width)
			for i := range copy.grouping {
				slot := i
				if definition != nil && definition.cycle != nil && i == definition.userWidth {
					slot = definition.cycle.markSlot
				}
				copy.grouping[i] = set.grouping[i]
				copy.grouping[i].routine = ref(set.grouping[i].routine)
				if err == nil {
					copy.grouping[i].Grouping, err = sqltype.BindGrouping(schema[slot].Usage)
				}
			}
		}
		if definition != nil {
			cycle := definition.cycle
			if original := node.(*recursiveUnionNode).cycle; original != nil && original != node.(*recursiveUnionNode).definition.cycle {
				cycle, err = m.cycle(original)
			}
			next = &recursiveUnionNode{setNode: copy, definition: definition, search: definition.search, cycle: cycle}
		} else {
			next = &copy
		}
	case *distinctNode:
		copy := *n
		copy.querySchema = schema
		copy.child = child(n.child)
		copy.expressions = expressions(n.expressions)
		copy.grouping = make([]queryGrouping, len(copy.expressions))
		for i, expression := range copy.expressions {
			copy.grouping[i] = n.grouping[i]
			copy.grouping[i].routine = ref(n.grouping[i].routine)
			if err == nil {
				copy.grouping[i].Grouping, err = sqltype.BindGrouping(expression.ResultUsage())
			}
		}
		next = &copy
	case *distinctOnNode:
		copy := *n
		copy.querySchema = schema
		copy.child = child(n.child)
		copy.expressions = expressions(n.expressions)
		copy.grouping = make([]queryGrouping, len(copy.expressions))
		for i, expression := range copy.expressions {
			copy.grouping[i] = n.grouping[i]
			copy.grouping[i].routine = ref(n.grouping[i].routine)
			if err == nil {
				copy.grouping[i].Grouping, err = sqltype.BindGrouping(expression.ResultUsage())
			}
		}
		next = &copy
	case *filterNode:
		next = &filterNode{querySchema: schema, child: child(n.child), predicate: expression(n.predicate)}
	case *sortNode:
		copy := *n
		copy.querySchema = schema
		copy.child = child(n.child)
		copy.keys = append([]sortKey(nil), n.keys...)
		copy.outputs = expressions(n.outputs)
		for i, key := range copy.keys {
			copy.keys[i].expression = expression(key.expression)
			if err == nil && !copy.keys[i].expression.CollationIndeterminate() {
				copy.keys[i].comparison, err = sqltype.BindOrdering(copy.keys[i].expression.ResultUsage())
			}
		}
		next = &copy
	case *windowNode:
		copy := *n
		copy.querySchema = schema
		copy.child = child(n.child)
		copy.specs = append([]boundWindowSpec(nil), n.specs...)
		for i, spec := range copy.specs {
			spec.partition = expressions(spec.partition)
			spec.grouping = make([]queryGrouping, len(spec.partition))
			for j, key := range spec.partition {
				spec.grouping[j] = n.specs[i].grouping[j]
				spec.grouping[j].routine = ref(n.specs[i].grouping[j].routine)
				if err == nil {
					spec.grouping[j].Grouping, err = sqltype.BindGrouping(key.ResultUsage())
				}
			}
			spec.order = append([]sortKey(nil), spec.order...)
			for j, key := range spec.order {
				spec.order[j].expression = expression(key.expression)
				spec.order[j].equalityRoutine = ref(key.equalityRoutine)
				if err == nil {
					spec.order[j].comparison, err = sqltype.BindOrdering(spec.order[j].expression.ResultUsage())
				}
			}
			for _, bound := range []*boundWindowBound{&spec.frame.start, &spec.frame.end} {
				bound.offset = expression(bound.offset)
				if err == nil && spec.frame.unit == parser.FrameRange {
					bound.support, err = bound.support.Remap(m.typ)
				}
			}
			copy.specs[i] = spec
		}
		copy.calls = append([]windowCall(nil), n.calls...)
		for i, call := range copy.calls {
			copy.calls[i].routine = ref(call.routine)
			copy.calls[i].expression = expression(call.expression).(*windowExpression)
			copy.calls[i].argument = expression(call.argument)
			copy.calls[i].ordinal = expression(call.ordinal)
			copy.calls[i].defaultValue = expression(call.defaultValue)
			switch p := call.provider.(type) {
			case functions.IntegralSum:
				copy.calls[i].provider = p.Reconstruct(typ(p.InputType()), usage(p.ResultUsage()))
			case functions.NumericSum:
				copy.calls[i].provider = p.Reconstruct(typ(p.InputType()), usage(p.ResultUsage()))
			case functions.FloatSum:
				copy.calls[i].provider = p.Reconstruct(typ(p.InputType()), usage(p.ResultUsage()))
			}
		}
		next = &copy
	case *aggregateNode:
		copy := *n
		copy.querySchema = schema
		copy.child = child(n.child)
		copy.keys = expressions(n.keys)
		copy.grouping = make([]queryGrouping, len(copy.keys))
		for i, key := range copy.keys {
			copy.grouping[i] = n.grouping[i]
			copy.grouping[i].routine = ref(n.grouping[i].routine)
			if err == nil {
				copy.grouping[i].Grouping, err = sqltype.BindGrouping(key.ResultUsage())
			}
		}
		copy.calls = make([]aggregateCall, len(n.calls))
		for i, call := range n.calls {
			copy.calls[i] = call
			if call.procedure != nil && ref != nil {
				copy.calls[i].ref = ref(call.ref)
				if !m.referencesOnly {
					proc, ok := m.catalog.Procedure(copy.calls[i].ref.Address)
					if !ok {
						return nil, fmt.Errorf("stuntgres: missing remapped intagg aggregate")
					}
					copy.calls[i].procedure = proc
					if failure := validateIntaggIdentity(m.catalog, proc, copy.calls[i].ref, "int_array_aggregate"); failure != nil {
						return nil, failure
					}
				}
			}
			copy.calls[i].routine = ref(call.routine)
			switch p := call.provider.(type) {
			case *hypotheticalSetProvider:
				copy.calls[i].provider = &hypotheticalSetProvider{usage: usage(p.usage), operation: p.operation}
			case *orderedSetProvider:
				provider := *p
				p = &provider
				p.usage = usage(p.usage)
				p.input, p.fraction = typ(p.input), typ(p.fraction)
				if err == nil {
					inputUsage := usage(call.argument.ResultUsage())
					if err == nil {
						p.comparison, err = sqltype.BindOrdering(inputUsage)
					}
				}
				if err == nil && p.operation == functions.AggregatePercentileCont && p.input.ID() == sqltype.Interval {
					p.subtract, err = sqltype.BindArithmetic(sqltype.ArithmeticSubtract, p.input, p.input, p.input)
					if err == nil {
						p.multiply, err = sqltype.BindArithmetic(sqltype.ArithmeticMultiply, p.input, p.fraction, p.input)
					}
					if err == nil {
						p.add, err = sqltype.BindArithmetic(sqltype.ArithmeticAdd, p.input, p.input, p.input)
					}
				}
				copy.calls[i].provider = p
			case functions.CountProvider:
				copy.calls[i].provider = functions.NewCountProvider(usage(p.ResultUsage()))
			case functions.BooleanAggregateProvider:
				copy.calls[i].provider = functions.NewBooleanAggregateProvider(usage(p.ResultUsage()), p.Conjunction())
			case functions.IntervalAvg:
				copy.calls[i].provider = p.Reconstruct(usage(p.ResultUsage()))
			case functions.NumericAvg:
				copy.calls[i].provider = p.Reconstruct(typ(p.InputType()), usage(p.ResultUsage()))
			case functions.FloatAvg:
				copy.calls[i].provider = p.Reconstruct(typ(p.InputType()), usage(p.ResultUsage()))
			case functions.StatisticalAggregate:
				copy.calls[i].provider = p.Reconstruct(typ(p.InputType()), usage(p.ResultUsage()))
			case functions.RegressionAggregate:
				copy.calls[i].provider = p.Reconstruct(typ(p.InputType()), usage(p.ResultUsage()))
			case functions.RegressionCountAggregate:
				copy.calls[i].provider = p.Reconstruct(typ(p.InputType()), usage(p.ResultUsage()))
			case functions.IntegralBitwiseAggregateProvider:
				u := usage(p.ResultUsage())
				if err == nil {
					var bound sqltype.Bitwise
					bound, err = sqltype.BindBitwise(p.Operator(), u.Type, u.Type)
					if err == nil {
						copy.calls[i].provider = functions.NewIntegralBitwiseAggregateProvider(u, p.Operator(), bound)
					}
				}
			case functions.MoneySum:
				u := usage(p.ResultUsage())
				if err == nil {
					var rebound functions.MoneySum
					rebound, err = p.Reconstruct(u)
					copy.calls[i].provider = rebound
				}
			case functions.IntervalSum:
				copy.calls[i].provider = p.Reconstruct(usage(p.ResultUsage()))
			case functions.BitStringAggregateProvider:
				copy.calls[i].provider = functions.NewBitStringAggregateProvider(usage(p.ResultUsage()), p.Operator())
			case functions.IntegralSum:
				copy.calls[i].provider = p.Reconstruct(typ(p.InputType()), usage(p.ResultUsage()))
			case functions.NumericSum:
				copy.calls[i].provider = p.Reconstruct(typ(p.InputType()), usage(p.ResultUsage()))
			case functions.FloatSum:
				copy.calls[i].provider = p.Reconstruct(typ(p.InputType()), usage(p.ResultUsage()))
			case functions.AnyValue:
				copy.calls[i].provider = p.Reconstruct(usage(p.ResultUsage()))
			case functions.JSONArrayAggregateProvider:
				input, result := usage(p.InputUsage()), usage(p.ResultUsage())
				if err == nil {
					var plan sqltype.JSONArrayAggregatePlan
					plan, err = sqltype.BindJSONArrayAggregate(input, result, p.Absent())
					if err == nil {
						copy.calls[i].provider = functions.NewJSONArrayAggregateProvider(input, result, p.Absent(), plan)
					}
				}
			case jsonObjectAggregateProvider:
				p.key, p.input, p.usage = usage(p.key), usage(p.input), usage(p.usage)
				if err == nil {
					p.plan, err = sqltype.BindJSONObjectAggregate(p.key, p.input, p.usage, p.strict, p.unique)
				}
				copy.calls[i].provider = p
			case functions.ExtremaProvider:
				result := usage(p.ResultUsage())
				if err == nil {
					_, conflict := p.Comparison().InputCollation()
					var comparison sqltype.Comparison
					comparison, err = sqltype.BindComparison(result.Type, result.Type, result.Collation, conflict)
					if err == nil {
						copy.calls[i].provider = functions.NewExtremaProvider(result, comparison, p.Maximum())
					}
				}
			case functions.StringAggregateProvider:
				copy.calls[i].provider = functions.NewStringAggregateProvider(usage(p.ResultUsage()))
			case *functions.ArrayAggregateProvider:
				copy.calls[i].provider = functions.NewArrayAggregateProvider(typ(p.ArgumentType()), usage(p.ResultUsage()), p.Arrays())
			case functions.RangeAggregateProvider:
				input, result := usage(p.InputUsage()), usage(p.ResultUsage())
				if err == nil {
					var bound sqltype.RangeAggregate
					bound, err = sqltype.BindRangeAggregate(input.Type, p.Operation())
					if err == nil && bound.ResultUsage() != result {
						err = fmt.Errorf("stuntgres: inconsistent remapped range aggregate result")
					}
					if err == nil {
						copy.calls[i].provider = functions.NewRangeAggregateProvider(input, result, p.Operation(), bound)
					}
				}
			default:
				return nil, fmt.Errorf("stuntgres: unpersistable view aggregate")
			}
			if call.argument != nil {
				copy.calls[i].argument = expression(call.argument)
			}
			if call.separator != nil {
				copy.calls[i].separator = expression(call.separator)
			}
			copy.calls[i].direct = expressions(call.direct)
			if call.filter != nil {
				copy.calls[i].filter = expression(call.filter)
			}
			copy.calls[i].distinct = make([]queryGrouping, len(call.distinct))
			for k := range call.distinct {
				copy.calls[i].distinct[k] = call.distinct[k]
				copy.calls[i].distinct[k].routine = ref(call.distinct[k].routine)
				arg := copy.calls[i].argument
				if k == 1 {
					arg = copy.calls[i].separator
				}
				if err == nil {
					copy.calls[i].distinct[k].Grouping, err = sqltype.BindGrouping(arg.ResultUsage())
				}
				if err == nil {
					_, err = aggregateOrdering(arg.ResultUsage(), arg.SourcePosition(), true)
				}
			}
			copy.calls[i].order = append([]sortKey(nil), call.order...)
			for k, key := range call.order {
				copy.calls[i].order[k].expression = expression(key.expression)
				if err == nil {
					usage := copy.calls[i].order[k].expression.ResultUsage()
					if key.slot == 0 {
						usage = copy.calls[i].argument.ResultUsage()
					} else if key.slot == 1 {
						usage = copy.calls[i].separator.ResultUsage()
					} else if key.slot >= 2 {
						usage = copy.calls[i].order[key.slot-2].expression.ResultUsage()
					}
					copy.calls[i].order[k].comparison, err = aggregateOrdering(usage, key.expression.SourcePosition(), false)
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
func catalogExpressionTypesChanged(d *relationDescriptor, typ func(*sqltype.Descriptor) *sqltype.Descriptor) bool {
	typ = recordTypeMapper(typ)
	changed := false
	expression := func(node BoundExpr) {
		walkQueryArgumentExpression(node, func(child BoundExpr) {
			old := child.ResultUsage().Type
			changed = changed || old != typ(old)
		})
	}
	for _, definition := range d.defaults {
		expression(definition.expression)
	}
	for _, definition := range d.generations {
		expression(definition.expression)
		expression(definition.planned)
	}
	for _, constraint := range d.checkConstraints {
		expression(constraint.expression)
	}
	for _, policy := range d.policies {
		for _, condition := range []*policyExpression{policy.using, policy.check} {
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
	if d.sqlIndex != nil {
		for _, key := range d.sqlIndex.keys {
			if key.derived != nil {
				expression(key.derived.definition)
				expression(key.derived.execution)
			}
		}
		if predicate := d.sqlIndex.predicate; predicate != nil {
			expression(predicate.definition)
			expression(predicate.execution)
		}
	}
	if d.partition != nil {
		for _, key := range d.partition.keys {
			changed = changed || key.usage.Type != typ(key.usage.Type)
			if key.derived != nil {
				expression(key.derived.definition)
				expression(key.derived.execution)
			}
		}
	}
	if d.membership != nil {
		for _, tuple := range [][]partitionDatum{d.membership.lower, d.membership.upper} {
			for _, datum := range tuple {
				if datum.kind == 0 {
					changed = changed || datum.value.Type() != typ(datum.value.Type())
				}
			}
		}
	}
	if d.view != nil {
		for _, usage := range d.view.castUsages {
			changed = changed || usage.Type != typ(usage.Type)
		}
		for _, captured := range d.view.composites {
			changed = changed || captured.base.Type != typ(captured.base.Type)
		}
		for _, literal := range d.view.literals {
			expression(literal)
		}
		changed = changed || queryDescriptorsChanged(d.view.query.Root, typ)
	}
	return changed
}

func (e *Engine) remapCatalogExpressions(next *catalogState, mapping map[ids.ObjectAddress]ids.ObjectAddress) error {
	return e.reownCatalogExpressions(next, mapping, false)
}

// Imported rules have identities outside the descriptor address map. Reserve
// them before allocating replacements, including collisions with retained TEMP
// objects and relation identities remapped earlier in the private candidate.
func (e *Engine) remapViewRuleOIDs(next *catalogState) error {
	const descriptorOID, ruleOID, claimedOID = uint8(1), uint8(2), uint8(4)
	reserved := make(map[ids.OID]uint8, next.DescriptorCount())
	var views []*relationDescriptor
	for address, descriptor := range next.Descriptors() {
		reserved[address.ObjectID] |= descriptorOID
		if view, ok := descriptor.(*relationDescriptor); ok && view.view != nil {
			if view.view.rule == 0 {
				return fmt.Errorf("stuntgres: invalid imported view rule identity")
			}
			reserved[view.view.rule] |= ruleOID
			views = append(views, view)
		}
	}
	sort.Slice(views, func(i, j int) bool { return views[i].address.ObjectID < views[j].address.ObjectID })
	for _, view := range views {
		rule := view.view.rule
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
			definition := *view.view
			definition.rule = rule
			replacement := *view
			replacement.view = &definition
			next.setDescriptor(view.address, &replacement)
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
	copy.schema = append(querySchema(nil), old.schema...)
	for i := range copy.schema {
		copy.schema[i].Usage.Type = m.typ(copy.schema[i].Usage.Type)
		if m.collation != nil {
			collation, err := m.collation(copy.schema[i].Usage.Collation)
			if err != nil {
				return nil, err
			}
			copy.schema[i].Usage.Collation = collation
		}
	}
	if old.search != nil {
		var err error
		copy.search, err = m.search(old.search)
		if err != nil {
			return nil, err
		}
	}
	if old.cycle != nil {
		var err error
		copy.cycle, err = m.cycle(old.cycle)
		if err != nil {
			return nil, err
		}
	}
	if old.query != nil {
		query := *old.query
		root, err := m.nested(query.Root)
		if err != nil {
			return nil, err
		}
		query.Root = root
		copy.query = &query
	}
	if old.mutation != nil && m.mutation != nil {
		body, err := m.mutation(old)
		if err != nil {
			return nil, err
		}
		copy.mutation = &body
	}
	return &copy, nil
}

func (m *viewQueryRemapper) cycle(old *cycleRecipe) (*cycleRecipe, error) {
	copy := *old
	copy.record, copy.path = m.typ(old.record), m.typ(old.path)
	if !occurrenceRecordArray(copy.path) || copy.path.Element() != copy.record {
		return nil, ErrCatalogReplaced
	}
	expression := func(old BoundExpr) (BoundExpr, error) {
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
	copy.mark, err = expression(old.mark)
	if err != nil {
		return nil, err
	}
	copy.fallback, err = expression(old.fallback)
	if err != nil {
		return nil, err
	}
	if m.referencesOnly {
		return &copy, nil
	}
	for i, usage := range old.typedConstants {
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
		copy.typedConstants[i] = usage
	}
	copy.grouping = make([]sqltype.Grouping, len(old.grouping))
	for i := range copy.grouping {
		copy.grouping[i], err = sqltype.BindGrouping(copy.record.CompositeMember(i).Usage)
		if err != nil {
			return nil, err
		}
	}
	boolean, ok := m.catalog.LookupType(sqltype.Bool)
	if !ok {
		return nil, ErrCatalogReplaced
	}
	usage := copy.mark.ResultUsage()
	copy.inequality, err = sqltype.BindComparisonPredicate(sqltype.CompareNotEqual, usage.Type, usage.Type, boolean, usage.Collation, copy.mark.CollationIndeterminate())
	if err != nil {
		return nil, err
	}
	return &copy, nil
}

func (m *viewQueryRemapper) search(old *searchRecipe) (*searchRecipe, error) {
	copy := *old
	copy.record, copy.result, copy.depth = m.typ(old.record), m.typ(old.result), m.typ(old.depth)
	if copy.record == nil || copy.record.ID() != 2249 || !copy.record.IsComposite() ||
		old.syntax.Breadth && copy.result != copy.record ||
		!old.syntax.Breadth && (!occurrenceRecordArray(copy.result) || copy.result.Element() != copy.record) {
		return nil, ErrCatalogReplaced
	}
	copy.grouping = make([]sqltype.Grouping, len(old.grouping))
	for i := range copy.grouping {
		var err error
		copy.grouping[i], err = sqltype.BindGrouping(copy.record.CompositeMember(i).Usage)
		if err != nil {
			return nil, err
		}
	}
	return &copy, nil
}

// remapQuoteOwner retargets the owner of a node's quote-capture origin, which
// the node carries as an opaque annotation.
func remapQuoteOwner(node BoundExpr, ref func(ids.ObjectRef) ids.ObjectRef) BoundExpr {
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
