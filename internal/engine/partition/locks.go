package partition

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// These newly admitted mutation forms finish ordinary analysis and ACL checks,
// but must not demand defaults, capture virtual storage or publish routed effects.
func WriteExtensionBoundary(statement parser.Statement, d *catalog.Relation) error {
	if d == nil || !catalog.HierarchyRelation(d) {
		return nil
	}
	switch syntax := statement.(type) {
	case *parser.Insert:
		if syntax.Conflict != nil {
			return sqlError("0A000", "ON CONFLICT on partition hierarchies is unsupported")
		}
	case *parser.Merge:
		return sqlError("0A000", "merging partition hierarchies is unsupported")
	}
	return nil
}

// ConsumerBoundary rejects the statements partition hierarchies do not
// support, with their 0A000/42P16 diagnostics. replayAllowed is the caller's
// Restore trust check for the ALTER TABLE and ALTER ... DEFAULT replays; it is
// consulted only for those statements, at the same point as before the move.
// GiST CREATE admission uses only the captured method's native authority;
// method and option diagnostics remain the ordinary index binder's responsibility.
func ConsumerBoundary(read catalog.Read, statement parser.Statement, d *catalog.Relation, replayAllowed func(parser.Statement, *catalog.Relation) bool) error {
	if d.Membership != nil && d.Membership.DetachPending {
		switch s := statement.(type) {
		case *parser.AlterTable, *parser.AlterTableDefault, *parser.AlterTableAttachPartition, *parser.AlterTableOf:
			return PendingDetachError(d, false)
		case *parser.AlterTableDetachPartition:
			if s.Mode != parser.DetachFinalize {
				return PendingDetachError(d, false)
			}
		case *parser.AlterOwner:
			if s.Kind == "table" {
				return PendingDetachError(d, false)
			}
		case *parser.AlterTableRenameColumn, *parser.AlterTableRenameTable, *parser.AlterTableSetSchema:
			// Rename and namespace transfer are separate utility paths, not ATPrepCmd.
			// Their ordinary identity/ownership executors retain the pending edge.
			return nil
		}
	}
	if !catalog.HierarchyRelation(d) {
		return nil
	}
	message := ""
	switch s := statement.(type) {
	case *parser.Delete:
		message = "deleting from partition hierarchies is unsupported"
	case *parser.Select:
		if s.Locking != nil {
			message = "row locking partition hierarchies is unsupported"
		}
	case *parser.LockTable:
		message = "locking partition hierarchies is unsupported"
	case *parser.CreateIndex:
		if d.Partition != nil && !s.Unique {
			method, ok := read.LookupAccessMethod(s.Method)
			if !ok || method.Native != catalog.GistAccessMethod && method.Native != catalog.SpgistAccessMethod {
				message = "partitioned indexes are unsupported"
			}
		}
	case *parser.Reindex:
		if d.Partition != nil {
			message = "partitioned indexes are unsupported"
		}
	case *parser.AlterTable:
		for _, action := range s.Actions {
			if action.Action == parser.InheritTable {
				return table.RelationshipChildKind(d, action.Action)
			}
		}
		for _, action := range s.Actions {
			if action.Action == parser.SetLogged || action.Action == parser.SetUnlogged || action.Action == parser.SetAccessMethod {
				// Ordered ordinary preparation owns exact property eligibility;
				// method assignment is supported without descending into leaves.
				return nil
			}
		}
		if table.RelationshipOnly(s.Actions) {
			return nil // Detach rejects actual partitions at its misc action.
		}
		if table.ColumnStorageOnly(s.Actions) {
			return nil
		}
		if access.RowSecurityActions(s.Actions) || table.TablespaceOnlyActions(s.Actions) || table.RelationOptionsOnly(s.Actions) {
			return nil
		}
		if ExclusionAddActions(s) {
			return nil
		}
		if index.PartitionConstraintDropActions(d, s.Actions) {
			return nil
		}
		if replayAllowed(s, d) {
			return nil
		}
		if table.PartitionConstraintActions(d, s.Actions) {
			return nil
		}
		if table.HasRelationOptions(s.Actions) {
			// Prepare private candidates, then retain the executor's unsupported
			// shape guard before any journal transfer. This is not propagation.
			return nil
		}
		for _, action := range s.Actions {
			if action.Action != parser.DropColumn {
				continue
			}
			if s.Only && d.Partition != nil && len(d.Partition.Children) != 0 {
				err := sqlError("42P16", "cannot drop column from only the partitioned table when partitions exist").(*sqlstate.Error)
				err.Hint = "Do not specify the ONLY keyword."
				return err
			}
			if slot, ok := d.Index[action.Column.Text]; ok && KeyUsesAttribute(d, d.Attributes[slot]) {
				return sqlError("42P16", fmt.Sprintf(`cannot drop column "%s" because it is part of the partition key of relation "%s"`, action.Column.Text, d.Name()))
			}
		}
		message = "altering partition hierarchies is unsupported"
	case *parser.AlterTableDefault:
		if !replayAllowed(s, d) {
			message = "altering partition hierarchies is unsupported"
		}
	case *parser.AlterTableRenameColumn, *parser.AlterTableRenameTable, *parser.AlterTableSetSchema:
		message = "altering partition hierarchies is unsupported"
	}
	if message != "" {
		return sqlError("0A000", message)
	}
	return nil
}

