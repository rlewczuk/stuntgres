package core

import (
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
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
		if storage := r.statisticsStorage(physical.Address()); storage != nil && storage.Maintenance.Scanned {
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
	if !only && (relation.Partition != nil || catalog.InheritanceHasChildren(read, relation.Address())) {
		metadata := r.statisticsMetadata(relation.Address())
		if metadata != nil && metadata.InheritedScanned {
			rows = min(plan.ExplainEstimateLimit, float64(metadata.InheritedTuples))
		}
	}
	return rows, nil
}

// filterEstimate captures only immutable column metadata. Comparisons are
// detached from Engine.Mu just like collection; no expression is executed.
func (r *explainRenderer) filterEstimate(context plan.Node, predicate expr.Expr, estimate plan.ExplainEstimate, inputRows float64) (plan.ExplainEstimate, error) {
	var inputs []plan.ColumnStatisticInput
	plan.WalkExpression(predicate, func(expression expr.Expr) {
		column, ok := expression.(plan.ColumnExpr)
		if !ok || column.Virtual != nil || column.Origin.Address.SubID <= 0 {
			return
		}
		address := column.Origin.Address
		attribute := address.SubID
		address.SubID = 0
		relation, ok := r.auth.Catalog.Relation(address)
		if !ok || column.Origin.Epoch != r.auth.Catalog.Epoch() {
			return
		}
		owner := context
		if frame := r.frames[column.Frame]; frame != nil && !explainContains(frame, context) {
			owner = frame
		}
		source, slot := explainStatisticSource(owner, column.Slot)
		if source == nil || source.Ref.Address != address || slot != relation.IndexForAttribute(attribute) {
			return
		}
		if column.QueryOccurrence != 0 && column.QueryOccurrence != source.Occurrence+1 {
			return
		}
		only := r.targetOnly && address == r.policyTarget
		if source.Occurrence >= 0 && source.Occurrence < len(r.leaves) {
			only = r.leaves[source.Occurrence].Only
		}
		inherited := !only && (relation.Partition != nil || catalog.InheritanceHasChildren(r.auth.Catalog, address))
		metadata := r.statisticsMetadata(address)
		facts := session.ResolveStatistics(r.scope.PendingCatalog(), address, metadata, relation, r.auth.Catalog)
		if facts != nil {
			if fact := facts.Columns[storage.StatisticsKey{Attribute: attribute, Inherited: inherited}]; fact != nil {
				inputs = append(inputs, plan.ColumnStatisticInput{Column: column, Statistics: fact})
			}
		}
	})
	if len(inputs) == 0 {
		return estimate, nil
	}
	e := r.scope.Engine()
	e.Mu.Unlock()
	selectivity, known, err := plan.EstimateSelectivity(r.scope.executionContext(), predicate, inputs)
	e.Mu.Lock()
	if err == nil && known {
		estimate.Rows = min(plan.ExplainEstimateLimit, max(0, inputRows*selectivity))
	}
	return estimate, err
}

// Existing committed maintenance is nontransactional, even when this statement
// sees a private DDL row projection. New objects have only their private owner.
func (r *explainRenderer) statisticsStorage(address ids.ObjectAddress) *storage.Relation {
	e := r.scope.Engine()
	if store := e.Storage.Relations[address]; store != nil {
		return store
	}
	return e.PrivateStorageLocked(r.scope.PendingCatalog(), address)
}

func (r *explainRenderer) statisticsMetadata(address ids.ObjectAddress) *storage.RelationMetadata {
	return session.ResolveRelationMetadata(r.scope.PendingCatalog(), r.scope.Engine().Storage.RelationMetadata, address)
}

// Ordinary stored-column bindings use frame/slot identity; QueryOccurrence is
// populated only for virtual recipes. Follow actual input coordinates rather
// than choosing an occurrence by relation name (which would mix self-joins).
func explainStatisticSource(root plan.Node, slot int) (*plan.SourceNode, int) {
	if root == nil || slot < 0 || slot >= len(root.Schema()) {
		return nil, 0
	}
	switch node := root.(type) {
	case *plan.SourceNode:
		return node, slot
	case *plan.JoinNode:
		if slot < node.LeftWidth {
			return explainStatisticSource(node.Left, slot)
		}
		if slot < node.LeftWidth+node.RightWidth {
			return explainStatisticSource(node.Right, slot-node.LeftWidth)
		}
	case *plan.FilterNode:
		return explainStatisticSource(node.Child, slot)
	case *plan.SortNode:
		return explainStatisticSource(node.Child, slot)
	case *plan.LimitNode:
		return explainStatisticSource(node.Child, slot)
	case *plan.RowLockNode:
		return explainStatisticSource(node.Child, slot)
	case *plan.EvaluationNode:
		return explainStatisticSource(node.Child, slot)
	}
	return nil, 0
}
