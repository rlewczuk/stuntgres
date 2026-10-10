package engine

import (
	"fmt"
	"sort"
	"stuntgres/internal/sqltype"
)

// Replacement copies the closed bound tree; source and expression identities
// are reminted together. Syntax and frozen layouts are immutable and shared.
func remapBoundExpression(node BoundExpr, ref func(ObjectRef) ObjectRef, slots map[int32]int) (BoundExpr, error) {
	return remapBoundExpressionWithQuery(node, ref, slots, func(root LogicalNode) (LogicalNode, error) {
		return remapViewQuery(root, ref, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ })
	})
}

func remapBoundExpressionWithQuery(node BoundExpr, ref func(ObjectRef) ObjectRef, slots map[int32]int, queryMapper func(LogicalNode) (LogicalNode, error)) (BoundExpr, error) {
	if node == nil {
		return nil, nil
	}
	var err error
	child := func(old BoundExpr) BoundExpr {
		if err != nil {
			return old
		}
		var next BoundExpr
		next, err = remapBoundExpressionWithQuery(old, ref, slots, queryMapper)
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
	case regclassArrayExpression:
		n.elements = args(n.elements)
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
		n.owner = ref(n.owner)
		next = n
	case columnExpression:
		n.origin = ref(n.origin)
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
	case uuidGenerationExpression:
		n.args = args(n.args)
		next = n
	case sequenceCallExpression:
		n.ref = ref(n.ref)
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

func remapViewQuery(node LogicalNode, ref func(ObjectRef) ObjectRef, typ func(*sqltype.Descriptor) *sqltype.Descriptor) (LogicalNode, error) {
	return (&viewQueryRemapper{ref: ref, typ: typ}).query(node)
}

type viewQueryRemapper struct {
	ref    func(ObjectRef) ObjectRef
	typ    func(*sqltype.Descriptor) *sqltype.Descriptor
	frames map[uint32]LogicalNode
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
		if schema[i].OriginTable != 0 {
			origin := ref(ObjectRef{Address: ObjectAddress{ClassID: RelationClassID, ObjectID: OID(schema[i].OriginTable)}})
			schema[i].OriginTable = uint32(origin.Address.ObjectID)
		}
	}
	var err error
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
		next, _, failure := retagBoundTypesWithQuery(old, typ, nil)
		if failure != nil {
			err = failure
			return old
		}
		next, err = remapBoundExpressionWithQuery(next, ref, nil, m.nested)
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
		next = &limitNode{querySchema: schema, child: child(n.child), offset: expression(n.offset), count: expression(n.count)}
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
				p.usage.Type = typ(p.usage.Type)
				copy.calls[i].provider = p
			case booleanAggregateProvider:
				p.usage.Type = typ(p.usage.Type)
				copy.calls[i].provider = p
			case intervalAvgProvider:
				p.usage.Type = typ(p.usage.Type)
				copy.calls[i].provider = p
			case numericAvgProvider:
				p.input = typ(p.input)
				p.usage.Type = typ(p.usage.Type)
				copy.calls[i].provider = p
			case floatAvgProvider:
				p.input = typ(p.input)
				p.usage.Type = typ(p.usage.Type)
				copy.calls[i].provider = p
			case integralSumProvider:
				p.input = typ(p.input)
				p.usage.Type = typ(p.usage.Type)
				copy.calls[i].provider = p
			case numericSumProvider:
				p.input = typ(p.input)
				p.usage.Type = typ(p.usage.Type)
				copy.calls[i].provider = p
			case floatSumProvider:
				p.input = typ(p.input)
				p.usage.Type = typ(p.usage.Type)
				copy.calls[i].provider = p
			case anyValueProvider:
				p.usage.Type = typ(p.usage.Type)
				copy.calls[i].provider = p
			case extremaProvider:
				p.usage.Type = typ(p.usage.Type)
				if err == nil {
					p.comparison, err = sqltype.BindComparison(p.usage.Type, p.usage.Type)
				}
				copy.calls[i].provider = p
			case stringAggregateProvider:
				p.usage.Type = typ(p.usage.Type)
				copy.calls[i].provider = p
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

func (e *Engine) reownCatalogExpressions(next *catalogState, mapping map[ObjectAddress]ObjectAddress, typesOnly bool) error {
	ref := func(old ObjectRef) ObjectRef {
		if old == (ObjectRef{}) || typesOnly {
			return old
		}
		address := old.Address
		address.SubID = 0
		if target, ok := mapping[address]; ok {
			target.SubID = old.Address.SubID
			old.Address = target
		}
		old.owner = e.owner
		old.Epoch = next.epoch
		return old
	}
	typ := func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == nil {
			return nil
		}
		address := ObjectAddress{ClassID: TypeClassID, ObjectID: OID(old.ID())}
		if target, ok := mapping[address]; ok {
			address = target
		}
		if d, ok := next.descriptors[address].(*typeDescriptor); ok {
			return d.typ
		}
		return old
	}
	for address, descriptor := range next.descriptors {
		d, ok := descriptor.(*relationDescriptor)
		if !ok {
			continue
		}
		if typesOnly && !catalogExpressionTypesChanged(d, typ) {
			continue
		}
		if len(d.defaults) != 0 {
			copy := *d
			copy.defaults = make(map[int32]columnDefault, len(d.defaults))
			for attribute, definition := range d.defaults {
				node, _, err := retagBoundTypes(definition.expression, typ)
				if err != nil {
					return err
				}
				node, err = remapBoundExpression(node, ref, nil)
				if err != nil {
					return err
				}
				definition.expression = node
				definition.dependencies = append([]ObjectRef(nil), definition.dependencies...)
				for i := range definition.dependencies {
					definition.dependencies[i] = ref(definition.dependencies[i])
				}
				copy.defaults[attribute] = definition
			}
			d = &copy
			next.descriptors[address] = d
		}
		if len(d.generations) != 0 {
			copy := *d
			copy.generations = make(map[int32]columnGeneration, len(d.generations))
			for attribute, definition := range d.generations {
				node, _, err := retagBoundTypes(definition.expression, typ)
				if err != nil {
					return err
				}
				node, err = remapBoundExpression(node, ref, nil)
				if err != nil {
					return err
				}
				definition.expression = node
				planned, _, err := retagBoundTypes(definition.planned, typ)
				if err != nil {
					return err
				}
				planned, err = remapBoundExpression(planned, ref, nil)
				if err != nil {
					return err
				}
				definition.planned = planned
				definition.dependencies = append([]ObjectRef(nil), definition.dependencies...)
				for i := range definition.dependencies {
					definition.dependencies[i] = ref(definition.dependencies[i])
				}
				copy.generations[attribute] = definition
			}
			d = &copy
			next.descriptors[address] = d
		}
		if len(d.checkConstraints) != 0 {
			copy := *d
			copy.checkConstraints = append([]checkConstraint(nil), d.checkConstraints...)
			for i := range copy.checkConstraints {
				c := &copy.checkConstraints[i]
				node, _, err := retagBoundTypes(c.expression, typ)
				if err != nil {
					return err
				}
				node, err = remapBoundExpression(node, ref, nil)
				if err != nil {
					return err
				}
				c.expression = node
				c.dependencies = append([]expressionDependency(nil), c.dependencies...)
				for j := range c.dependencies {
					c.dependencies[j].ref = ref(c.dependencies[j].ref)
				}
			}
			d = &copy
			next.descriptors[address] = d
		}
		if d.sqlIndex != nil {
			copy := *d
			definition := *d.sqlIndex
			definition.keys = append([]indexKey(nil), definition.keys...)
			for i, key := range definition.keys {
				if key.derived == nil {
					continue
				}
				derived := *key.derived
				node, _, err := retagBoundTypes(derived.definition, typ)
				if err != nil {
					return err
				}
				node, err = remapBoundExpression(node, ref, nil)
				if err != nil {
					return err
				}
				derived.definition = node
				node, _, err = retagBoundTypes(derived.execution, typ)
				if err != nil {
					return err
				}
				node, err = remapBoundExpression(node, ref, nil)
				if err != nil {
					return err
				}
				derived.execution = node
				derived.dependencies = append([]ObjectRef(nil), derived.dependencies...)
				for j := range derived.dependencies {
					derived.dependencies[j] = ref(derived.dependencies[j])
				}
				definition.keys[i].derived = &derived
			}
			copy.sqlIndex = &definition
			d = &copy
			next.descriptors[address] = d
		}
		if d.view == nil {
			continue
		}
		definition := *d.view
		// Namespace metadata has only labels, occurrence/frame slots and stable
		// attribute SubIDs. It is immutable and has no catalog-generation refs
		// or type pointers; the remapped leaves/tree supply its new identities.
		definition.sources = append([]viewLeaf(nil), definition.sources...)
		for i := range definition.sources {
			definition.sources[i].ref = ref(definition.sources[i].ref)
		}
		if len(d.view.literals) != 0 {
			definition.literals = make(map[viewLiteralPath]literalExpression, len(d.view.literals))
			for path, literal := range d.view.literals {
				node, _, err := retagBoundTypes(literal, typ)
				if err != nil {
					return err
				}
				node, err = remapBoundExpression(node, ref, nil)
				if err != nil {
					return err
				}
				definition.literals[path] = node.(literalExpression)
			}
		}
		if len(d.view.castUsages) != 0 {
			definition.castUsages = make(map[viewLiteralPath]sqltype.Usage, len(d.view.castUsages))
			for path, usage := range d.view.castUsages {
				usage.Type = typ(usage.Type)
				definition.castUsages[path] = usage
			}
		}
		definition.sequenceRefs = make(map[int]ObjectRef, len(d.view.sequenceRefs))
		for offset, old := range d.view.sequenceRefs {
			definition.sequenceRefs[offset] = ref(old)
		}
		definition.dependencies = append([]expressionDependency(nil), definition.dependencies...)
		for i := range definition.dependencies {
			definition.dependencies[i].ref = ref(definition.dependencies[i].ref)
		}
		root, err := remapViewQuery(definition.query.Root, ref, typ)
		if err != nil {
			return err
		}
		query := *definition.query
		query.Root = root
		definition.query = &query
		definition.frames, definition.queries = collectQueryFrames(root)
		copy := *d
		copy.view = &definition
		next.descriptors[address] = &copy
	}
	return nil
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
