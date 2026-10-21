package engine

import (
	"slices"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// RestoredPhysicalForeignKeyProvider is the short-lived readiness receipt of
// a completed physical constructor or cleanup whose relation has a genuine
// primary key: a following self-referencing foreign key may use that key.
// It never creates a key or grants foreign-key privileges.
type RestoredPhysicalForeignKeyProvider struct {
	Op         *session.Operation
	RoleRecord *access.RoleRecord
	Ref        ids.ObjectRef
	Current    *catalog.Relation
	Primary    *catalog.PhysicalPrimarySupport
	// OwnerDirective is set once the one owner directive before the foreign
	// key has been admitted.
	OwnerDirective bool
	// Permuted is set once a foreign key referenced the key's columns in
	// another order; only then may a competing unique key follow.
	Permuted bool
}

// Holds reports whether view, read under roles, still holds the relation
// the receipt recorded.
func (provider *RestoredPhysicalForeignKeyProvider) Holds(roles *access.RoleRecord, view catalog.Read) bool {
	return roles == provider.RoleRecord && view.Get(provider.Ref.Address) == provider.Current
}

// CapturePhysicalForeignKeyProvider issues the receipt for ref, a relation
// this Restore created whose physical NOT NULL flags all come from this
// import; nil when it has no flags or no primary key.
func CapturePhysicalForeignKeyProvider(op *session.Operation, roles *access.RoleRecord, view catalog.Read, ref ids.ObjectRef, created map[ids.ObjectAddress]ids.ObjectRef) (*RestoredPhysicalForeignKeyProvider, error) {
	old, ok := view.Relation(ref.Address)
	if !ok || created[ref.Address] != ref || len(old.Constraints.PhysicalNotNull) == 0 || old.Partition != nil || old.Membership != nil {
		return nil, nil
	}
	for attribute, proof := range old.Constraints.PhysicalNotNull {
		if !index.PhysicalNotNullOriginValid(view, old, attribute, proof) || proof.Cause.Origin != ref || proof.Cause.Kind != catalog.PhysicalNotNullImportCleanup && proof.Cause.Kind != catalog.PhysicalNotNullImportFlag {
			return nil, sqlError("0A000", table.RestorePhysicalNotNullBoundary)
		}
	}
	for i := range old.Constraints.Unique {
		primary := &old.Constraints.Unique[i]
		if !primary.Primary {
			continue
		}
		support := catalog.CapturePhysicalPrimarySupport(old, primary)
		if !index.PhysicalPrimarySupportValid(view, old, primary, support) {
			return nil, sqlError("0A000", table.RestorePhysicalNotNullBoundary)
		}
		return &RestoredPhysicalForeignKeyProvider{Op: op, RoleRecord: roles, Ref: ref, Current: old, Primary: support}, nil
	}
	return nil, nil
}

// PhysicalSelfForeignKeyShape reports whether raw, issued by op, is the
// named, valid, self-referencing foreign key on provider's key that may
// follow the receipt; a different statement is not handled, a malformed
// one rejected.
func PhysicalSelfForeignKeyShape(op *session.Operation, raw *parser.AlterTable, provider *RestoredPhysicalForeignKeyProvider) (bool, error) {
	if len(raw.Actions) != 1 || raw.Actions[0].Action != parser.AddForeignKey || raw.Actions[0].ForeignKey == nil || raw.Actions[0].ForeignKey.NotValid {
		return false, nil
	}
	foreign := raw.Actions[0].ForeignKey
	if provider == nil || provider.Op != op || !raw.Only || raw.IfExists || len(raw.Name.Parts) != 2 || len(foreign.Target.Parts) != 2 || foreign.Name.Text == "" || len(foreign.Columns) != len(provider.Primary.Keys) {
		return false, sqlError("0A000", table.RestorePhysicalNotNullBoundary)
	}
	for i := range raw.Name.Parts {
		if raw.Name.Parts[i].Text != foreign.Target.Parts[i].Text {
			return false, sqlError("0A000", table.RestorePhysicalNotNullBoundary)
		}
	}
	return true, nil
}

// PhysicalSelfForeignKeyColumns checks the referenced columns of raw's
// foreign key against provider's relation in view. An explicit list must
// permute the primary key's columns, and no other index may be eligible for
// it; the receipt then records the permutation.
func PhysicalSelfForeignKeyColumns(view catalog.Read, raw *parser.AlterTable, provider *RestoredPhysicalForeignKeyProvider) error {
	reject := func() error { return sqlError("0A000", table.RestorePhysicalNotNullBoundary) }
	foreign := raw.Actions[0].ForeignKey
	old := provider.Current
	namespace := view.Get(old.Namespace())
	if namespace == nil || namespace.Name() != raw.Name.Parts[0].Text || old.Name() != raw.Name.Parts[1].Text {
		return reject()
	}
	if len(foreign.Referenced) == 0 {
		return nil
	}
	if len(foreign.Referenced) != len(provider.Primary.Keys) {
		return reject()
	}
	permuted := false
	for i, column := range foreign.Referenced {
		slot, exists := old.Index[column.Text]
		if !exists {
			return reject()
		}
		attribute := old.Attributes[slot]
		if !slices.ContainsFunc(provider.Primary.Keys, func(key catalog.PhysicalPrimaryKeyUsage) bool { return key.Attribute == attribute }) {
			return reject()
		}
		for _, prior := range foreign.Referenced[:i] {
			if prior.Text == column.Text {
				return reject()
			}
		}
		permuted = permuted || attribute != provider.Primary.Keys[i].Attribute
	}
	if !permuted {
		return reject()
	}
	var keyBuffer [32]int32
	attributes := keyBuffer[:len(provider.Primary.Keys)]
	for i, key := range provider.Primary.Keys {
		attributes[i] = key.Attribute
	}
	for _, descriptor := range view.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.Address() == provider.Primary.Index {
			continue
		}
		eligible, eligibilityErr := table.ForeignKeyIndexEligible(old, index, attributes)
		if eligibilityErr != nil {
			return eligibilityErr
		}
		if eligible {
			return reject()
		}
	}
	provider.Permuted = true
	return nil
}

// PinPhysicalSelfForeignKey pins the one pending self-referencing foreign
// key to provider's genuine current primary key; its target must resolve
// to provider's relation.
func PinPhysicalSelfForeignKey(pending *table.RestoredForeignKeys, provider *RestoredPhysicalForeignKeyProvider) error {
	if pending.Definitions[0].Target != provider.Ref.Address {
		return sqlError("0A000", table.RestorePhysicalNotNullBoundary)
	}
	pending.Definitions[0].Supporting = provider.Primary.Index
	return nil
}
