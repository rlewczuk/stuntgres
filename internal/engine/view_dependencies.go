package engine

import (
	"slices"
	"sort"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
)

func viewDependencyClosure(catalog catalog.Read, targets []ids.ObjectAddress, stage *CatalogStage) []view.DependencyEdge {
	provider := &dropProvider{base: catalog, stage: stage}
	return provider.viewClosure(targets)
}

func (provider *dropProvider) viewClosure(targets []ids.ObjectAddress) []view.DependencyEdge {
	var views []*catalog.Relation
	for address, descriptor := range provider.Descriptors() {
		if provider.Removed(address) {
			continue
		}
		if replacement := provider.Replacement(address); replacement != nil {
			descriptor = replacement
		}
		if d, ok := descriptor.(*catalog.Relation); ok && d.View != nil {
			views = append(views, d)
		}
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Address().ObjectID < views[j].Address().ObjectID })
	return view.DependencyClosure(views, targets)
}

func (provider *dropProvider) viewDependents(targets []ids.ObjectAddress, mode parser.DropMode, foreign ...table.DomainDependency) error {
	edges := provider.viewClosure(targets)
	expanded := targets
	if len(edges) != 0 {
		expanded = make([]ids.ObjectAddress, len(targets), len(targets)+len(edges))
		copy(expanded, targets)
		for _, edge := range edges {
			expanded = append(expanded, edge.View.Address())
		}
	}
	policies := provider.policyDependentLabels(expanded)
	policies = append(policies, provider.ruleDependentLabels(expanded)...)
	checks, err := table.CheckDependents(provider, expanded, mode)
	if err != nil {
		return err
	}
	defaults, err := table.DefaultDependents(provider, expanded, mode)
	if err != nil {
		return err
	}
	visibleForeign := slices.ContainsFunc(foreign, func(edge table.DomainDependency) bool { return !edge.Automatic })
	if len(edges) == 0 && len(checks) == 0 && len(defaults) == 0 && len(policies) == 0 && !visibleForeign {
		return table.ForeignKeyDependents(provider, foreign)
	}
	if mode == parser.DropRestrict {
		labels := supportedDependentLabelsWithPolicies(provider.base, targets, checks, edges, defaults, provider.path, policies, foreign...)
		multipleDeletion := provider.scope != nil && provider.scope.inheritance.alter != nil
		return supportedRestrictErrorLabels(provider.base, edges, provider.path, labels, multipleDeletion)
	}
	if err := table.ForeignKeyDependents(provider, foreign); err != nil {
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
		d := edge.View
		if provider.Removed(d.Address()) {
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
