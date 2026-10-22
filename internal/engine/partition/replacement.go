package partition

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func HasHashPartition(snapshot *catalog.State) bool {
	for _, d := range snapshot.Descriptors() {
		if relation, ok := d.(*catalog.Relation); ok && relation.Partition != nil && relation.Partition.Strategy == parser.PartitionHash {
			return true
		}
	}
	return false
}

// Partition rows have equal named columns, but private replay may preserve a
// different physical order. Borrow values unless a real layout change is needed.
func ReplacementRow(from, to *catalog.Relation, row []sqltype.Value) []sqltype.Value {
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

// HashRepair is the row movement of a remapped hash partition repair: per
// relation, the version indexes that leave it (Removed), the versions that
// arrive (Incoming, values in the destination's layout), and every relation
// either touches (Dirty).
type HashRepair struct {
	Removed  map[ids.ObjectAddress]map[int]bool
	Incoming map[ids.ObjectAddress][]storage.RowVersion
	Dirty    map[ids.ObjectAddress]bool
}

// RepairRemappedHash routes every live row of the remapped leaf partitions in
// relations from its highest hash ancestor down again, validates it against
// the destination's constraints (retained CHECKs through snapshot's row
// history) and every ancestor's bound, and returns the rows that move. It
// changes nothing; the caller installs the result. A row with no partition is
// the MissingPartition 23514 under authority and env.
func RepairRemappedHash(ctx context.Context, evaluation *exec.Context, authority access.View, env access.Environment, relations map[ids.ObjectAddress]*storage.Relation, snapshot *index.Snapshot) (HashRepair, error) {
	var repair HashRepair
	view := evaluation.Catalog
	for address, store := range relations {
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
			return HashRepair{}, ids.ErrCatalogReplaced
		}
		historyPosition := 0
		var retainedChecks RetainedChecks
		for i, version := range store.Versions {
			if err := ctx.Err(); err != nil {
				return HashRepair{}, err
			}
			if !visibility.LiveVersion(version, ^ids.CommitSeq(0), store.Ends, nil) {
				continue
			}
			var original []sqltype.Value
			if history.Relation != nil {
				var err error
				original, err = history.NextRow(ctx, &historyPosition, version)
				if err != nil {
					return HashRepair{}, err
				}
			}
			destination := root
			for destination.Partition != nil {
				var buffer [32]catalog.PartitionDatum
				key, err := RowKey(destination, ReplacementRow(source, destination, version.Values), evaluation, buffer[:])
				if err != nil {
					return HashRepair{}, err
				}
				var selected *catalog.Relation
				for _, childAddress := range destination.Partition.Children {
					child := view.MustRelation(childAddress)
					if child.Membership.DefaultBound {
						continue
					}
					contains, err := Contains(ctx, destination.Partition.Keys, child.Membership, key, evaluation.Input)
					if err != nil {
						return HashRepair{}, err
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
					state := InsertState{Catalog: view, Authority: authority, Env: env, Target: root}
					return HashRepair{}, state.MissingPartition(ctx, destination, key.Values, evaluation.Output)
				}
				destination = selected
			}
			values := ReplacementRow(source, destination, version.Values)
			validation := destination
			if destination.Address() == address && history.Relation != nil {
				var err error
				validation, err = retainedChecks.ForRow(ctx, history, destination, original, values, snapshot, evaluation)
				if err != nil {
					return HashRepair{}, err
				}
			}
			violation, err := table.PhysicalRowViolation(validation, values, evaluation)
			if err != nil {
				return HashRepair{}, err
			}
			if violation != nil {
				detail, err := exec.FailingRowDetail(values, evaluation.Output, nil, nil, destination)
				if err != nil {
					return HashRepair{}, err
				}
				return HashRepair{}, violation.Diagnostic(view.Get(destination.Namespace()).Name(), detail)
			}
			// The highest HASH target can itself have RANGE ancestors; ordinary direct
			// insertion would enforce their constraints as well.
			child := destination
			for child.Membership != nil {
				parent := view.MustRelation(child.Membership.Parent)
				allowed, err := Accepts(view, parent, child, ReplacementRow(source, parent, version.Values), evaluation)
				if err != nil {
					return HashRepair{}, err
				}
				if !allowed {
					return HashRepair{}, fmt.Errorf("stuntgres: remapped partition row violates ancestor constraint")
				}
				child = parent
			}
			if destination.Address() == address {
				continue
			}
			if repair.Dirty == nil {
				repair.Removed = make(map[ids.ObjectAddress]map[int]bool)
				repair.Incoming = make(map[ids.ObjectAddress][]storage.RowVersion)
				repair.Dirty = make(map[ids.ObjectAddress]bool)
			}
			if repair.Removed[address] == nil {
				repair.Removed[address] = make(map[int]bool)
			}
			repair.Removed[address][i] = true
			version.Values = values
			repair.Incoming[destination.Address()] = append(repair.Incoming[destination.Address()], version)
			repair.Dirty[address], repair.Dirty[destination.Address()] = true, true
		}
	}
	return repair, nil
}
