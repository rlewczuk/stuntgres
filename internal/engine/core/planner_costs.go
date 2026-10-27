package core

import (
	"cmp"
	"slices"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
)

func plannerSourceRows(store *storage.Relation) float64 {
	if store != nil && store.Maintenance.Scanned {
		return max(0, float64(store.Maintenance.Tuples))
	}
	return 1000
}

// explainScan uses only the admitted catalog and published maintenance/root
// metadata. It never opens a source, reads rows/buckets, or evaluates a probe.
func (r *explainRenderer) explainScan(filter *plan.FilterNode, frame uint32) plan.ScanDecision {
	s := r.scope
	costs := s.Op.PlannerCosts
	leaf, ok := filter.Child.(*plan.SourceNode)
	if !ok {
		return costs.Scan(1000, 1, ids.ObjectAddress{}, "")
	}
	e := s.Engine()
	store := r.statisticsStorage(leaf.Ref.Address)
	rows := plannerSourceRows(store)
	full := costs.Scan(rows, 1, ids.ObjectAddress{}, "")
	if !s.implicit || s.Catalog != nil || len(r.leaves) != 1 || r.leaves[0].Mark.Mode != 0 {
		return full
	}
	relation, ok := r.auth.Catalog.Relation(leaf.Ref.Address)
	if !ok || relation.RelationKind != catalog.TableRelation || relation.Partition != nil || catalog.InheritanceHasChildren(r.auth.Catalog, relation.Address()) || store == nil {
		return full
	}
	principal := r.auth.State.CurrentUser
	if r.leaves[0].Principal != (authorization.PrincipalRef{}) {
		principal = r.leaves[0].Principal
	}
	security, err := access.SelectRowSecurity(r.auth.Roles, relation, principal, catalog.PolicySelect, false, false)
	if err != nil || security.Count != 0 {
		return full
	}
	var indexes []exec.CapturedHashIndex
	for address, carrier := range e.Storage.IndexRoots {
		idx, ok := r.auth.Catalog.Relation(address)
		if !ok || idx.SQLIndex == nil || idx.SQLIndex.Method != catalog.HashMethod || idx.SQLIndex.Parent != leaf.Ref.Address || !idx.SQLIndex.Ready || !idx.SQLIndex.Valid || !idx.SQLIndex.Live || carrier == nil || carrier.Hash == nil {
			continue
		}
		root := carrier.Hash
		if root.Storage != store || root.Generation != store.Generation || root.Prefix != len(store.Versions) || root.Ends != store.Ends {
			continue
		}
		indexes = append(indexes, exec.CapturedHashIndex{Address: address, Name: idx.Name(), Definition: idx.SQLIndex, Root: root, Usage: root.Binding.Usage})
	}
	slices.SortFunc(indexes, func(a, b exec.CapturedHashIndex) int { return cmp.Compare(a.Address.ObjectID, b.Address.ObjectID) })
	// OrdinaryEquality/EqualityFor inspect concrete typed metadata only;
	// neither invokes the class's hash/equality implementation.
	return exec.ChooseScan(filter, leaf.Ref, 1, frame, s.PlannerMethods, costs, rows, 1, indexes)
}
