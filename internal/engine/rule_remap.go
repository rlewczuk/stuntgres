package engine

import (
	"fmt"
	"maps"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

func remapRuleBinding(view catalog.Read, b binding, ref func(ids.ObjectRef) ids.ObjectRef, typ func(*sqltype.Descriptor) *sqltype.Descriptor, collation typedef.CollationRemapper) (binding, error) {
	typ = recordTypeMapper(typ, collation)
	mapper := &viewQueryRemapper{catalog: view, ref: ref, typ: typ, collation: collation}
	mapper.mutation = func(definition *cteDefinition) (binding, error) {
		return remapRuleBindingMapped(view, *definition.mutation, ref, typ, collation, mapper)
	}
	return remapRuleBindingMapped(view, b, ref, typ, collation, mapper)
}

func remapRuleBindingMapped(view catalog.Read, b binding, ref func(ids.ObjectRef) ids.ObjectRef, typ func(*sqltype.Descriptor) *sqltype.Descriptor, collation typedef.CollationRemapper, mapper *viewQueryRemapper) (binding, error) {
	originalRef := b.ref
	mapExpr := func(expr expr.Expr) (expr.Expr, error) {
		if expr == nil {
			return nil, nil
		}
		node, _, err := retagBoundTypesWithQuery(view, expr, typ, nil, collation)
		if err != nil {
			return nil, err
		}
		return remapBoundExpressionWithQuery(node, ref, nil, mapper.nested, nil)
	}
	mapTargetExpr := func(expr expr.Expr) (expr.Expr, bool, error) {
		if expr == nil {
			return nil, false, nil
		}
		next, err := mapExpr(expr)
		return next, true, err // Import remints the complete scalar identity tree.
	}
	mapList := func(list []expr.Expr) ([]expr.Expr, error) {
		if len(list) == 0 {
			return list, nil
		}
		next := slices.Clone(list)
		for i, expr := range next {
			var err error
			next[i], err = mapExpr(expr)
			if err != nil {
				return nil, err
			}
		}
		return next, nil
	}
	mapQuery := func(root LogicalNode) (LogicalNode, error) {
		if root == nil {
			return nil, nil
		}
		if _, project := root.(*projectNode); project {
			return mapper.nested(root)
		}
		return mapper.query(root)
	}
	mapSources := func(sources []queryLeaf) ([]queryLeaf, error) {
		next := slices.Clone(sources)
		for i := range next {
			old := next[i].relation
			next[i].ref = ref(next[i].ref)
			next[i].relation, _ = view.Relation(next[i].ref.Address)
			next[i].storage = nil
			if old != nil && next[i].relation != nil && !slices.Equal(old.Attributes, next[i].relation.Attributes) {
				next[i].uses = slices.Clone(next[i].uses)
				for j, slot := range next[i].uses {
					if slot < 0 || slot >= len(old.Attributes) {
						return nil, ids.ErrCatalogReplaced
					}
					next[i].uses[j] = next[i].relation.IndexForAttribute(old.Attributes[slot])
					if next[i].uses[j] < 0 {
						return nil, ids.ErrCatalogReplaced
					}
				}
			}
		}
		return next, nil
	}
	mapUsage := func(usage sqltype.Usage) (sqltype.Usage, error) {
		usage.Type = typ(usage.Type)
		var err error
		if collation != nil {
			usage.Collation, err = collation(usage.Collation)
		}
		return usage, err
	}
	var mapUpdate func(*boundUpdate) (*boundUpdate, error)
	mapUpdate = func(update *boundUpdate) (*boundUpdate, error) {
		if update == nil {
			return nil, nil
		}
		next := *update
		var err error
		next.assignments, err = mapList(update.assignments)
		if err != nil {
			return nil, err
		}
		next.arrayTargets, err = mapRuleAssignmentTargets(update.arrayTargets, mapTargetExpr, mapUsage)
		if err != nil {
			return nil, err
		}
		next.predicate, err = mapExpr(update.predicate)
		if err != nil {
			return nil, err
		}
		next.from, err = mapQuery(update.from)
		if err != nil {
			return nil, err
		}
		return &next, nil
	}
	mapReturning := func(returning *boundReturning) (*boundReturning, error) {
		if returning == nil {
			return nil, nil
		}
		next := *returning
		var err error
		next.expressions, err = mapList(returning.expressions)
		if err != nil {
			return nil, err
		}
		next.schema = slices.Clone(returning.schema)
		for i := range next.schema {
			next.schema[i].Usage.Type = typ(next.schema[i].Usage.Type)
			if collation != nil {
				next.schema[i].Usage.Collation, err = collation(next.schema[i].Usage.Collation)
				if err != nil {
					return nil, err
				}
			}
		}
		return &next, nil
	}
	b.ref = ref(b.ref)
	var current *catalog.Relation
	if b.relation != nil {
		current, _ = view.Relation(b.ref.Address)
		if current == nil {
			return b, ids.ErrCatalogReplaced
		}
		b.storage = nil
	}
	var err error
	b.querySources, err = mapSources(b.querySources)
	if err != nil {
		return b, err
	}
	b.arrayTargets, err = mapRuleAssignmentTargets(b.arrayTargets, mapTargetExpr, mapUsage)
	if err != nil {
		return b, err
	}
	if b.query != nil {
		next := *b.query
		next.Root, err = mapQuery(b.query.Root)
		if err != nil {
			return b, err
		}
		b.query = &next
	}
	b.expressions = slices.Clone(b.expressions)
	for i, row := range b.expressions {
		b.expressions[i], err = mapList(row)
		if err != nil {
			return b, err
		}
	}
	b.insert.defaults, err = mapList(b.insert.defaults)
	if err != nil {
		return b, err
	}
	b.predicate, err = mapExpr(b.predicate)
	if err != nil {
		return b, err
	}
	b.delete.using, err = mapQuery(b.delete.using)
	if err != nil {
		return b, err
	}
	b.update, err = mapUpdate(b.update)
	if err != nil {
		return b, err
	}
	b.returning, err = mapReturning(b.returning)
	if err != nil {
		return b, err
	}
	b.dependencies = slices.Clone(b.dependencies)
	for i := range b.dependencies {
		b.dependencies[i].Ref = ref(b.dependencies[i].Ref)
	}
	b.prepared.sequenceConstants = slices.Clone(b.prepared.sequenceConstants)
	for i := range b.prepared.sequenceConstants {
		b.prepared.sequenceConstants[i].ref = ref(b.prepared.sequenceConstants[i].ref)
	}
	if b.insert.conflict != nil {
		next := *b.insert.conflict.instantiate()
		if next.constraint != (ids.ObjectAddress{}) {
			constraintRef := originalRef
			constraintRef.Address = next.constraint
			next.constraint = ref(constraintRef).Address
		}
		if next.namedIndex != (ids.ObjectAddress{}) {
			indexRef := originalRef
			indexRef.Address = next.namedIndex
			next.namedIndex = ref(indexRef).Address
		}
		next.keys = slices.Clone(next.keys)
		for i := range next.keys {
			next.keys[i].expression, err = mapExpr(next.keys[i].expression)
			if err != nil {
				return b, err
			}
			if collation != nil && next.keys[i].collation != 0 {
				mapped, err := collation(sqltype.CollationRef{ID: next.keys[i].collation})
				if err != nil {
					return b, err
				}
				next.keys[i].collation = mapped.ID
			}
		}
		next.predicate, err = mapExpr(next.predicate)
		if err != nil {
			return b, err
		}
		next.update, err = mapUpdate(next.update)
		if err != nil {
			return b, err
		}
		next.returning, err = mapReturning(next.returning)
		if err != nil {
			return b, err
		}
		next.sources, err = mapSources(next.sources)
		if err != nil {
			return b, err
		}
		b.insert.conflict = &next
	}
	b.ctes = slices.Clone(b.ctes)
	for i, definition := range b.ctes {
		mapped, err := mapper.cte(definition)
		if err != nil {
			return b, err
		}
		b.ctes[i] = mapped
	}
	b.rule.ctes = nil
	if current != nil {
		b, err = refreshRuleBinding(view, b, current)
		if err != nil {
			return b, err
		}
	}
	return b, nil
}

func remapRules(view catalog.Read, d *catalog.Relation, mapping map[ids.ObjectAddress]ids.ObjectAddress, ref func(ids.ObjectRef) ids.ObjectRef, typ func(*sqltype.Descriptor) *sqltype.Descriptor, collation typedef.CollationRemapper) (*catalog.Relation, error) {
	if ns, ok := view.Namespace(d.Namespace()); ok && ns.Temporary != (ids.SessionToken{}) {
		mapping = nil
		ref = func(old ids.ObjectRef) ids.ObjectRef {
			if old == (ids.ObjectRef{}) {
				return old
			}
			old = view.Owner().Domain.Ref(old.Epoch, old.Address)
			old.Epoch = view.Epoch()
			return old
		}
		typ = func(old *sqltype.Descriptor) *sqltype.Descriptor {
			if old == nil {
				return nil
			}
			if d, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(old.ID())}); ok {
				return d.SQLType
			}
			return old
		}
	}
	copy := *d
	copy.Rules = rulesPayloadOf(slices.Clone(rulesOf(d)))
	for i := range rulesOf(&copy) {
		r := &rulesOf(&copy)[i]
		var err error
		if r.condition != nil {
			r.condition, _, err = retagBoundTypes(view, r.condition, typ, collation)
			if err != nil {
				return nil, err
			}
			r.condition, err = remapBoundExpression(r.condition, ref, nil)
			if err != nil {
				return nil, err
			}
		}
		if r.conditionPresentation != nil {
			r.conditionPresentation, err = remapFrozenPresentation(view, r.conditionPresentation, mapping, ref, typ, collation)
			if err != nil {
				return nil, err
			}
		}
		if r.conditionBinding != nil {
			next, err := remapRuleBinding(view, *r.conditionBinding, ref, typ, collation)
			if err != nil {
				return nil, err
			}
			r.conditionBinding = &next
		}
		r.actions = slices.Clone(r.actions)
		for j := range r.actions {
			action := &r.actions[j]
			action.binding, err = remapRuleBinding(view, action.binding, ref, typ, collation)
			if err != nil {
				return nil, err
			}
			if action.presentation != nil {
				action.presentation, err = remapFrozenPresentation(view, action.presentation, mapping, ref, typ, collation)
				if err != nil {
					return nil, err
				}
			}
			action.modifierPresentations = maps.Clone(action.modifierPresentations)
			for statement, presentation := range action.modifierPresentations {
				mapped, err := remapFrozenPresentation(view, presentation, mapping, ref, typ, collation)
				if err != nil {
					return nil, err
				}
				action.modifierPresentations[statement] = mapped
			}
		}
		r.references = slices.Clone(r.references)
		for j, a := range r.references {
			r.references[j] = ref(ids.ObjectRef{Address: a}).Address
		}
	}
	return &copy, nil
}

