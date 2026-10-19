package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func hasHashPartition(snapshot *catalog.State) bool {
	for _, d := range snapshot.Descriptors() {
		if relation, ok := d.(*catalog.Relation); ok && relation.Partition != nil && relation.Partition.Strategy == parser.PartitionHash {
			return true
		}
	}
	return false
}

// Partition rows have equal named columns, but private replay may preserve a
// different physical order. Borrow values unless a real layout change is needed.
func partitionReplacementRow(from, to *catalog.Relation, row []sqltype.Value) []sqltype.Value {
	same := true
	for i, c := range to.Columns {
		if from.Columns[i].Name != c.Name {
			same = false
			break
		}
	}
	if same {
		return row
	}
	values := make([]sqltype.Value, len(to.Columns))
	for i, c := range to.Columns {
		values[i] = row[from.Index[c.Name]]
	}
	return values
}

// Run only inside the isolated admitted repair statement, after scalar/provider
// remint and before index transport. Published target/TEMP roots are never touched.
func repairRemappedHashPartitions(ctx context.Context, evaluation *EvalContext, relations map[ids.ObjectAddress]*storage.Relation, auth AuthorizationView, snapshot *indexSnapshot) (map[ids.ObjectAddress]bool, error) {
	view := evaluation.catalog
	var removed map[ids.ObjectAddress]map[int]bool
	var incoming map[ids.ObjectAddress][]storage.RowVersion
	var dirty map[ids.ObjectAddress]bool
	for address, store := range relations {
		source, ok := view.Relation(address)
		if !ok || source.Membership == nil || source.Partition != nil {
			continue
		}
		root := partitionHashReplayTarget(view, source)
		if root == source {
			continue
		}
		var history partitionRowHistory
		if snapshot != nil {
			history = snapshot.partitionRows[address]
		}
		if history.relation == nil && partitionNeedsRowHistory(view, source) {
			return nil, ids.ErrCatalogReplaced
		}
		historyPosition := 0
		var retainedChecks partitionRetainedChecks
		for i, version := range store.Versions {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if !visibility.LiveVersion(version, ^ids.CommitSeq(0), store.Ends, nil) {
				continue
			}
			var original []sqltype.Value
			if history.relation != nil {
				var err error
				original, err = history.nextRow(ctx, &historyPosition, version)
				if err != nil {
					return nil, err
				}
			}
			destination := root
			for destination.Partition != nil {
				var buffer [32]catalog.PartitionDatum
				key, err := partitionRowKey(destination, partitionReplacementRow(source, destination, version.Values), evaluation, buffer[:])
				if err != nil {
					return nil, err
				}
				var selected *catalog.Relation
				for _, childAddress := range destination.Partition.Children {
					child := view.MustRelation(childAddress)
					if child.Membership.DefaultBound {
						continue
					}
					contains, err := partitionContains(ctx, destination.Partition.Keys, child.Membership, key, evaluation.Input)
					if err != nil {
						return nil, err
					}
					if contains {
						selected = child
						break
					}
				}
				if selected == nil && destination.Partition.DefaultChild != (ids.ObjectAddress{}) {
					selected = view.MustRelation(destination.Partition.DefaultChild)
				}
				if selected == nil {
					state := partitionInsertState{catalog: view, auth: auth, target: root}
					return nil, state.missingPartition(ctx, destination, key.values, evaluation.Output)
				}
				destination = selected
			}
			values := partitionReplacementRow(source, destination, version.Values)
			validation := destination
			if destination.Address() == address && history.relation != nil {
				var err error
				validation, err = retainedChecks.forRow(ctx, history, destination, original, values, snapshot, evaluation)
				if err != nil {
					return nil, err
				}
			}
			detail := func() (string, error) { return failingRowDetail(values, evaluation.Output, nil, nil, destination) }
			schema := view.Get(destination.Namespace()).Name()
			if err := validatePhysicalRowConstraints(validation, schema, values, evaluation, detail); err != nil {
				return nil, err
			}
			// The highest HASH target can itself have RANGE ancestors; ordinary direct
			// insertion would enforce their constraints as well.
			child := destination
			for child.Membership != nil {
				parent := view.MustRelation(child.Membership.Parent)
				allowed, err := partitionAccepts(view, parent, child, partitionReplacementRow(source, parent, version.Values), evaluation)
				if err != nil {
					return nil, err
				}
				if !allowed {
					return nil, fmt.Errorf("stuntgres: remapped partition row violates ancestor constraint")
				}
				child = parent
			}
			if destination.Address() == address {
				continue
			}
			if dirty == nil {
				removed = make(map[ids.ObjectAddress]map[int]bool)
				incoming = make(map[ids.ObjectAddress][]storage.RowVersion)
				dirty = make(map[ids.ObjectAddress]bool)
			}
			if removed[address] == nil {
				removed[address] = make(map[int]bool)
			}
			removed[address][i] = true
			version.Values = values
			incoming[destination.Address()] = append(incoming[destination.Address()], version)
			dirty[address], dirty[destination.Address()] = true, true
		}
	}
	for address := range dirty {
		original := relations[address]
		if original == nil {
			return nil, ids.ErrCatalogReplaced
		}
		next := *original
		next.Versions = make([]storage.RowVersion, 0, len(original.Versions)-len(removed[address])+len(incoming[address]))
		for i, version := range original.Versions {
			if !removed[address][i] {
				next.Versions = append(next.Versions, version)
			}
		}
		next.Versions = append(next.Versions, incoming[address]...)
		next.Generation++
		relations[address] = &next
	}
	return dirty, ctx.Err()
}
