package core

import (
	"iter"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// ownedDropPhase is the existing sequence expression phase, followed by its
// actual sequence removal/tombstone. It stores selected metadata, never a MAC
// grant or a pretend already-removed catalog.
type ownedDropPhase struct {
	reference   ids.ObjectRef
	relation    *catalog.Relation
	expressions *DropDependencyPlan
}

type dropPhaseCapture struct {
	selected map[ids.ObjectAddress]bool
	edges    map[ids.ObjectAddress]bool
	plans    []*DropDependencyPlan
}

func (p *DropProvider) prepareDropDependencies(roots []ids.ObjectAddress, mode parser.DropMode, subject, column ids.ObjectAddress) (*DropDependencyPlan, error) {
	capture := dropPhaseCapture{}
	plan, err := p.captureDropDependencyPhase(roots, mode, subject, column, true, &capture)
	if err != nil {
		return nil, err
	}
	if p.sepgsqlRetirementOwner() != nil {
		var targets []ids.ObjectAddress
		var edges []table.DomainDependency
		for _, phase := range capture.plans {
			targets = append(targets, phase.effectPlan.Targets...)
			edges = append(edges, phase.effectEdges...)
		}
		// Raw removal and index evolution have additional real physical owners.
		// Capture the incumbent worker's exact products, not another closure or a
		// pretend removed catalog. All grants precede the single semantic cohort.
		raw, err := p.prepareRawDropEffects(targets)
		if err != nil {
			return nil, err
		}
		for _, effect := range raw.Effects() {
			if effect.Remove.Address == (ids.ObjectAddress{}) || effect.Internal {
				continue
			}
			if capture.selected[effect.Remove.Address] {
				continue
			}
			capture.selected[effect.Remove.Address] = true
			targets = append(targets, effect.Remove.Address)
			if p.Scope != nil && p.Scope.LockTicket != nil {
				if err = p.Scope.acquireRetiredDefinitionObjects(raw.Base, []ids.ObjectAddress{effect.Remove.Address}); err != nil {
					return nil, err
				}
				if effect.Remove.Address.ClassID == ids.RelationClassID {
					if err = p.Acquire(effect.Remove, true); err != nil {
						return nil, err
					}
				}
			}
		}
		// Every ordinary primary/secondary RESTRICT, identity and resource grant has
		// succeeded. Only now decide the combined source cohort, while labels live.
		if err = p.PrepareSepgsqlRetirements(targets, edges); err != nil {
			return nil, err
		}
	}
	return plan, nil
}

func (p *DropProvider) captureDropDependencyPhase(roots []ids.ObjectAddress, mode parser.DropMode, subject, column ids.ObjectAddress, top bool, capture *dropPhaseCapture) (*DropDependencyPlan, error) {
	plan, err := p.prepareExpressionDependencies(roots, mode, subject, column, capture.edges)
	if err != nil {
		return nil, err
	}
	capture.plans = append(capture.plans, plan)
	if len(plan.edges) != 0 {
		if capture.edges == nil {
			capture.edges = make(map[ids.ObjectAddress]bool, len(plan.edges))
		}
		for _, edge := range plan.edges {
			capture.edges[edge.Address] = true
		}
	}
	if capture.selected == nil {
		capture.selected = make(map[ids.ObjectAddress]bool)
	}
	for _, target := range plan.effectPlan.Targets {
		if target.SubID == 0 {
			capture.selected[target] = true
		}
	}
	// The old primary tail owns hierarchy-induced sequences before the caller's
	// later ordinary owned phase. Preserve that physical phase order exactly.
	if len(plan.plan.Hierarchy) != 0 {
		plan.hierarchyOwned, err = p.captureOwnedDropPhase(plan, plan.plan.Hierarchy, mode, capture)
		if err != nil {
			return nil, err
		}
	}
	if top && column == (ids.ObjectAddress{}) {
		plan.rootOwned, err = p.captureOwnedDropPhase(plan, plan.roots, mode, capture)
		if err != nil {
			return nil, err
		}
	}
	return plan, nil
}

func (p *DropProvider) dropPhaseRemovalInputs(plan *DropDependencyPlan) iter.Seq2[ids.ObjectAddress, catalog.Descriptor] {
	return func(yield func(ids.ObjectAddress, catalog.Descriptor) bool) {
		for address, descriptor := range p.RemovalDescriptors() {
			if !yield(address, descriptor) {
				return
			}
		}
		// These are exact selected whole effects/roots from the existing plan, used
		// only for canonical ownership classification. No reader claims removal and
		// no permission is ever decided from this forecast descriptor.
		for _, address := range plan.effectPlan.Targets {
			if address.SubID != 0 {
				continue
			}
			descriptor := p.Descriptor(address)
			if descriptor != nil {
				if _, relation := descriptor.(*catalog.Relation); relation {
					if !yield(address, descriptor) {
						return
					}
				}
			}
		}
	}
}

func (p *DropProvider) captureOwnedDropPhase(primary *DropDependencyPlan, roots []ids.ObjectAddress, mode parser.DropMode, capture *dropPhaseCapture) ([]ownedDropPhase, error) {
	targets := table.OwnedSequenceTargets(p.Base, roots, p.dropPhaseRemovalInputs(primary))
	var result []ownedDropPhase
	for reference, relation := range table.SelectOwnedSequences(p, targets) {
		if capture.selected[reference.Address] {
			continue
		}
		capture.selected[reference.Address] = true
		closure := make([]ids.ObjectAddress, len(targets)+1)
		copy(closure, targets)
		closure[len(targets)] = reference.Address
		expressions, err := p.captureDropDependencyPhase(closure, mode, ids.ObjectAddress{}, ids.ObjectAddress{}, false, capture)
		if err != nil {
			return nil, err
		}
		if p.StatementBound() {
			if err = p.Acquire(reference, false); err != nil {
				return nil, err
			}
		}
		result = append(result, ownedDropPhase{reference: reference, relation: relation, expressions: expressions})
	}
	return result, nil
}

func (p *DropProvider) applyCapturedOwnedDrops(phases []ownedDropPhase) error {
	for _, phase := range phases {
		if err := p.ApplyExpressionDependencies(phase.expressions); err != nil {
			return err
		}
		if err := p.Remove(phase.reference); err != nil {
			return err
		}
		if phase.relation.Sequence.Identity {
			p.TombstoneIdentity(phase.reference.Address)
		}
	}
	return nil
}

// ApplyOwnedExpressionDependencies consumes the caller's already captured
// whole-owned phase after its primary effects. It never rediscovers a closure
// or acquires an after-the-fact allow against an own-retired parent/schema.
func (p *DropProvider) ApplyOwnedExpressionDependencies(plan *DropDependencyPlan) error {
	if plan == nil || plan.provider != p || !plan.consumed || plan.ownedConsumed {
		return ids.ErrCatalogReplaced
	}
	if plan.stage != p.Stage || plan.prepared != p.Prepared || plan.scope != p.Scope || plan.changes != p.Changes || plan.epoch != p.Base.Epoch() {
		return ids.ErrCatalogReplaced
	}
	if err := p.Ctx.Err(); err != nil {
		return err
	}
	if p.Stage != nil && (p.Stage.Lifecycle != CatalogOpen || p.Stage.Invalid != nil) {
		return ids.ErrCatalogReplaced
	}
	if p.Scope != nil {
		if err := p.Scope.Active(); err != nil {
			return err
		}
	}
	plan.ownedConsumed = true
	return p.applyCapturedOwnedDrops(plan.rootOwned)
}
