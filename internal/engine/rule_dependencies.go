package engine

import (
	"context"
	"slices"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

func ruleReferences(ctx context.Context, catalog catalog.Read, r ruleDefinition) ([]ids.ObjectAddress, error) {
	var refs []ids.ObjectAddress
	add := func(items []ids.ObjectAddress) {
		for _, a := range items {
			if !slices.Contains(refs, a) {
				refs = append(refs, a)
			}
		}
	}
	expr := func(e expr.Expr) error {
		if e == nil {
			return nil
		}
		items, err := ExpressionDependRefs(ctx, catalog, e)
		add(items)
		return err
	}
	targets := func(items []*plan.ArrayAssignmentTarget) error {
		for _, target := range items {
			for ; target != nil; target = target.Next {
				for _, sub := range target.Node.Subscripts {
					if err := expr(sub.Lower); err != nil {
						return err
					}
					if err := expr(sub.Upper); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	if err := expr(r.condition); err != nil {
		return nil, err
	}
	var seen map[*plan.CTEDefinition]bool
	var bindingRefs func(analyze.Binding) error
	bindingRefs = func(b analyze.Binding) error {
		if err := targets(b.ArrayTargets); err != nil {
			return err
		}
		if b.Relation != nil {
			for _, slot := range b.Targets {
				a := b.Ref.Address
				a.SubID = b.Relation.Attributes[slot]
				add([]ids.ObjectAddress{a})
			}
			if len(b.Targets) == 0 {
				add([]ids.ObjectAddress{b.Ref.Address})
			}
		}
		items, err := QueryDependRefs(ctx, catalog, nil, b.Query)
		if err != nil {
			return err
		}
		add(items)
		for _, row := range b.Expressions {
			for _, e := range row {
				if err := expr(e); err != nil {
					return err
				}
			}
		}
		if err := expr(b.Predicate); err != nil {
			return err
		}
		if b.Update != nil {
			if err := targets(b.Update.ArrayTargets); err != nil {
				return err
			}
			for _, e := range b.Update.Assignments {
				if err := expr(e); err != nil {
					return err
				}
			}
			if err := expr(b.Update.Predicate); err != nil {
				return err
			}
			items, err := QueryDependRefs(ctx, catalog, nil, &plan.Query{Root: b.Update.From})
			if err != nil {
				return err
			}
			add(items)
		}
		if b.Delete.Using != nil {
			items, err := QueryDependRefs(ctx, catalog, nil, &plan.Query{Root: b.Delete.Using})
			if err != nil {
				return err
			}
			add(items)
		}
		if b.Returning != nil {
			for _, e := range b.Returning.Expressions {
				if err := expr(e); err != nil {
					return err
				}
			}
		}
		if b.Insert.Conflict != nil {
			if b.Insert.Conflict.Constraint != (ids.ObjectAddress{}) {
				add([]ids.ObjectAddress{b.Insert.Conflict.Constraint})
			}
			for _, key := range b.Insert.Conflict.Keys {
				if err := expr(key.Expression); err != nil {
					return err
				}
			}
			if err := expr(b.Insert.Conflict.Predicate); err != nil {
				return err
			}
			if b.Insert.Conflict.Update != nil {
				// PostgreSQL's EXCLUDED targetlist captures every live attribute
				// at definition time, independently of executable ACL read demand.
				for _, attribute := range b.Relation.Attributes {
					a := b.Ref.Address
					a.SubID = attribute
					add([]ids.ObjectAddress{a})
				}
				if err := targets(b.Insert.Conflict.Update.ArrayTargets); err != nil {
					return err
				}
				for i, value := range b.Insert.Conflict.Update.Assignments {
					if err := expr(value); err != nil {
						return err
					}
					a := b.Ref.Address
					a.SubID = b.Relation.Attributes[b.Insert.Conflict.UpdateTargets[i]]
					add([]ids.ObjectAddress{a})
				}
				if err := expr(b.Insert.Conflict.Update.Predicate); err != nil {
					return err
				}
			}
		}
		for _, dependency := range b.Dependencies {
			add([]ids.ObjectAddress{dependency.Ref.Address})
		}
		for _, definition := range b.CTEs {
			mutation, ok := analyze.MutationBinding(definition)
			if !ok || seen[definition] {
				continue
			}
			if seen == nil {
				seen = make(map[*plan.CTEDefinition]bool)
			}
			seen[definition] = true
			if err := bindingRefs(*mutation); err != nil {
				return err
			}
		}
		return nil
	}
	for _, action := range r.actions {
		if err := bindingRefs(action.binding); err != nil {
			return nil, err
		}
	}
	return refs, nil
}

func ruleDepends(catalog catalog.Read, r ruleDefinition, target ids.ObjectAddress) bool {
	for _, a := range r.references {
		if a.ClassID == target.ClassID && a.ObjectID == target.ObjectID && (target.SubID == 0 || a.SubID == target.SubID) {
			return true
		}
	}
	return false
}

func (provider *dropProvider) ruleDependentLabels(targets []ids.ObjectAddress) []supportedDependent {
	if provider.prepared == nil {
		return ruleDependentLabels(provider.base, targets, provider.path)
	}
	return ruleDependentLabelsFrom(provider.base, targets, provider.path, provider.liveDescriptors(), provider.observe)
}

func (provider *dropProvider) ruleDependents(targets []ids.ObjectAddress, mode parser.DropMode) error {
	if mode == parser.DropRestrict {
		return nil
	}
	for address, descriptor := range provider.inputDescriptors() {
		if provider.removed(address) || slices.Contains(targets, address) {
			continue
		}
		if replacement := provider.replacement(address); replacement != nil {
			descriptor = replacement
		}
		d, ok := descriptor.(*catalog.Relation)
		if !ok || len(rulesOf(d)) == 0 {
			continue
		}
		var kept []ruleDefinition
		for _, r := range rulesOf(d) {
			drop := false
			for _, target := range targets {
				if ruleDepends(provider.base, r, target) {
					drop = true
					break
				}
			}
			if !drop {
				kept = append(kept, r)
			}
		}
		if len(kept) == len(rulesOf(d)) {
			continue
		}
		copy := *d
		copy.Rules = rulesPayloadOf(kept)
		revision, err := provider.revision(copy.Revision())
		if err != nil {
			return err
		}
		copy.ObjectRevision = revision
		if err := provider.replace(&copy); err != nil {
			return err
		}
	}
	return nil
}
