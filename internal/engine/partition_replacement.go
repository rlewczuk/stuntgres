package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

// Run only inside the isolated admitted repair statement, after scalar/provider
// remint and before index transport. Published target/TEMP roots are never touched.
func (stage *Engine) repairRemappedHashPartitions(ctx context.Context, evaluation *exec.Context, auth AuthorizationView, snapshot *index.Snapshot) (map[ids.ObjectAddress]bool, error) {
	view := evaluation.Catalog
	var removed map[ids.ObjectAddress]map[int]bool
	var incoming map[ids.ObjectAddress][]storage.RowVersion
	var dirty map[ids.ObjectAddress]bool
	for address, store := range stage.storage.Relations {
		source, ok := view.Relation(address)
		if !ok || source.Membership == nil || source.Partition != nil {
			continue
		}
		root := index.PartitionHashReplayTarget(view, source)
		if root == source {
			continue
		}
		var history index.PartitionRowHistory
		if snapshot != nil {
			history = snapshot.PartitionRows[address]
		}
		if history.Relation == nil && index.PartitionNeedsRowHistory(view, source) {
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
			if history.Relation != nil {
				var err error
				original, err = history.NextRow(ctx, &historyPosition, version)
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
			if destination.Address() == address && history.Relation != nil {
				var err error
				validation, err = retainedChecks.forRow(ctx, history, destination, original, values, snapshot, evaluation)
				if err != nil {
					return nil, err
				}
			}
			detail := func() (string, error) { return exec.FailingRowDetail(values, evaluation.Output, nil, nil, destination) }
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
		original := stage.storage.Relations[address]
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
		stage.mu.Lock()
		stage.storage.InstallRelationLocked(address, &next)
		stage.mu.Unlock()
	}
	return dirty, ctx.Err()
}
