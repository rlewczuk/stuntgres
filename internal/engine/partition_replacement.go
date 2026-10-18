package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func hasHashPartition(catalog *catalogState) bool {
	for _, d := range catalog.descriptors {
		if relation, ok := d.(*relationDescriptor); ok && relation.partition != nil && relation.partition.strategy == parser.PartitionHash {
			return true
		}
	}
	return false
}

// Partition rows have equal named columns, but private replay may preserve a
// different physical order. Borrow values unless a real layout change is needed.
func partitionReplacementRow(from, to *relationDescriptor, row []sqltype.Value) []sqltype.Value {
	same := true
	for i, c := range to.columns {
		if from.columns[i].Name != c.Name {
			same = false
			break
		}
	}
	if same {
		return row
	}
	values := make([]sqltype.Value, len(to.columns))
	for i, c := range to.columns {
		values[i] = row[from.index[c.Name]]
	}
	return values
}

// Run only inside the isolated admitted repair statement, after scalar/provider
// remint and before index transport. Published target/TEMP roots are never touched.
func repairRemappedHashPartitions(ctx context.Context, evaluation *EvalContext, storage map[ids.ObjectAddress]*relationStorage, auth AuthorizationView, snapshot *indexSnapshot) (map[ids.ObjectAddress]bool, error) {
	catalog := evaluation.catalog
	var removed map[ids.ObjectAddress]map[int]bool
	var incoming map[ids.ObjectAddress][]rowVersion
	var dirty map[ids.ObjectAddress]bool
	for address, store := range storage {
		source, ok := catalog.state.descriptors[address].(*relationDescriptor)
		if !ok || source.membership == nil || source.partition != nil {
			continue
		}
		root := partitionHashReplayTarget(catalog, source)
		if root == source {
			continue
		}
		var history partitionRowHistory
		if snapshot != nil {
			history = snapshot.partitionRows[address]
		}
		if history.relation == nil && partitionNeedsRowHistory(catalog, source) {
			return nil, ErrCatalogReplaced
		}
		historyPosition := 0
		var retainedChecks partitionRetainedChecks
		for i, version := range store.versions {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if !liveVersion(version, ^CommitSeq(0), store.ends, nil) {
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
			for destination.partition != nil {
				var buffer [32]partitionDatum
				key, err := partitionRowKey(destination, partitionReplacementRow(source, destination, version.values), evaluation, buffer[:])
				if err != nil {
					return nil, err
				}
				var selected *relationDescriptor
				for _, childAddress := range destination.partition.children {
					child := catalog.state.descriptors[childAddress].(*relationDescriptor)
					if child.membership.defaultBound {
						continue
					}
					contains, err := partitionContains(ctx, destination.partition.keys, child.membership, key, evaluation.Input)
					if err != nil {
						return nil, err
					}
					if contains {
						selected = child
						break
					}
				}
				if selected == nil && destination.partition.defaultChild != (ids.ObjectAddress{}) {
					selected = catalog.state.descriptors[destination.partition.defaultChild].(*relationDescriptor)
				}
				if selected == nil {
					state := partitionInsertState{catalog: catalog, auth: auth, target: root}
					return nil, state.missingPartition(ctx, destination, key.values, evaluation.Output)
				}
				destination = selected
			}
			values := partitionReplacementRow(source, destination, version.values)
			validation := destination
			if destination.address == address && history.relation != nil {
				var err error
				validation, err = retainedChecks.forRow(ctx, history, destination, original, values, snapshot, evaluation)
				if err != nil {
					return nil, err
				}
			}
			detail := func() (string, error) { return failingRowDetail(values, evaluation.Output, nil, nil, destination) }
			schema := catalog.state.descriptors[destination.namespace].Name()
			if err := validatePhysicalRowConstraints(validation, schema, values, evaluation, detail); err != nil {
				return nil, err
			}
			// The highest HASH target can itself have RANGE ancestors; ordinary direct
			// insertion would enforce their constraints as well.
			child := destination
			for child.membership != nil {
				parent := catalog.state.descriptors[child.membership.parent].(*relationDescriptor)
				allowed, err := partitionAccepts(catalog, parent, child, partitionReplacementRow(source, parent, version.values), evaluation)
				if err != nil {
					return nil, err
				}
				if !allowed {
					return nil, fmt.Errorf("stuntgres: remapped partition row violates ancestor constraint")
				}
				child = parent
			}
			if destination.address == address {
				continue
			}
			if dirty == nil {
				removed = make(map[ids.ObjectAddress]map[int]bool)
				incoming = make(map[ids.ObjectAddress][]rowVersion)
				dirty = make(map[ids.ObjectAddress]bool)
			}
			if removed[address] == nil {
				removed[address] = make(map[int]bool)
			}
			removed[address][i] = true
			version.values = values
			incoming[destination.address] = append(incoming[destination.address], version)
			dirty[address], dirty[destination.address] = true, true
		}
	}
	for address := range dirty {
		original := storage[address]
		if original == nil {
			return nil, ErrCatalogReplaced
		}
		next := *original
		next.versions = make([]rowVersion, 0, len(original.versions)-len(removed[address])+len(incoming[address]))
		for i, version := range original.versions {
			if !removed[address][i] {
				next.versions = append(next.versions, version)
			}
		}
		next.versions = append(next.versions, incoming[address]...)
		next.generation++
		storage[address] = &next
	}
	return dirty, ctx.Err()
}
