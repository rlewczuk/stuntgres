package engine

import (
	"fmt"
	"sort"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Replacement copies the closed bound tree; source and expression identities
// are reminted together. Syntax and frozen layouts are immutable and shared.
func remapBoundExpression(node BoundExpr, ref func(ObjectRef) ObjectRef, slots map[int32]int) (BoundExpr, error) {
	return remapBoundExpressionWithQuery(node, ref, slots, func(root LogicalNode) (LogicalNode, error) {
		return (&viewQueryRemapper{ref: ref, typ: func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }, referencesOnly: true}).query(root)
	}, nil)
}

func remapBoundExpressionWithQuery(node BoundExpr, ref func(ObjectRef) ObjectRef, slots map[int32]int, queryMapper func(LogicalNode) (LogicalNode, error), rowRef func(ObjectRef) ObjectRef) (BoundExpr, error) {
	if rowRef == nil {
		rowRef = ref
	}
	return remapScalarExpression(node, ref, rowRef, slots, queryMapper)
}

// Row copies retain explicit object constants and external calls. Replacement
// callers map both roles, whereas LIKE and partition inheritance supply a
// distinct owner-row mapper.
func remapScalarExpression(node BoundExpr, ref, rowRef func(ObjectRef) ObjectRef, slots map[int32]int, queryMapper func(LogicalNode) (LogicalNode, error)) (BoundExpr, error) {
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
		next = n
	case identityExpression, currentDateTimeExpression, transactionTimeExpression:
		next = n
	case literalExpression:
		if ref == nil {
			next = n
			break
		}
		if n.regclassRef != (ObjectRef{}) {
			n.regclassRef = ref(n.regclassRef)
		}
		if n.usage.Type.ContainsRegclass() && !n.value.IsNull() {
			var failure error
			n.value, failure = remapRegclassDatum(n.value, func(id OID) OID {
				return ref(ObjectRef{Address: ObjectAddress{ClassID: RelationClassID, ObjectID: id}}).Address.ObjectID
			})
			if failure != nil {
				return nil, failure
			}
		}
		if n.usage.Type.ContainsRegtype() && !n.value.IsNull() {
			var failure error
			n.value, failure = remapRegtypeDatum(n.value, func(id OID) OID {
				return ref(ObjectRef{Address: ObjectAddress{ClassID: TypeClassID, ObjectID: id}}).Address.ObjectID
			})
			if failure != nil {
				return nil, failure
			}
		}
		next = n
	case tableOIDExpression:
		n.owner = rowRef(n.owner)
		next = n
	case columnExpression:
		n.origin = rowRef(n.origin)
		if slots != nil {
			slot, ok := slots[n.origin.Address.SubID]
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing remapped row attribute")
			}
			n.slot = slot
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
	case timeZoneExpression:
		n.value = child(n.value)
		if !n.local {
			n.zone = child(n.zone)
		}
		next = n
	case bitwiseExpression:
		n.left, n.right = child(n.left), child(n.right)
		next = n
	case arithmeticExpression:
		n.left, n.right = child(n.left), child(n.right)
		next = n
	case likeExpression:
		n.left, n.right = child(n.left), child(n.right)
		next = n
	case likeEscapeExpression:
		n.pattern, n.escape = child(n.pattern), child(n.escape)
		next = n
	case similarExpression:
		n.left, n.right = child(n.left), child(n.right)
		next = n
	case similarEscapeExpression:
		n.pattern, n.escape = child(n.pattern), child(n.escape)
		next = n
	case similarSubstringExpression:
		for i := range n.args {
			n.args[i] = child(n.args[i])
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
	case binaryExpression:
		n.args = args(n.args)
		next = n
	case datetimeJustifyExpression:
		n.arg = child(n.arg)
		next = n
	case datetimeExtractExpression:
		for i := range n.args {
			n.args[i] = child(n.args[i])
		}
		next = n
	case datetimeOverlapsExpression:
		for i := range n.args {
			n.args[i] = child(n.args[i])
		}
		next = n
	case datetimeBinExpression:
		for i := range n.args {
			n.args[i] = child(n.args[i])
		}
		next = n
	case datetimeTruncExpression:
		for i := range n.children() {
			n.args[i] = child(n.args[i])
		}
		next = n
	case datetimeZoneCalendarExpression:
		for i := range n.children() {
			n.args[i] = child(n.args[i])
		}
		next = n
	case datetimeAgeFiniteExpression:
		for i := range n.children() {
			n.args[i] = child(n.args[i])
		}
		next = n
	case datetimeMakeExpression:
		for i := range n.children() {
			n.args[i] = child(n.args[i])
		}
		next = n
	case datetimeFormattingExpression:
		for i := range n.args {
			n.args[i] = child(n.args[i])
		}
		next = n
	case numericFormattingExpression:
		for i := range n.args {
			n.args[i] = child(n.args[i])
		}
		next = n
	case stringExpression:
		n.args = args(n.args)
		next = n
	case mathExpression:
		n.args = args(n.args)
		next = n
	case currentSchemasExpression:
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
	case nullCountExpression:
		n.args = args(n.args)
		next = n
	case formatExpression:
		n.args = args(n.args)
		next = n
	case randomExpression:
		n.args = args(n.args)
		next = n
	case advisoryExpression:
		n.args = args(n.args)
		next = n
	case uuidGenerationExpression:
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
	default:
		return nil, fmt.Errorf("stuntgres: unpersistable scalar expression %T", node)
	}
	return next, err
}

