package engine

import (
	"fmt"
	"slices"
	"sort"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func viewDepends(d *catalog.Relation, target ids.ObjectAddress) bool {
	return d.View != nil && d.View.DependsOn(target)
}

type viewDependencyEdge struct {
	view   *catalog.Relation
	target ids.ObjectAddress
}

func viewDependencyClosure(catalog catalog.Read, targets []ids.ObjectAddress, stage *CatalogStage) []viewDependencyEdge {
	provider := &dropProvider{base: catalog, stage: stage}
	return provider.viewClosure(targets)
}

func (provider *dropProvider) viewClosure(targets []ids.ObjectAddress) []viewDependencyEdge {
	var views []*catalog.Relation
	for address, descriptor := range provider.descriptors() {
		if provider.removed(address) {
			continue
		}
		if replacement := provider.replacement(address); replacement != nil {
			descriptor = replacement
		}
		if d, ok := descriptor.(*catalog.Relation); ok && d.View != nil {
			views = append(views, d)
		}
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Address().ObjectID < views[j].Address().ObjectID })
	seen := make(map[ids.ObjectAddress]bool, len(targets))
	for _, target := range targets {
		if target.SubID == 0 {
			seen[target] = true
		}
	}
	var edges []viewDependencyEdge
	var visit func(ids.ObjectAddress)
	visit = func(target ids.ObjectAddress) {
		for _, view := range views {
			if !seen[view.Address()] && viewDepends(view, target) {
				seen[view.Address()] = true
				edges = append(edges, viewDependencyEdge{view, target})
				visit(view.Address())
			}
		}
	}
	for _, target := range targets {
		visit(target)
	}
	return edges
}

func (provider *dropProvider) viewDependents(targets []ids.ObjectAddress, mode parser.DropMode, foreign ...domainDependency) error {
	edges := provider.viewClosure(targets)
	expanded := targets
	if len(edges) != 0 {
		expanded = make([]ids.ObjectAddress, len(targets), len(targets)+len(edges))
		copy(expanded, targets)
		for _, edge := range edges {
			expanded = append(expanded, edge.view.Address())
		}
	}
	policies := provider.policyDependentLabels(expanded)
	policies = append(policies, provider.ruleDependentLabels(expanded)...)
	checks, err := checkDependents(provider, expanded, mode)
	if err != nil {
		return err
	}
	defaults, err := defaultDependents(provider, expanded, mode)
	if err != nil {
		return err
	}
	visibleForeign := slices.ContainsFunc(foreign, func(edge domainDependency) bool { return !edge.automatic })
	if len(edges) == 0 && len(checks) == 0 && len(defaults) == 0 && len(policies) == 0 && !visibleForeign {
		return foreignKeyDependents(provider, foreign)
	}
	if mode == parser.DropRestrict {
		labels := supportedDependentLabelsWithPolicies(provider.base, targets, checks, edges, defaults, provider.path, policies, foreign...)
		multipleDeletion := provider.scope != nil && provider.scope.inheritance.alter != nil
		return supportedRestrictErrorLabels(provider.base, edges, provider.path, labels, multipleDeletion)
	}
	if err := foreignKeyDependents(provider, foreign); err != nil {
		return err
	}
	if err := provider.policyDependents(expanded, mode); err != nil {
		return err
	}
	if err := provider.ruleDependents(expanded, mode); err != nil {
		return err
	}
	activeEdges := edges[:0]
	for _, edge := range edges {
		d := edge.view
		if provider.removed(d.Address()) {
			continue
		}
		if err := provider.removeObjectLocked(provider.owner.catalog.Domain.Ref(provider.base.Epoch(), d.Address()), false); err != nil {
			return err
		}
		activeEdges = append(activeEdges, edge)
	}
	for _, dependent := range supportedDependentLabelsWithPolicies(provider.base, targets, checks, activeEdges, defaults, provider.path, policies, foreign...) {
		provider.cascadeNotice("drop cascades to " + dependent.label)
	}
	return nil
}

func validateViewDefinition(v catalog.Read, d *catalog.Relation) error {
	if d.View == nil {
		return fmt.Errorf("stuntgres: invalid view definition")
	}
	return d.View.Validate(v, d)
}
