package table

import (
	"context"
	"slices"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// ForeignKeyRowsCapture captures the rows of the relation ref names, with
// the root of index idx when idx is not zero. The engine's capture takes
// Engine.mu, so callers hold nothing.
type ForeignKeyRowsCapture func(ref ids.ObjectRef, idx ids.ObjectAddress) (ForeignKeyRows, error)

// ForeignKeyScan is what an initial foreign-key scan reads: the
// authority (View, whose Catalog is the candidate the keys live in, and
// Env) that shapes diagnostics, the domain that names the captured
// relations at the candidate's epoch, the row capture and the session's
// input and output environments.
type ForeignKeyScan struct {
	View    access.View
	Env     access.Environment
	Domain  *ids.Domain
	Capture ForeignKeyRowsCapture
	Input   sqltype.InputEnvironment
	Output  sqltype.OutputEnvironment
}

// ForeignKeyCheck is one key to scan: Key as Child declares it.
type ForeignKeyCheck struct {
	Child *catalog.Relation
	Key   catalog.ForeignKeyConstraint
}

// ResolveAlterForeignKeys resolves an execution-ordered historical worklist
// against the authentic final child, retaining the existing required prefix.
// Scheduled order is authoritative, not a membership filter over declaration
// order. Repeated identities keep their first position.
func ResolveAlterForeignKeys(child *catalog.Relation, scheduled []catalog.ForeignKeyConstraint, prefix []ForeignKeyCheck) ([]ForeignKeyCheck, error) {
	if len(scheduled) == 0 {
		return prefix, nil
	}
	checks := prefix
	for _, selected := range scheduled {
		slot := slices.IndexFunc(child.Constraints.ForeignKeys, func(c catalog.ForeignKeyConstraint) bool { return c.OID == selected.OID })
		if selected.OID == 0 || slot < 0 {
			return nil, ids.ErrCatalogReplaced
		}
		key := child.Constraints.ForeignKeys[slot]
		if key.NotValid || key.Name != selected.Name || key.Target != selected.Target || key.Index != selected.Index || key.Full != selected.Full || key.UpdateAction != selected.UpdateAction || key.DeleteAction != selected.DeleteAction || !slices.Equal(key.Attributes, selected.Attributes) || !slices.Equal(key.Referenced, selected.Referenced) || !slices.Equal(key.DeleteAttributes, selected.DeleteAttributes) {
			return nil, ids.ErrCatalogReplaced
		}
		if slices.ContainsFunc(checks, func(previous ForeignKeyCheck) bool {
			return previous.Child.Address() == child.Address() && previous.Key.OID == key.OID
		}) {
			continue
		}
		if len(checks) == len(prefix) {
			checks = make([]ForeignKeyCheck, len(prefix), len(prefix)+len(scheduled))
			copy(checks, prefix)
		}
		checks = append(checks, ForeignKeyCheck{Child: child, Key: key})
	}
	return checks, nil
}

// ReferencingForeignKeys are the valid keys of view that the relation at
// address declares or is referenced by, in view's descriptor order.
func ReferencingForeignKeys(view catalog.Read, address ids.ObjectAddress) []ForeignKeyCheck {
	var checks []ForeignKeyCheck
	for _, descriptor := range view.Descriptors() {
		child, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		for _, f := range child.Constraints.ForeignKeys {
			if f.NotValid || child.Address() != address && f.Target != address {
				continue
			}
			checks = append(checks, ForeignKeyCheck{Child: child, Key: f})
		}
	}
	return checks
}

// ScanForeignKeys scans the existing rows of every check in order and
// returns the first violation or error. A parent or index missing from the
// candidate is ErrCatalogReplaced.
func ScanForeignKeys(ctx context.Context, v ForeignKeyScan, checks []ForeignKeyCheck) error {
	for _, check := range checks {
		if err := v.validate(ctx, check.Child, check.Key); err != nil {
			return err
		}
	}
	return nil
}

// ScanRestoredForeignKeys scans the keys Restore bound on child. Each
// definition's bound slot must still hold its bound key, else the stage was
// replaced; NOT VALID keys are not scanned.
func ScanRestoredForeignKeys(ctx context.Context, v ForeignKeyScan, child *catalog.Relation, definitions []RestoredForeignKey) error {
	for _, definition := range definitions {
		if definition.BoundSlot < 0 || definition.BoundSlot >= len(child.Constraints.ForeignKeys) || definition.BoundOID == 0 || child.Constraints.ForeignKeys[definition.BoundSlot].OID != definition.BoundOID {
			return ids.ErrCatalogReplaced
		}
		f := child.Constraints.ForeignKeys[definition.BoundSlot]
		if f.NotValid {
			continue
		}
		if err := v.validate(ctx, child, f); err != nil {
			return err
		}
	}
	return nil
}

func (v ForeignKeyScan) validate(ctx context.Context, child *catalog.Relation, f catalog.ForeignKeyConstraint) error {
	view := v.View.Catalog
	parent, ok := view.Relation(f.Target)
	if !ok {
		return ids.ErrCatalogReplaced
	}
	index, ok := view.Relation(f.Index)
	if !ok {
		return ids.ErrCatalogReplaced
	}
	comparisons, err := BindForeignKeyComparison(view, child, parent, index, f)
	if err != nil {
		return err
	}
	children, err := v.Capture(v.Domain.Ref(view.Epoch(), child.Address()), ids.ObjectAddress{})
	if err != nil {
		return err
	}
	parents, err := v.Capture(v.Domain.Ref(view.Epoch(), parent.Address()), f.Index)
	if err != nil {
		return err
	}
	return ValidateForeignKeyData(ctx, v.View, v.Env, child, parent, f, comparisons, children, parents, v.Input, v.Output)
}
