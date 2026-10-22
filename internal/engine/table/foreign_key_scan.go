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

// AddedForeignKeys are the valid keys of child whose OIDs are in added: the
// keys an ALTER TABLE introduced, in declaration order.
func AddedForeignKeys(child *catalog.Relation, added []ids.OID) []ForeignKeyCheck {
	var checks []ForeignKeyCheck
	for _, f := range child.Constraints.ForeignKeys {
		if f.NotValid || !slices.Contains(added, f.OID) {
			continue
		}
		checks = append(checks, ForeignKeyCheck{Child: child, Key: f})
	}
	return checks
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
