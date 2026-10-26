package table

import (
	"iter"
	"slices"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// InstalledForeignKey reports whether the relation at address in base
// already has a key equal to f, so a staged copy of it needs no provider.
func InstalledForeignKey(base catalog.Read, address ids.ObjectAddress, f catalog.ForeignKeyConstraint) bool {
	parent, ok := base.Relation(address)
	if !ok {
		return false
	}
	for _, old := range parent.Constraints.ForeignKeys {
		if old.Name == f.Name && old.Target == f.Target && old.Full == f.Full && (old.NotValid == f.NotValid || old.NotValid && !f.NotValid && old.OID != 0 && old.OID == f.OID && old.Index == f.Index) && old.UpdateAction == f.UpdateAction && old.DeleteAction == f.DeleteAction && slices.Equal(old.DeleteAttributes, f.DeleteAttributes) && slices.Equal(old.Attributes, f.Attributes) && slices.Equal(old.Referenced, f.Referenced) {
			return true
		}
	}
	return false
}

// AlterForeignKeyIndex is the parent index an ADD FOREIGN KEY of ALTER TABLE
// captures before its waits: the unique index over the referenced columns
// (the primary key's when raw names none) of the ordinary table at target,
// or zero when there is none yet.
func AlterForeignKeyIndex(view catalog.Read, target ids.ObjectAddress, raw parser.ForeignKeyConstraint) ids.ObjectAddress {
	parent, ok := view.Relation(target)
	if !ok || parent.RelationKind != catalog.TableRelation {
		return ids.ObjectAddress{}
	}
	var attributes []int32
	if len(raw.Referenced) == 0 {
		for _, key := range parent.Constraints.Unique {
			if key.Primary {
				attributes = key.Attributes
				break
			}
		}
	} else {
		attributes, _ = ForeignKeyAttributes(parent, raw.Referenced)
	}
	if len(attributes) != 0 {
		if index, err := ForeignKeyIndex(view, parent, attributes); err == nil {
			return index.Address()
		}
	}
	return ids.ObjectAddress{}
}

// ForeignKeyTypeProviders are the tables of every key of view that the
// relation at address declares or is referenced by, in domain at view's
// epoch, ordered by OID. A column type change locks them before it rebinds.
func ForeignKeyTypeProviders(view catalog.Read, domain *ids.Domain, address ids.ObjectAddress) []ids.ObjectRef {
	var refs []ids.ObjectRef
	for _, descriptor := range view.Descriptors() {
		child, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		for _, f := range child.Constraints.ForeignKeys {
			if child.Address() != address && f.Target != address {
				continue
			}
			for _, a := range []ids.ObjectAddress{child.Address(), f.Target} {
				ref := domain.Ref(view.Epoch(), a)
				if !slices.Contains(refs, ref) {
					refs = append(refs, ref)
				}
			}
		}
	}
	sortRefs(refs)
	return refs
}

// ForeignKeyDefinitionProviders are the existing parents of base that the
// new keys of the staged descriptors reference, in domain at base's epoch,
// ordered by OID. Installed keys, self references and parents created by
// the same stage need no provider.
func ForeignKeyDefinitionProviders(base catalog.Read, domain *ids.Domain, staged iter.Seq2[ids.ObjectAddress, catalog.Descriptor]) []ids.ObjectRef {
	var refs []ids.ObjectRef
	for address, descriptor := range staged {
		child, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		for _, f := range child.Constraints.ForeignKeys {
			if !definitionProvider(base, address, f) {
				continue
			}
			ref := domain.Ref(base.Epoch(), f.Target)
			if !slices.Contains(refs, ref) {
				refs = append(refs, ref)
			}
		}
	}
	sortRefs(refs)
	return refs
}

// CheckForeignKeyDefinitionProviders rechecks every provider key after its
// grant against view's current catalog: the parent and its index still
// exist, the index still supports the key, the comparison still binds, and
// the authority (view and env) still has REFERENCES on the columns.
func CheckForeignKeyDefinitionProviders(view access.View, env access.Environment, base catalog.Read, staged iter.Seq2[ids.ObjectAddress, catalog.Descriptor]) error {
	for address, descriptor := range staged {
		child, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		for _, f := range child.Constraints.ForeignKeys {
			if !definitionProvider(base, address, f) {
				continue
			}
			parent, ok := view.Catalog.Relation(f.Target)
			if !ok {
				return ids.ErrCatalogReplaced
			}
			index, ok := view.Catalog.Relation(f.Index)
			if !ok {
				return ids.ErrCatalogReplaced
			}
			eligible, err := ForeignKeyIndexEligible(parent, index, f.Referenced)
			if err != nil {
				return err
			}
			if !eligible {
				return ids.ErrCatalogReplaced
			}
			if _, err := BindForeignKeyComparison(view.Catalog, child, parent, index, f); err != nil {
				return err
			}
			if err := ForeignKeyReferencesAllowed(view, env, parent, f.Referenced); err != nil {
				return err
			}
		}
	}
	return nil
}

// definitionProvider reports whether f of the staged relation at address
// is a new key on an existing parent other than the relation itself.
func definitionProvider(base catalog.Read, address ids.ObjectAddress, f catalog.ForeignKeyConstraint) bool {
	if InstalledForeignKey(base, address, f) {
		return false
	}
	if f.Target == address {
		return false
	}
	_, external := base.Find(f.Target)
	return external
}

func sortRefs(refs []ids.ObjectRef) {
	slices.SortFunc(refs, func(a, b ids.ObjectRef) int {
		if a.Address.ObjectID < b.Address.ObjectID {
			return -1
		}
		if a.Address.ObjectID > b.Address.ObjectID {
			return 1
		}
		return 0
	})
}
