package engine

import (
	"context"
	"iter"
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func ruleReferences(ctx context.Context, catalog CatalogRead, r ruleDefinition) ([]ids.ObjectAddress, error) {
	var refs []ids.ObjectAddress
	add := func(items []ids.ObjectAddress) {
		for _, a := range items {
			if !slices.Contains(refs, a) {
				refs = append(refs, a)
			}
		}
	}
	expr := func(e BoundExpr) error {
		if e == nil {
			return nil
		}
		items, err := pgDependExpressionRefs(ctx, catalog, e)
		add(items)
		return err
	}
	targets := func(items []*arrayAssignmentTarget) error {
		for _, target := range items {
			for ; target != nil; target = target.next {
				for _, sub := range target.node.subscripts {
					if err := expr(sub.lower); err != nil {
						return err
					}
					if err := expr(sub.upper); err != nil {
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
	var seen map[*cteDefinition]bool
	var bindingRefs func(binding) error
	bindingRefs = func(b binding) error {
		if err := targets(b.arrayTargets); err != nil {
			return err
		}
		if b.relation != nil {
			for _, slot := range b.targets {
				a := b.ref.Address
				a.SubID = b.relation.attributes[slot]
				add([]ids.ObjectAddress{a})
			}
			if len(b.targets) == 0 {
				add([]ids.ObjectAddress{b.ref.Address})
			}
		}
		items, err := pgDependQueryRefs(ctx, catalog, b.query)
		if err != nil {
			return err
		}
		add(items)
		for _, row := range b.expressions {
			for _, e := range row {
				if err := expr(e); err != nil {
					return err
				}
			}
		}
		if err := expr(b.predicate); err != nil {
			return err
		}
		if b.update != nil {
			if err := targets(b.update.arrayTargets); err != nil {
				return err
			}
			for _, e := range b.update.assignments {
				if err := expr(e); err != nil {
					return err
				}
			}
			if err := expr(b.update.predicate); err != nil {
				return err
			}
			items, err := pgDependQueryRefs(ctx, catalog, &BoundQuery{Root: b.update.from})
			if err != nil {
				return err
			}
			add(items)
		}
		if b.deleteUsing != nil {
			items, err := pgDependQueryRefs(ctx, catalog, &BoundQuery{Root: b.deleteUsing})
			if err != nil {
				return err
			}
			add(items)
		}
		if b.returning != nil {
			for _, e := range b.returning.expressions {
				if err := expr(e); err != nil {
					return err
				}
			}
		}
		if b.conflict != nil {
			if b.conflict.constraint != (ids.ObjectAddress{}) {
				add([]ids.ObjectAddress{b.conflict.constraint})
			}
			for _, key := range b.conflict.keys {
				if err := expr(key.expression); err != nil {
					return err
				}
			}
			if err := expr(b.conflict.predicate); err != nil {
				return err
			}
			if b.conflict.update != nil {
				// PostgreSQL's EXCLUDED targetlist captures every live attribute
				// at definition time, independently of executable ACL read demand.
				for _, attribute := range b.relation.attributes {
					a := b.ref.Address
					a.SubID = attribute
					add([]ids.ObjectAddress{a})
				}
				if err := targets(b.conflict.update.arrayTargets); err != nil {
					return err
				}
				for i, value := range b.conflict.update.assignments {
					if err := expr(value); err != nil {
						return err
					}
					a := b.ref.Address
					a.SubID = b.relation.attributes[b.conflict.updateTargets[i]]
					add([]ids.ObjectAddress{a})
				}
				if err := expr(b.conflict.update.predicate); err != nil {
					return err
				}
			}
		}
		for _, dependency := range b.dependencies {
			add([]ids.ObjectAddress{dependency.ref.Address})
		}
		for _, definition := range b.ctes {
			if definition.mutation == nil || seen[definition] {
				continue
			}
			if seen == nil {
				seen = make(map[*cteDefinition]bool)
			}
			seen[definition] = true
			if err := bindingRefs(*definition.mutation); err != nil {
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

func ruleDepends(catalog CatalogRead, r ruleDefinition, target ids.ObjectAddress) bool {
	for _, a := range r.references {
		if a.ClassID == target.ClassID && a.ObjectID == target.ObjectID && (target.SubID == 0 || a.SubID == target.SubID) {
			return true
		}
	}
	return false
}

func ruleDependentLabels(catalog CatalogRead, targets []ids.ObjectAddress, path []ids.ObjectAddress) []supportedDependent {
	return ruleDependentLabelsFrom(catalog, targets, path, catalog.Descriptors(), nil)
}

func (provider *dropProvider) ruleDependentLabels(targets []ids.ObjectAddress) []supportedDependent {
	if provider.prepared == nil {
		return ruleDependentLabels(provider.base, targets, provider.path)
	}
	return ruleDependentLabelsFrom(provider.base, targets, provider.path, provider.liveDescriptors(), provider.observe)
}

func ruleDependentLabelsFrom(catalog CatalogRead, targets []ids.ObjectAddress, path []ids.ObjectAddress, descriptors iter.Seq2[ids.ObjectAddress, Descriptor], observe func(ids.ObjectAddress)) []supportedDependent {
	var dependents []supportedDependent
	for _, descriptor := range descriptors {
		d, ok := descriptor.(*relationDescriptor)
		if !ok || slices.Contains(targets, d.address) {
			continue
		}
		for _, r := range d.rules {
			for _, target := range targets {
				if ruleDepends(catalog, r, target) {
					if observe != nil {
						observe(d.address)
					}
					dependents = append(dependents, supportedDependent{oid: r.OID, target: target, label: "rule " + dependencyIdentifier(r.Name) + " on " + relationDiagnosticKind(d) + " " + dependencyRelationName(catalog, d, path)})
					break
				}
			}
		}
	}
	return dependents
}

func stageRuleDependents(stage *CatalogStage, targets []ids.ObjectAddress, mode parser.DropMode) error {
	return stageDropProvider(context.Background(), stage, nil, nil, nil).ruleDependents(targets, mode)
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
		d, ok := descriptor.(*relationDescriptor)
		if !ok || len(d.rules) == 0 {
			continue
		}
		var kept []ruleDefinition
		for _, r := range d.rules {
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
		if len(kept) == len(d.rules) {
			continue
		}
		copy := *d
		copy.rules = kept
		revision, err := provider.revision(copy.revision)
		if err != nil {
			return err
		}
		copy.revision = revision
		if err := provider.replace(&copy); err != nil {
			return err
		}
	}
	return nil
}
