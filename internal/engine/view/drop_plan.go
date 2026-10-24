package view

import (
	"iter"
	"slices"
	"sort"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
)

// DropRoots returns roots followed by the relations in removals that roots
// lack, in OID order: whole relations an earlier pass of the drop already
// removed are retirement roots too, so their dependents are discovered before
// any surviving owner changes. It returns roots itself when nothing is added.
func DropRoots(roots []ids.ObjectAddress, removals iter.Seq2[ids.ObjectAddress, catalog.Descriptor]) []ids.ObjectAddress {
	var retirementRoots []ids.ObjectAddress
	for address, descriptor := range removals {
		if _, relation := descriptor.(*catalog.Relation); relation && !slices.Contains(roots, address) {
			retirementRoots = append(retirementRoots, address)
		}
	}
	if len(retirementRoots) == 0 {
		return roots
	}
	sort.Slice(retirementRoots, func(i, j int) bool { return retirementRoots[i].ObjectID < retirementRoots[j].ObjectID })
	return append(append([]ids.ObjectAddress(nil), roots...), retirementRoots...)
}

// DropPlan is what a CASCADE drop of roots does with the edges of their
// dependency closure.
type DropPlan struct {
	// Targets are the roots, then each edge's target and each dependent the
	// drop removes or prunes, in edge order; an address may repeat.
	Targets []ids.ObjectAddress
	// Relations are the surviving tables that lose a column or a constraint,
	// in OID order.
	Relations []*catalog.Relation
	// Hierarchy are the inheritance and partition owners the closure
	// reached, in edge order.
	Hierarchy []ids.ObjectAddress
	// ObserveIdentities reports whether the drop reaches an extension, an
	// SQL/MED wrapper or server, or a foreign-handler procedure, whose
	// incoming identities the drop must fence.
	ObserveIdentities bool
}

// PlanDrop classifies the closure edges of roots. It reads r only to check
// whether a reached procedure is a foreign handler, stopping at the first
// fenced identity, and changes nothing.
func PlanDrop(r DropReader, roots []ids.ObjectAddress, edges []table.DomainDependency) DropPlan {
	plan := DropPlan{Targets: append([]ids.ObjectAddress(nil), roots...)}
	seen := make(map[ids.ObjectAddress]bool)
	for _, target := range plan.Targets {
		if target.ClassID == ids.RelationClassID && target.SubID == 0 {
			seen[target] = true
		}
	}
	for _, edge := range edges {
		// Discovery can enter an owned provider without emitting a removal
		// edge for it: relation retirement already removes its row type and
		// array. Preserve the captured provider identity when pruning stored
		// expressions, not just the requested root and dependent addresses.
		if !slices.Contains(plan.Targets, edge.Target) {
			plan.Targets = append(plan.Targets, edge.Target)
		}
		if table.ClosureTargetClass(edge.Address.ClassID) || edge.Column != "" || edge.Relation != nil && edge.Address == edge.Relation.Address() {
			plan.Targets = append(plan.Targets, edge.Address)
			if edge.Address.ClassID == ids.RelationClassID && edge.Address.SubID == 0 {
				seen[edge.Address] = true
			}
		}
		if edge.Relation != nil && edge.Address == edge.Relation.Address() && catalog.HierarchyRelation(edge.Relation) {
			plan.Hierarchy = append(plan.Hierarchy, edge.Address)
		}
	}
	// The final closure can discover SQL/MED providers below a schema,
	// registered procedure or wrapper root. Fence their incoming identities
	// together with the existing extension retirement observation.
	for _, target := range plan.Targets {
		observe := target.ClassID == ids.ExtensionClassID || target.ClassID == ids.ForeignWrapperClassID || target.ClassID == ids.ForeignServerClassID
		if target.ClassID == ids.ProcedureClassID {
			procedure, ok := r.RetirementDescriptor(target).(*catalog.Procedure)
			observe = ok && procedure.Foreign != nil
		}
		if observe {
			plan.ObserveIdentities = true
			break
		}
	}
	for _, edge := range edges {
		if edge.Relation != nil && (edge.Column != "" || edge.Address.ClassID == 2606) && !seen[edge.Relation.Address()] {
			plan.Relations = append(plan.Relations, edge.Relation)
			seen[edge.Relation.Address()] = true
		}
	}
	sort.Slice(plan.Relations, func(i, j int) bool {
		return plan.Relations[i].Address().ObjectID < plan.Relations[j].Address().ObjectID
	})
	return plan
}

// LockRelations are the relations a statement-bound drop locks, in OID
// order: the plan's relations, plus each surviving policy owner and each
// table or foreign table the closure removes. Lock owners are not deletion
// or projection targets; a policy edge captures its surviving owner's
// identity even without a column edge.
func (plan DropPlan) LockRelations(edges []table.DomainDependency) []*catalog.Relation {
	locks := make([]*catalog.Relation, 0, len(plan.Relations))
	seen := make(map[ids.ObjectAddress]bool)
	for _, r := range plan.Relations {
		locks = append(locks, r)
		seen[r.Address()] = true
	}
	for _, edge := range edges {
		r := edge.Relation
		if r == nil || seen[r.Address()] {
			continue
		}
		if edge.Address.ClassID == ids.PolicyClassID || edge.Address == r.Address() && (r.RelationKind == catalog.TableRelation || r.RelationKind == catalog.ForeignRelation) {
			locks = append(locks, r)
			seen[r.Address()] = true
		}
	}
	sort.Slice(locks, func(i, j int) bool { return locks[i].Address().ObjectID < locks[j].Address().ObjectID })
	return locks
}

// IdentityObservationTargets are the provider identities a drop of roots
// fences: every root when an extension retires, otherwise only the whole
// SQL/MED wrappers, servers and foreign-handler procedures among roots.
func IdentityObservationTargets(r DropReader, roots []ids.ObjectAddress) []ids.ObjectAddress {
	if slices.ContainsFunc(roots, func(address ids.ObjectAddress) bool { return address.ClassID == ids.ExtensionClassID }) {
		// Extension retirement retains its existing complete expression fence.
		return slices.Clone(roots)
	}
	// SQL/MED observes only whole provider identities selected by the
	// completed closure, not unrelated relation/type/column targets.
	targets := make([]ids.ObjectAddress, 0, len(roots))
	for _, address := range roots {
		if address.SubID != 0 {
			continue
		}
		switch address.ClassID {
		case ids.ForeignWrapperClassID, ids.ForeignServerClassID:
			targets = append(targets, address)
		case ids.ProcedureClassID:
			if procedure, ok := r.RetirementDescriptor(address).(*catalog.Procedure); ok && procedure.Foreign != nil {
				targets = append(targets, address)
			}
		}
	}
	return targets
}
