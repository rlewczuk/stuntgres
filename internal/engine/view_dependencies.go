package engine

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
)

func viewDependencyClosure(catalog catalog.Read, targets []ids.ObjectAddress, stage *CatalogStage) []view.DependencyEdge {
	return view.DependentViews(&dropProvider{base: catalog, stage: stage}, targets)
}

func (provider *dropProvider) viewDependents(targets []ids.ObjectAddress, mode parser.DropMode, foreign ...table.DomainDependency) error {
	edges := view.DependentViews(provider, targets)
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
		labels := view.DependentLabelsWithPolicies(provider.base, targets, checks, edges, defaults, provider.path, policies, foreign...)
		multipleDeletion := provider.scope != nil && provider.scope.inheritance.alter != nil
		if multipleDeletion && len(targets) == 1 && targets[0].ClassID == ids.RelationClassID && targets[0].SubID != 0 {
			multipleDeletion = provider.inheritanceColumnDropMultiple(targets[0])
		}
		return view.DependentsRestrictError(provider.base, edges, provider.path, labels, multipleDeletion)
	}
	if err := table.ForeignKeyDependents(provider, foreign); err != nil {
		return err
	}
	if err := view.DropPolicyDependents(provider, expanded, mode); err != nil {
		return err
	}
	if err := view.DropRuleDependents(provider, expanded, mode); err != nil {
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
	for _, dependent := range view.DependentLabelsWithPolicies(provider.base, targets, checks, activeEdges, defaults, provider.path, policies, foreign...) {
		provider.cascadeNotice("drop cascades to " + dependent.Label)
	}
	return nil
}

// A recursive DROP can remove an origin without deleting the child's column.
// The root is one physical deletion; a second exists exactly when an immediate
// child loses its sole nonlocal origin. Without that first deletion no deeper
// column can disappear, so this threshold needs neither a simulated graph nor
// a count of descendants which merely retain/localize their columns.
func (provider *dropProvider) inheritanceColumnDropMultiple(target ids.ObjectAddress) bool {
	state := provider.scope.inheritance.alter
	if state.Only {
		return false
	}
	address := target
	address.SubID = 0
	source := provider.base.MustRelation(address)
	slot := source.IndexForAttribute(target.SubID)
	if slot < 0 {
		return false
	}
	name := source.Columns[slot].Name
	rootSlot, exists := state.Root.Index[name]
	if !exists {
		return false
	}
	attribute := state.Root.Attributes[rootSlot]
	for _, child := range state.Targets {
		if child.Inheritance == nil {
			continue
		}
		childSlot, exists := child.Index[name]
		if !exists {
			continue
		}
		fact := child.Inheritance.Columns[child.Attributes[childSlot]]
		if !fact.Local && len(fact.Origins) == 1 && fact.Origins[0].Parent == state.Root.Address() && fact.Origins[0].Attribute == attribute {
			return true
		}
	}
	return false
}
