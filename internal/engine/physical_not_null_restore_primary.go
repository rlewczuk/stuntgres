package engine

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// Record the actual result of a named, qualified CREATE INDEX on a table freshly
// created by this import. No generated name, eligibility rebinding or index
// metadata is synthesized here; the ordinary provider already completed it.
func (e *Engine) capturePhysicalImportIndex(before catalog.Read, raw *parser.CreateIndex, created map[ids.ObjectAddress]ids.ObjectRef) *catalog.Relation {
	if raw.Name.Text == "" || raw.IfNotExists || raw.Concurrent.Character != 0 || len(raw.Table.Parts) != 2 {
		return nil
	}
	after := e.CatalogRead()
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
	if index.SQLIndex.Parent != parent || ref.Address != parent || ref.Domain() != e.catalog.Domain || ref.Epoch != after.Epoch() {
		return nil
	}
	return index
}

// A keyless physical cleanup may be followed immediately by the captured,
// unrelated ordinary primary declaration. It cannot certify one of its own
// flag-only attributes as a primary key (or as an INCLUDE payload key).
func (e *Engine) prepareDeferredPhysicalPrimary(op *session.Operation, raw *parser.AlterTable, pair *restoredPhysicalNotNull, created map[ids.ObjectAddress]ids.ObjectRef) (*physicalNotNullConstructor, error) {
	if pair == nil || len(raw.Actions) != 1 || raw.Actions[0].Action != parser.AddUnique {
		return nil, nil
	}
	reject := func() (*physicalNotNullConstructor, error) {
		return nil, sqlError("0A000", RestorePhysicalNotNullBoundary)
	}
	definition := raw.Actions[0].Unique
	if !raw.Only || raw.IfExists || len(raw.Name.Parts) != 2 || definition == nil || !definition.Primary || definition.NullsNotDistinct || definition.Name.Text == "" || len(definition.Columns) == 0 || pair.position != len(pair.repairs) || pair.primary != nil || pair.op != op {
		return reject()
	}
	auth, err := operationAuthorization(op)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !partitionConstraintReplayImport(e, op, auth) || auth.RoleRecord != pair.authority.RoleRecord || auth.Catalog.Get(pair.ref.Address) != pair.current || created[pair.ref.Address] != pair.ref || pair.ref.Epoch != auth.Catalog.Epoch() {
		return reject()
	}
	ref, err := e.resolveRelationReference(auth, raw.Name, false)
	if err != nil {
		return nil, err
	}
	old := pair.current
	if ref != pair.ref || old.RelationKind != catalog.TableRelation || old.Partition != nil || old.Membership != nil || catalog.InheritanceHasChildren(auth.Catalog, old.Address()) || slices.ContainsFunc(old.Constraints.Unique, func(c catalog.UniqueConstraint) bool { return c.Primary }) {
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
	return &physicalNotNullConstructor{Before: old, Ref: ref, Deferred: definition}, nil
}
