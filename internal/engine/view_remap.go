package engine

import (
	"fmt"
	"sort"
	"strconv"
	"stuntgres/internal/sqltype"
)

// Replacement copies the closed bound tree; source and expression identities
// are reminted together. Syntax and frozen layouts are immutable and shared.
func remapBoundExpression(node BoundExpr, ref func(ObjectRef) ObjectRef, slots map[int32]int) (BoundExpr, error) {
	if node == nil {
		return nil, nil
	}
	var err error
	child := func(old BoundExpr) BoundExpr {
		if err != nil {
			return old
		}
		var next BoundExpr
		next, err = remapBoundExpression(old, ref, slots)
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
	case identityExpression:
		next = n
	case literalExpression:
		if n.usage.Type.ID() == sqltype.Regclass && !n.value.IsNull() {
			text, failure := n.value.Text()
			if failure != nil {
				return nil, failure
			}
			oid, failure := strconv.ParseUint(text, 10, 32)
			if failure != nil {
				return nil, failure
			}
			target := ref(ObjectRef{Address: ObjectAddress{ClassID: RelationClassID, ObjectID: OID(oid)}})
			n.value = sqltype.String(n.usage.Type, strconv.FormatUint(uint64(target.Address.ObjectID), 10))
		}
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
	case cachedOrderExpression:
		next = n // The cache slot belongs to this immutable query plan.
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
	case mathExpression:
		n.args = args(n.args)
		next = n
	case currentSchemasExpression:
		n.arg = child(n.arg)
		next = n
	case deparseExpression:
		n.args = args(n.args)
		next = n
	case commentInquiryExpression:
		n.args = args(n.args)
		next = n
	case nullCountExpression:
		n.args = args(n.args)
		next = n
	case randomExpression:
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
		next, err = remapViewQuery(old, ref, typ)
		return next
	}
	expression := func(old BoundExpr) BoundExpr {
		if err != nil {
			return old
		}
		next, _, failure := retagBoundTypes(old, typ)
		if failure != nil {
			err = failure
			return old
		}
		next, err = remapBoundExpression(next, ref, nil)
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
	case *limitNode:
		next = &limitNode{querySchema: schema, child: child(n.child), offset: expression(n.offset), count: expression(n.count)}
	case *evaluationNode:
		next = &evaluationNode{querySchema: schema, child: child(n.child), expressions: expressions(n.expressions), base: n.base}
	case *rowLockNode:
		next = &rowLockNode{querySchema: schema, child: child(n.child), mode: n.mode}
	case *sourceNode:
		next = &sourceNode{querySchema: schema, ref: ref(n.ref), occurrence: n.occurrence}
	case *joinNode:
		next = &joinNode{querySchema: schema, left: child(n.left), right: child(n.right), predicate: expression(n.predicate), leftWidth: n.leftWidth, rightWidth: n.rightWidth, merges: expressions(n.merges)}
	case *singletonNode:
		next = &singletonNode{querySchema: schema}
	case *projectNode:
		next = &projectNode{querySchema: schema, child: child(n.child), expressions: expressions(n.expressions)}
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
			if err == nil {
				copy.keys[i].comparison, err = sqltype.BindOrdering(copy.keys[i].expression.ResultUsage())
			}
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
			default:
				return nil, fmt.Errorf("stuntgres: unpersistable view aggregate")
			}
			if call.argument != nil {
				copy.calls[i].argument = expression(call.argument)
			}
		}
		next = &copy
	default:
		return nil, fmt.Errorf("stuntgres: unpersistable view query %T", node)
	}
	return next, err
}

func (e *Engine) remapCatalogExpressions(next *catalogState, mapping map[ObjectAddress]ObjectAddress) error {
	ref := func(old ObjectRef) ObjectRef {
		if old == (ObjectRef{}) {
			return old
		}
		address := old.Address
		address.SubID = 0
		if target, ok := mapping[address]; ok {
			target.SubID = old.Address.SubID
			old.Address = target
		}
		old.owner = e.owner
		old.Epoch = e.state.epoch + 1
		return old
	}
	typ := func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == nil {
			return nil
		}
		if d, ok := next.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(old.ID())}].(*typeDescriptor); ok {
			return d.typ
		}
		return old
	}
	for address, descriptor := range next.descriptors {
		d, ok := descriptor.(*relationDescriptor)
		if !ok {
			continue
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
