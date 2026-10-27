package core

import (
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/table"
)

// Source statistics use only metadata in the admitted catalog window. Hierarchy
// identities follow the same metadata closure as query admission; no source is
// opened, counted, captured or sampled.
func (r *explainRenderer) sourceStatistics(tree *explainTree, source *plan.SourceNode, relation *catalog.Relation) (float64, error) {
	only := r.targetOnly && relation.Address() == r.policyTarget
	if source.Occurrence >= 0 && source.Occurrence < len(r.leaves) {
		leaf := r.leaves[source.Occurrence]
		if leaf.Ref.Address == source.Ref.Address {
			only = leaf.Only
		}
	}
	read := partition.CaptureCatalog(r.scope.inheritanceRead(r.auth.Catalog), r.scope.PartitionRelations)
	var rows float64
	var names []string
	visit := func(physical *catalog.Relation) error {
		names = append(names, r.objectName(physical.Namespace(), physical.Name()))
		estimate := float64(1000)
		if storage := r.scope.Engine().PrivateStorageLocked(r.scope.PendingCatalog(), physical.Address()); storage != nil && storage.Maintenance.Scanned {
			estimate = float64(storage.Maintenance.Tuples)
		}
		rows = min(plan.ExplainEstimateLimit, rows+estimate)
		return nil
	}
	var partitionTree func(*catalog.Relation) error
	partitionTree = func(parent *catalog.Relation) error {
		if parent.RelationKind != catalog.PartitionedRelation {
			return visit(parent)
		}
		if only {
			return nil
		}
		for _, address := range parent.Partition.Children {
			child, ok := read.Relation(address)
			if !ok {
				return ids.ErrCatalogReplaced
			}
			if err := partitionTree(child); err != nil {
				return err
			}
		}
		return nil
	}
	var err error
	if relation.RelationKind == catalog.PartitionedRelation {
		err = partitionTree(relation)
	} else if relation.RelationKind == catalog.TableRelation && !only {
		err = table.InheritanceQueryClosure(read, relation, r.scope.Op.Session.SessionToken, visit)
	} else {
		err = visit(relation)
	}
	if err != nil {
		return 0, err
	}
	if len(names) > 1 || relation.RelationKind == catalog.PartitionedRelation {
		tree.details = append(tree.details, "Planned Sources: "+strings.Join(names, ", "))
	}
	return rows, nil
}