func KeyUsesAttribute(d *catalog.Relation, attribute int32) bool {
	if d.Partition == nil {
		return false
	}
	for _, key := range d.Partition.Keys {
		if key.Attribute == attribute {
			return true
		}
		if key.Derived != nil {
			for _, ref := range key.Derived.Dependencies {
				if ref.Address.ClassID == ids.RelationClassID && ref.Address.ObjectID == d.Address().ObjectID && ref.Address.SubID == attribute {
					return true
				}
			}
		}
	}
	return false
}

// The stored defining arrays/key providers remain identical. Only an appended
// child manifest may cross the read admission fence; no name/shape retargeting.
func AppendCompatible(old, current *catalog.Relation) bool {
	if old == nil || current == nil || old.Partition == nil || current.Partition == nil ||
		!catalog.RelationDefiningAuthorityCompatible(old, current) || old.RelationKind != current.RelationKind ||
		old.Membership != current.Membership || old.Inheritance != current.Inheritance ||
		old.Partition.Strategy != current.Partition.Strategy ||
		len(old.Partition.Keys) != len(current.Partition.Keys) ||
		len(current.Partition.Children) <= len(old.Partition.Children) ||
		&old.Partition.Keys[0] != &current.Partition.Keys[0] ||
		!slices.Equal(old.Partition.Children, current.Partition.Children[:len(old.Partition.Children)]) {
		return false
	}
	return old.Partition.DefaultChild == (ids.ObjectAddress{}) || old.Partition.DefaultChild == current.Partition.DefaultChild
}

// PendingCompatible admits exactly the constructor's stamped first-phase
// publication, including inherited CHECK propagation. Later revision clones,
// final removal, imported graphs and unrelated same-shape replacements fail.
func PendingCompatible(old, current *catalog.Relation) bool {
	if old == nil || current == nil || current.History.DetachPredecessor != old ||
		current.Revision() != current.History.DetachPublication ||
		!catalog.RelationDefiningAuthorityCompatible(old, current) ||
		old.RelationKind != current.RelationKind || old.Partition != current.Partition ||
		old.Inheritance != current.Inheritance || old.Membership == nil || current.Membership == nil {
		return false
	}
	a, b := old.Membership, current.Membership
	root := current.History.DetachRoot
	if root == (ids.ObjectAddress{}) || current.Address() == root && !b.DetachPending ||
		current.Address() != root && a != b {
		return false
	}
	if a.DetachPending || a.Parent != b.Parent || a.Strategy != b.Strategy || a.DefaultBound != b.DefaultBound ||
		a.Modulus != b.Modulus || a.Remainder != b.Remainder ||
		len(a.Lower) != len(b.Lower) || len(a.Upper) != len(b.Upper) || len(a.Values) != len(b.Values) {
		return false
	}
	return (len(a.Lower) == 0 || &a.Lower[0] == &b.Lower[0]) &&
		(len(a.Upper) == 0 || &a.Upper[0] == &b.Upper[0]) &&
		(len(a.Values) == 0 || &a.Values[0] == &b.Values[0])
}

// CaptureCatalog pins admitted append-growth descriptors and derives the same
// pending-edge topology used by lock discovery and routing. Snapshot authority
// is restricted to current pending-edge eligibility, never name resolution.
func CaptureCatalog(read catalog.Read, admitted map[ids.ObjectAddress]*catalog.Relation, snapshot *catalog.State) catalog.Read {
	var state *catalog.State
	for address, captured := range admitted {
		current, _ := read.Relation(address)
		selected := current
		if current != captured && (AppendCompatible(captured, current) || PendingCompatible(captured, current)) {
			selected = captured
		}
		if selected == nil {
			continue
		}
		if selected.Partition != nil {
			children := EffectiveChildren(read, selected, snapshot)
			if len(children) != len(selected.Partition.Children) {
				next, definition := *selected, *selected.Partition
				definition.Children = children
				next.Partition = &definition
				selected = &next
			}
		}
		if selected != current {
			if state == nil {
				next := *read.State()
				state = &next
			}
			state.SetDescriptor(address, selected)
		}
	}
	if state == nil {
		return read
	}
	return read.WithState(state)
}

// Ancestors returns the ancestors of ref in view, root first, for the caller
// to lock before the leaf. A missing relation or a stale epoch is
// ids.ErrCatalogReplaced.
func Ancestors(view catalog.Read, ref ids.ObjectRef) ([]ids.ObjectRef, error) {
	d, ok := view.Relation(ref.Address)
	if !ok || ref.Epoch != view.Epoch() {
		return nil, ids.ErrCatalogReplaced
	}
	var ancestors []ids.ObjectRef
	for d.Membership != nil {
		d, ok = view.Relation(d.Membership.Parent)
		if !ok {
			return nil, ids.ErrCatalogReplaced
		}
		ancestors = append(ancestors, ref.Domain().Ref(ref.Epoch, d.Address()))
	}
	slices.Reverse(ancestors)
	return ancestors, nil
}