func remapViewQuery(view CatalogRead, node LogicalNode, ref func(ObjectRef) ObjectRef, typ func(*sqltype.Descriptor) *sqltype.Descriptor, collations ...collationRemapper) (LogicalNode, error) {
	var collation collationRemapper
	if len(collations) != 0 {
		collation = collations[0]
	}
	return (&viewQueryRemapper{catalog: view, ref: ref, typ: typ, collation: collation}).query(node)
}

type viewQueryRemapper struct {
	ref            func(ObjectRef) ObjectRef
	typ            func(*sqltype.Descriptor) *sqltype.Descriptor
	frames         map[uint32]LogicalNode
	collation      collationRemapper
	catalog        CatalogRead
	referencesOnly bool
}

func (m *viewQueryRemapper) nested(node LogicalNode) (LogicalNode, error) {
	project, ok := node.(*projectNode)
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing remapped query frame")
	}
	if root := m.frames[project.frame]; root != nil {
		return root, nil
	}
	root, err := m.query(node)
	if err != nil {
		return nil, err
	}
	if m.frames == nil {
		m.frames = make(map[uint32]LogicalNode)
	}
	m.frames[project.frame] = root
	return root, nil
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
			origin := ref(ObjectRef{Address: ObjectAddress{ClassID: RelationClassID, ObjectID: OID(schema[i].OriginTable)}})
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
		next := old
		if !m.referencesOnly {
			var failure error
			next, _, failure = retagBoundTypesWithQuery(m.catalog, old, typ, nil, m.collation)
			if failure != nil {
				err = failure
				return old
			}
		}
		next, err = remapBoundExpressionWithQuery(next, ref, nil, m.nested, nil)
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
	case *valuesNode:
		rows := make([][]BoundExpr, len(n.rows))
		for i, row := range n.rows {
			rows[i] = expressions(row)
		}
		next = &valuesNode{querySchema: schema, rows: rows, viewOrdinal: n.viewOrdinal, identityOrderDemand: append([]bool(nil), n.identityOrderDemand...)}
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
		next = &sourceNode{querySchema: schema, ref: ref(n.ref), occurrence: n.occurrence}
	case *joinNode:
		next = &joinNode{querySchema: schema, kind: n.kind, left: child(n.left), right: child(n.right), predicate: expression(n.predicate), leftWidth: n.leftWidth, rightWidth: n.rightWidth, merges: expressions(n.merges), viewOrdinal: n.viewOrdinal}
	case *singletonNode:
		next = &singletonNode{querySchema: schema}
	case *derivedNode:
		query := *n.query
		query.Root, err = m.nested(query.Root)
		next = &derivedNode{querySchema: schema, query: &query}
	case *projectNode:
		next = &projectNode{querySchema: schema, child: child(n.child), expressions: expressions(n.expressions), analysisExpressions: expressions(n.analysisExpressions), frozenValues: n.frozenValues, frame: n.frame, syntax: n.syntax}
	case *setNode:
		copy := *n
		copy.querySchema = schema
		if err == nil {
			var left LogicalNode
			left, err = m.nested(n.left)
			if err == nil {
				copy.left = left.(*projectNode)
			}
		}
		if err == nil {
			var right LogicalNode
			right, err = m.nested(n.right)
			if err == nil {
				copy.right = right.(*projectNode)
			}
		}
		copy.leftExpressions = expressions(n.leftExpressions)
		copy.rightExpressions = expressions(n.rightExpressions)
		copy.grouping = nil
		if !(copy.operation == "union" && copy.all) {
			copy.grouping = make([]sqltype.Grouping, len(schema))
			for i, column := range schema {
				if err == nil {
					copy.grouping[i], err = sqltype.BindGrouping(column.Usage)
				}
			}
		}
		next = &copy
	case *distinctNode:
		copy := *n
		copy.querySchema = schema
		copy.child = child(n.child)
		copy.expressions = expressions(n.expressions)
		copy.grouping = make([]sqltype.Grouping, len(copy.expressions))
		for i, expression := range copy.expressions {
			if err == nil {
				copy.grouping[i], err = sqltype.BindGrouping(expression.ResultUsage())
			}
		}
		next = &copy
	case *distinctOnNode:
		copy := *n
		copy.querySchema = schema
		copy.child = child(n.child)
		copy.expressions = expressions(n.expressions)
		copy.grouping = make([]sqltype.Grouping, len(copy.expressions))
		for i, expression := range copy.expressions {
			if err == nil {
				copy.grouping[i], err = sqltype.BindGrouping(expression.ResultUsage())
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
			spec.grouping = make([]sqltype.Grouping, len(spec.partition))
			for j, key := range spec.partition {
				if err == nil {
					spec.grouping[j], err = sqltype.BindGrouping(key.ResultUsage())
				}
			}
			spec.order = append([]sortKey(nil), spec.order...)
			for j, key := range spec.order {
				spec.order[j].expression = expression(key.expression)
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
			copy.calls[i].expression = expression(call.expression).(*windowExpression)
		}
		next = &copy
	case *aggregateNode:
		copy := *n
		copy.querySchema = schema
		copy.child = child(n.child)
		copy.keys = expressions(n.keys)
		copy.grouping = make([]sqltype.Grouping, len(copy.keys))
		for i, key := range copy.keys {
			if err == nil {
				copy.grouping[i], err = sqltype.BindGrouping(key.ResultUsage())
			}
		}
		copy.calls = make([]aggregateCall, len(n.calls))
		for i, call := range n.calls {
			copy.calls[i] = call
			switch p := call.provider.(type) {
			case countProvider:
				p.usage = usage(p.usage)
				copy.calls[i].provider = p
			case booleanAggregateProvider:
				p.usage = usage(p.usage)
				copy.calls[i].provider = p
			case intervalAvgProvider:
				p.usage = usage(p.usage)
				copy.calls[i].provider = p
			case numericAvgProvider:
				p.input = typ(p.input)
				p.usage = usage(p.usage)
				copy.calls[i].provider = p
			case floatAvgProvider:
				p.input = typ(p.input)
				p.usage = usage(p.usage)
				copy.calls[i].provider = p
			case integralSumProvider:
				p.input = typ(p.input)
				p.usage = usage(p.usage)
				copy.calls[i].provider = p
			case numericSumProvider:
				p.input = typ(p.input)
				p.usage = usage(p.usage)
				copy.calls[i].provider = p
			case floatSumProvider:
				p.input = typ(p.input)
				p.usage = usage(p.usage)
				copy.calls[i].provider = p
			case anyValueProvider:
				p.usage = usage(p.usage)
				copy.calls[i].provider = p
			case extremaProvider:
				p.usage = usage(p.usage)
				if err == nil {
					p.comparison, err = sqltype.BindComparison(p.usage.Type, p.usage.Type)
				}
				copy.calls[i].provider = p
			case stringAggregateProvider:
				p.usage = usage(p.usage)
				copy.calls[i].provider = p
			case *arrayAggregateProvider:
				provider := *p
				provider.argument = typ(p.argument)
				provider.usage = usage(p.usage)
				copy.calls[i].provider = &provider
			default:
				return nil, fmt.Errorf("stuntgres: unpersistable view aggregate")
			}
			if call.argument != nil {
				copy.calls[i].argument = expression(call.argument)
			}
			if call.separator != nil {
				copy.calls[i].separator = expression(call.separator)
			}
			if call.filter != nil {
				copy.calls[i].filter = expression(call.filter)
			}
			copy.calls[i].distinct = make([]sqltype.Grouping, len(call.distinct))
			for k := range call.distinct {
				arg := copy.calls[i].argument
				if k == 1 {
					arg = copy.calls[i].separator
				}
				if err == nil {
					copy.calls[i].distinct[k], err = sqltype.BindGrouping(arg.ResultUsage())
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
		for _, literal := range d.view.literals {
			expression(literal)
		}
		changed = changed || queryDescriptorsChanged(d.view.query.Root, typ)
	}
	return changed
}

func (e *Engine) remapCatalogExpressions(next *catalogState, mapping map[ObjectAddress]ObjectAddress) error {
	return e.reownCatalogExpressions(next, mapping, false)
}

// Imported rules have identities outside the descriptor address map. Reserve
// them before allocating replacements, including collisions with retained TEMP
// objects and relation identities remapped earlier in the private candidate.
func (e *Engine) remapViewRuleOIDs(next *catalogState) error {
	const descriptorOID, ruleOID, claimedOID = uint8(1), uint8(2), uint8(4)
	reserved := make(map[OID]uint8, len(next.descriptors))
	var views []*relationDescriptor
	for address, descriptor := range next.descriptors {
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
				candidate, err := e.allocateOIDLocked()
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
			next.descriptors[view.address] = &replacement
		}
		reserved[rule] |= ruleOID | claimedOID
	}
	return e.remapDefinitionOIDsLocked(next)
}
