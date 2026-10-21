package engine

import (
	"maps"
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

func remapRuleBinding(view catalog.Read, b analyze.Binding, ref func(ids.ObjectRef) ids.ObjectRef, typ func(*sqltype.Descriptor) *sqltype.Descriptor, collation typedef.CollationRemapper) (analyze.Binding, error) {
	typ = analyze.RecordTypeMapper(typ, collation)
	mapper := &viewQueryRemapper{Catalog: view, Ref: ref, Type: typ, Collation: collation}
	mapper.Mutation = func(definition *plan.CTEDefinition) (analyze.Binding, error) {
		mutation, _ := analyze.MutationBinding(definition)
		return remapRuleBindingMapped(view, *mutation, ref, typ, collation, mapper)
	}
	return remapRuleBindingMapped(view, b, ref, typ, collation, mapper)
}

func remapRuleBindingMapped(view catalog.Read, b analyze.Binding, ref func(ids.ObjectRef) ids.ObjectRef, typ func(*sqltype.Descriptor) *sqltype.Descriptor, collation typedef.CollationRemapper, mapper *viewQueryRemapper) (analyze.Binding, error) {
	originalRef := b.Ref
	mapExpr := func(expr expr.Expr) (expr.Expr, error) {
		if expr == nil {
			return nil, nil
		}
		node, _, err := stored.RetagTypesWithQuery(view, expr, typ, nil, collation)
		if err != nil {
			return nil, err
		}
		return stored.RemapExpressionWithQuery(node, ref, nil, mapper.Nested, nil)
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
	mapQuery := func(root plan.Node) (plan.Node, error) {
		if root == nil {
			return nil, nil
		}
		if _, project := root.(*plan.ProjectNode); project {
			return mapper.Nested(root)
		}
		return mapper.Query(root)
	}
	mapSources := func(sources []exec.Leaf) ([]exec.Leaf, error) {
		next := slices.Clone(sources)
		for i := range next {
			old := next[i].Relation
			next[i].Ref = ref(next[i].Ref)
			next[i].Relation, _ = view.Relation(next[i].Ref.Address)
			next[i].Storage = nil
			if old != nil && next[i].Relation != nil && !slices.Equal(old.Attributes, next[i].Relation.Attributes) {
				next[i].Uses = slices.Clone(next[i].Uses)
				for j, slot := range next[i].Uses {
					if slot < 0 || slot >= len(old.Attributes) {
						return nil, ids.ErrCatalogReplaced
					}
					next[i].Uses[j] = next[i].Relation.IndexForAttribute(old.Attributes[slot])
					if next[i].Uses[j] < 0 {
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
	var mapUpdate func(*analyze.UpdateBinding) (*analyze.UpdateBinding, error)
	mapUpdate = func(update *analyze.UpdateBinding) (*analyze.UpdateBinding, error) {
		if update == nil {
			return nil, nil
		}
		next := *update
		var err error
		next.Assignments, err = mapList(update.Assignments)
		if err != nil {
			return nil, err
		}
		next.ArrayTargets, err = stored.MapRuleAssignmentTargets(update.ArrayTargets, mapTargetExpr, mapUsage)
		if err != nil {
			return nil, err
		}
		next.Predicate, err = mapExpr(update.Predicate)
		if err != nil {
			return nil, err
		}
		next.From, err = mapQuery(update.From)
		if err != nil {
			return nil, err
		}
		return &next, nil
	}
	mapReturning := func(returning *analyze.Returning) (*analyze.Returning, error) {
		if returning == nil {
			return nil, nil
		}
		next := *returning
		var err error
		next.Expressions, err = mapList(returning.Expressions)
		if err != nil {
			return nil, err
		}
		next.Schema = slices.Clone(returning.Schema)
		for i := range next.Schema {
			next.Schema[i].Usage.Type = typ(next.Schema[i].Usage.Type)
			if collation != nil {
				next.Schema[i].Usage.Collation, err = collation(next.Schema[i].Usage.Collation)
				if err != nil {
					return nil, err
				}
			}
		}
		return &next, nil
	}
	b.Ref = ref(b.Ref)
	var current *catalog.Relation
	if b.Relation != nil {
		current, _ = view.Relation(b.Ref.Address)
		if current == nil {
			return b, ids.ErrCatalogReplaced
		}
		b.Storage = nil
	}
	var err error
	b.QuerySources, err = mapSources(b.QuerySources)
	if err != nil {
		return b, err
	}
	b.ArrayTargets, err = stored.MapRuleAssignmentTargets(b.ArrayTargets, mapTargetExpr, mapUsage)
	if err != nil {
		return b, err
	}
	if b.Query != nil {
		next := *b.Query
		next.Root, err = mapQuery(b.Query.Root)
		if err != nil {
			return b, err
		}
		b.Query = &next
	}
	b.Expressions = slices.Clone(b.Expressions)
	for i, row := range b.Expressions {
		b.Expressions[i], err = mapList(row)
		if err != nil {
			return b, err
		}
	}
	b.Insert.Defaults, err = mapList(b.Insert.Defaults)
	if err != nil {
		return b, err
	}
	b.Predicate, err = mapExpr(b.Predicate)
	if err != nil {
		return b, err
	}
	b.Delete.Using, err = mapQuery(b.Delete.Using)
	if err != nil {
		return b, err
	}
	b.Update, err = mapUpdate(b.Update)
	if err != nil {
		return b, err
	}
	b.Returning, err = mapReturning(b.Returning)
	if err != nil {
		return b, err
	}
	b.Dependencies = slices.Clone(b.Dependencies)
	for i := range b.Dependencies {
		b.Dependencies[i].Ref = ref(b.Dependencies[i].Ref)
	}
	b.Prepared.SequenceConstants = slices.Clone(b.Prepared.SequenceConstants)
	for i := range b.Prepared.SequenceConstants {
		b.Prepared.SequenceConstants[i].Ref = ref(b.Prepared.SequenceConstants[i].Ref)
	}
	if b.Insert.Conflict != nil {
		next := *b.Insert.Conflict.Clone()
		if next.Constraint != (ids.ObjectAddress{}) {
			constraintRef := originalRef
			constraintRef.Address = next.Constraint
			next.Constraint = ref(constraintRef).Address
		}
		if next.NamedIndex != (ids.ObjectAddress{}) {
			indexRef := originalRef
			indexRef.Address = next.NamedIndex
			next.NamedIndex = ref(indexRef).Address
		}
		next.Keys = slices.Clone(next.Keys)
		for i := range next.Keys {
			next.Keys[i].Expression, err = mapExpr(next.Keys[i].Expression)
			if err != nil {
				return b, err
			}
			if collation != nil && next.Keys[i].Collation != 0 {
				mapped, err := collation(sqltype.CollationRef{ID: next.Keys[i].Collation})
				if err != nil {
					return b, err
				}
				next.Keys[i].Collation = mapped.ID
			}
		}
		next.Predicate, err = mapExpr(next.Predicate)
		if err != nil {
			return b, err
		}
		next.Update, err = mapUpdate(next.Update)
		if err != nil {
			return b, err
		}
		next.Returning, err = mapReturning(next.Returning)
		if err != nil {
			return b, err
		}
		next.Sources, err = mapSources(next.Sources)
		if err != nil {
			return b, err
		}
		b.Insert.Conflict = &next
	}
	b.CTEs = slices.Clone(b.CTEs)
	for i, definition := range b.CTEs {
		mapped, err := mapper.CTE(definition)
		if err != nil {
			return b, err
		}
		b.CTEs[i] = mapped
	}
	b.Rule.CTEs = nil
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
			r.condition, _, err = stored.RetagTypes(view, r.condition, typ, collation)
			if err != nil {
				return nil, err
			}
			r.condition, err = stored.RemapExpression(r.condition, ref, nil)
			if err != nil {
				return nil, err
			}
		}
		if r.conditionPresentation != nil {
			r.conditionPresentation, err = stored.RemapView(view, r.conditionPresentation, mapping, ref, typ, collation)
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
				action.presentation, err = stored.RemapView(view, action.presentation, mapping, ref, typ, collation)
				if err != nil {
					return nil, err
				}
			}
			action.modifierPresentations = maps.Clone(action.modifierPresentations)
			for statement, presentation := range action.modifierPresentations {
				mapped, err := stored.RemapView(view, presentation, mapping, ref, typ, collation)
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
		conflict := func(b analyze.Binding) analyze.Binding {
			if b.Insert.Conflict != nil {
				if address, ok := mapping[b.Insert.Conflict.Constraint]; ok && address != b.Insert.Conflict.Constraint {
					copy := *b.Insert.Conflict
					copy.Constraint = address
					b.Insert.Conflict = &copy
				}
			}
			return b
		}
		for j := range r.actions {
			if len(r.actions[j].binding.CTEs) == 0 {
				r.actions[j].binding = conflict(r.actions[j].binding)
				continue
			}
			mapper := newRuleTreeMapper(view, nil, 0, nil, ruleTreeMapping{
				mutation: func(definition *plan.CTEDefinition) (analyze.Binding, error) {
					mutation, _ := analyze.MutationBinding(definition)
					return conflict(*mutation), nil
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
