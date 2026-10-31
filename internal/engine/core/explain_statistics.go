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
	read := partition.CaptureCatalog(r.scope.inheritanceRead(r.auth.Catalog), r.scope.PartitionRelations, r.scope.PendingDetachSnapshot())
	var rows float64
	var names []string
	var identities []map[string]any
	var physicals []*catalog.Relation
	visit := func(physical *catalog.Relation) error {
		if r.scanFilter != nil && r.scanFilter.Child == source {
			physicals = append(physicals, physical)
		}
		names = append(names, r.objectName(physical.Namespace(), physical.Name()))
		if r.options.format == "json" {
			identity := &explainTree{}
			r.relationProperties(identity, physical, physical.Name())
			identities = append(identities, identity.properties)
		}
		estimate := plannerSourceRows(r.statisticsStorage(physical.Address()))
		if metadata := r.statisticsMetadata(physical.Address()); metadata != nil && metadata.ForeignAnalyze != nil {
			estimate = metadata.ForeignAnalyze.TotalRows
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
		r.property(tree, "Planned Sources", identities)
	}
	if err := r.sourcePruning(tree, source, relation, read, physicals, !only && (len(physicals) > 1 || relation.Partition != nil)); err != nil {
		return 0, err
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
	imported := false
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
				imported = imported || fact.Imported != nil
				population := plannerSourceRows(r.statisticsStorage(address))
				if metadata != nil && metadata.ForeignAnalyze != nil {
					population = metadata.ForeignAnalyze.TotalRows
				}
				if inherited && metadata != nil && metadata.InheritedScanned {
					population = min(plan.ExplainEstimateLimit, float64(metadata.InheritedTuples))
				}
				inputs = append(inputs, plan.ColumnStatisticInput{Column: column, Statistics: fact, SourceRows: population})
			}
		}
	})
	if len(inputs) == 0 {
		return estimate, nil
	}
	e := r.scope.Engine()
	e.Mu.Unlock()
	rows, known, err := plan.EstimateSelectivityRows(r.scope.executionContext(), predicate, inputs, inputRows)
	e.Mu.Lock()
	if err == nil && known {
		estimate.Rows = min(plan.ExplainEstimateLimit, max(0, rows))
		// Imported planner facts use PostgreSQL's minimum row estimate for
		// a nonempty input, including an explicitly supplied zero fraction.
		// Wholly measured exact-count estimates retain their existing path.
		if imported && inputRows > 0 {
			estimate.Rows = max(1, estimate.Rows)
		}
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