// Definition OID reminting follows descriptor/expression import. Only named
// constraint identities change here; canonical recipe mapping keeps modifying
// WITH definitions and their scans coherent without remapping scalar types again.
func remapRuleConstraintIdentities(view catalog.Read, d *catalog.Relation, mapping map[ids.ObjectAddress]ids.ObjectAddress) (*catalog.Relation, error) {
	var replacement *catalog.Relation
	for i, r := range rulesOf(d) {
		var references []ids.ObjectAddress
		for j, address := range r.references {
			if next, ok := mapping[address]; ok && next != address {
				if references == nil {
					references = slices.Clone(r.references)
				}
				references[j] = next
			}
		}
		if references == nil {
			continue
		}
		if replacement == nil {
			copy := *d
			copy.Rules = rulesPayloadOf(slices.Clone(rulesOf(d)))
			replacement = &copy
		}
		r.references = references
		r.actions = slices.Clone(r.actions)
		conflict := func(b binding) binding {
			if b.insert.conflict != nil {
				if address, ok := mapping[b.insert.conflict.constraint]; ok && address != b.insert.conflict.constraint {
					copy := *b.insert.conflict
					copy.constraint = address
					b.insert.conflict = &copy
				}
			}
			return b
		}
		for j := range r.actions {
			if len(r.actions[j].binding.ctes) == 0 {
				r.actions[j].binding = conflict(r.actions[j].binding)
				continue
			}
			mapper := newRuleTreeMapper(view, nil, 0, nil, ruleTreeMapping{
				mutation: func(definition *cteDefinition) (binding, error) {
					return conflict(*definition.mutation), nil
				},
			})
			next, err := mapRuleBindingRecipes(conflict(r.actions[j].binding), mapper)
			if err != nil {
				return nil, err
			}
			r.actions[j].binding = next
		}
		rulesOf(replacement)[i] = r
	}
	if replacement == nil {
		return d, nil
	}
	return replacement, nil
}

func (e *Engine) checkTemporaryRuleSourcesReplacementLocked() error {
	view := e.catalog.read(e.catalog.state)
	for _, descriptor := range e.catalog.state.Descriptors() {
		d, ok := descriptor.(*catalog.Relation)
		if !ok || len(rulesOf(d)) == 0 {
			continue
		}
		ns, ok := e.catalog.state.Namespace(d.Namespace())
		if !ok || ns.Temporary == (ids.SessionToken{}) {
			continue
		}
		for _, rule := range rulesOf(d) {
			for _, address := range rule.references {
				address.SubID = 0
				if pgDependPinned(view, nil, address) {
					continue
				}
				provider := e.catalog.state.Get(address)
				if address.ClassID == 2606 {
					parent := constraintParent(view, address)
					if parent == nil {
						return ids.ErrCatalogReplaced
					}
					provider = parent
				}
				if provider == nil {
					return ids.ErrCatalogReplaced
				}
				providerNS, ok := e.catalog.state.Namespace(provider.Namespace())
				if !ok || providerNS.Temporary != ns.Temporary {
					return fmt.Errorf("stuntgres: catalog replacement cannot preserve temporary rule %q dependencies", rule.Name)
				}
			}
		}
	}
	return nil
}
