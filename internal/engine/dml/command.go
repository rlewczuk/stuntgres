package dml

import (
	"context"
	"errors"
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

// Command is the DML state of the active command of a statement. The engine
// embeds it, as a value, in the per-command mutation it swaps between the
// commands of a writable CTE and around rule and SQL routine bodies; a host
// hands out a pointer to the active slot, so readers always see the
// command that runs now. Fields are grouped by owner.
type Command struct {
	InheritedTargets []exec.Leaf // inheritance expansion of the target (table, dml)
	ViewWrite        *view.Write // auto-updatable view lowering (view)
	Indexes
	Insert
	Partition
	RLS view.RowSecurity
}

// Indexes holds the unique and exclusion write bindings (index).
type Indexes struct {
	UniqueBinding *index.UniqueWriteBinding
	IndexBindings map[ids.ObjectAddress]*index.UniqueWriteBinding
}

// Insert holds INSERT's ON CONFLICT and RETURNING state.
type Insert struct {
	InsertConflict  *InsertConflictState
	InsertReturning *InsertReturningOutput
}

// Partition holds INSERT partition routing (partition).
type Partition struct {
	PartitionInsert    *partition.InsertState
	PartitionRelations map[ids.ObjectAddress]*catalog.Relation
}

// TargetRefs is ref followed by the inherited targets' refs.
func (c *Command) TargetRefs(ref ids.ObjectRef) []ids.ObjectRef {
	refs := make([]ids.ObjectRef, 1, len(c.InheritedTargets)+1)
	refs[0] = ref
	for _, target := range c.InheritedTargets {
		refs = append(refs, target.Ref)
	}
	return refs
}

// Storage is the storage of ref when it is the admitted target (target,
// targetStorage) or one of the inherited targets, else nil.
func (c *Command) Storage(target ids.ObjectRef, targetStorage *storage.Relation, ref ids.ObjectRef) *storage.Relation {
	if ref == target {
		return targetStorage
	}
	for _, target := range c.InheritedTargets {
		if target.Ref == ref {
			return target.Storage
		}
	}
	return nil
}

// Capture the already admitted manifest, never rediscover a graph after the
// command view has frozen its roots. Each segment retains real physical rows.
func (c *Command) CaptureSource(ctx context.Context, dataView *visibility.View, b analyze.Binding) (*exec.Source, error) {
	if c.RLS.TargetDenied {
		return view.CaptureDeniedQuerySource(dataView, b.Ref)
	}
	if len(c.InheritedTargets) == 0 {
		return exec.CaptureSource(ctx, dataView, b.Ref)
	}
	pin := dataView.Retain()
	if pin == nil {
		return nil, errors.New("stuntgres: visibility view is released")
	}
	source := &exec.Source{Ref: b.Ref, View: pin, Inheritance: make([]exec.InheritanceQuerySegment, 0, len(c.InheritedTargets)+1), InheritanceEnds: make([]int, len(c.InheritedTargets)+1)}
	root := exec.Leaf{Ref: b.Ref, Relation: b.Relation, Storage: b.Storage, Only: true}
	for i := 0; i <= len(c.InheritedTargets); i++ {
		target := root
		if i != 0 {
			target = c.InheritedTargets[i-1]
		}
		layout, err := catalog.InheritanceQueryLayout(b.Relation, target.Relation)
		if err != nil {
			source.Close()
			return nil, err
		}
		prefix, err := exec.CaptureSource(ctx, dataView, target.Ref)
		if err != nil {
			source.Close()
			return nil, err
		}
		segment := exec.InheritanceQuerySegment{Leaf: target, Prefix: prefix, Layout: layout}
		if layout != nil {
			segment.Scratch = make([]sqltype.Value, len(layout))
		}
		source.Inheritance = append(source.Inheritance, segment)
		total := prefix.Count()
		if i != 0 {
			total += source.InheritanceEnds[i-1]
		}
		source.InheritanceEnds[i] = total
	}
	return source, nil
}

// WriteOverlay is the INSERT write overlay of the active command: ON
// CONFLICT DO UPDATE's or RETURNING's, or nil for neither.
func (c *Command) WriteOverlay() *InsertWriteOverlay {
	if c.InsertConflict != nil && c.InsertConflict.Update != nil {
		return &c.InsertConflict.InsertWriteOverlay
	}
	if c.InsertReturning != nil {
		return &c.InsertReturning.Overlay
	}
	return nil
}

// PartitionStoredRow is the row to store for the routed partition row. Input
// tuples already own their payload, and identity layouts borrow them. A
// reordered tuple can reuse that payload when there is no logical RETURNING
// consumer; otherwise storage must own a separate immutable physical tuple.
func (c *Command) PartitionStoredRow(row []sqltype.Value) []sqltype.Value {
	state := c.PartitionInsert
	if state.Layouts[state.Destination.Address()] == nil {
		return row
	}
	if c.InsertReturning != nil {
		return slices.Clone(state.PhysicalRow)
	}
	copy(row, state.PhysicalRow)
	return row
}

// PartitionInsertionGroup is the insertion group of the routed destination,
// created in first-use order.
func (c *Command) PartitionInsertionGroup(ref ids.ObjectRef) *partition.InsertGroup {
	state := c.PartitionInsert
	address := state.Destination.Address()
	group := state.Groups[address]
	if group == nil {
		group = &partition.InsertGroup{Ref: ref.Domain().Ref(ref.Epoch, address)}
		if state.Groups == nil {
			state.Groups = make(map[ids.ObjectAddress]*partition.InsertGroup)
		}
		state.Groups[address] = group
		state.Order = append(state.Order, group)
	}
	return group
}

// PartitionReturningGroup is PartitionInsertionGroup with the group's unique
// binding made active, for a RETURNING row.
func (c *Command) PartitionReturningGroup(ref ids.ObjectRef) *partition.InsertGroup {
	group := c.PartitionInsertionGroup(ref)
	c.UniqueBinding = group.Unique
	return group
}
