package engine

import (
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// CapturePhysicalImportIndex records the actual result of a named, qualified
// CREATE INDEX on a table this import freshly created: after holds the
// index, before (the read the statement started from) does not. No generated
// name, eligibility rebinding or index metadata is synthesized here; the
// ordinary provider already completed it. created maps the tables Restore
// created; domain is the catalog's.
func CapturePhysicalImportIndex(before, after catalog.Read, domain *ids.Domain, raw *parser.CreateIndex, created map[ids.ObjectAddress]ids.ObjectRef) *catalog.Relation {
	if raw.Name.Text == "" || raw.IfNotExists || raw.Concurrent.Character != 0 || len(raw.Table.Parts) != 2 {
		return nil
	}
	namespace := after.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: raw.Table.Parts[0].Text})
	if namespace == (ids.ObjectAddress{}) {
		return nil
	}
	key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: raw.Name.Text}
	address := after.NameAddress(key)
	if before.NameAddress(key) != (ids.ObjectAddress{}) || before.Get(address) != nil {
		return nil
	}
	index, ok := after.Relation(address)
	if !ok || index.SQLIndex == nil || index.SQLIndex.Constraint != nil {
		return nil
	}
	parent := after.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: raw.Table.Parts[1].Text})
	ref := created[parent]
	if index.SQLIndex.Parent != parent || ref.Address != parent || ref.Domain() != domain || ref.Epoch != after.Epoch() {
		return nil
	}
	return index
}

// DeferredPhysicalPrimaryShape reports whether raw, issued by op, is the
// ordinary primary key declaration that may follow pair's completed keyless
// cleanup; a different statement is not handled, a malformed one rejected.
func DeferredPhysicalPrimaryShape(op *session.Operation, raw *parser.AlterTable, pair *RestoredPhysicalNotNull) (bool, error) {
	if pair == nil || len(raw.Actions) != 1 || raw.Actions[0].Action != parser.AddUnique {
		return false, nil
	}
	definition := raw.Actions[0].Unique
	if !raw.Only || raw.IfExists || len(raw.Name.Parts) != 2 || definition == nil || !definition.Primary || definition.NullsNotDistinct || definition.Name.Text == "" || len(definition.Columns) == 0 || pair.Position != len(pair.Repairs) || pair.Primary != nil || pair.Op != op {
		return false, sqlError("0A000", table.RestorePhysicalNotNullBoundary)
	}
	return true, nil
}

// PrepareDeferredPhysicalPrimary returns the deferred constructor of raw's
// primary key on ref, pair's relation. A keyless physical cleanup may be
// followed immediately by the captured, unrelated ordinary primary
// declaration; it cannot certify one of its own flag-only attributes as a
// primary key (or as an INCLUDE payload key).
func PrepareDeferredPhysicalPrimary(view catalog.Read, ref ids.ObjectRef, raw *parser.AlterTable, pair *RestoredPhysicalNotNull) (*table.PhysicalNotNullConstructor, error) {
	reject := func() (*table.PhysicalNotNullConstructor, error) {
		return nil, sqlError("0A000", table.RestorePhysicalNotNullBoundary)
	}
	definition := raw.Actions[0].Unique
	old := pair.Current
	if ref != pair.Ref || old.RelationKind != catalog.TableRelation || old.Partition != nil || old.Membership != nil || catalog.InheritanceHasChildren(view, old.Address()) || slices.ContainsFunc(old.Constraints.Unique, func(c catalog.UniqueConstraint) bool { return c.Primary }) {
		return reject()
	}
	for _, column := range definition.Columns {
		slot, exists := old.Index[column.Text]
		if !exists {
			return reject()
		}
		if _, physical := old.Constraints.PhysicalNotNull[old.Attributes[slot]]; physical {
			return reject()
		}
	}
	return &table.PhysicalNotNullConstructor{Before: old, Ref: ref, Deferred: definition}, nil
}
