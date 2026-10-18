package engine

import (
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// Record the actual result of a named, qualified CREATE INDEX on a table freshly
// created by this import. No generated name, eligibility rebinding or index
// metadata is synthesized here; the ordinary provider already completed it.
func (e *Engine) capturePhysicalImportIndex(before CatalogRead, raw *parser.CreateIndex, created map[ids.ObjectAddress]ids.ObjectRef) *relationDescriptor {
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

func physicalImportedIndexMatches(source *relationDescriptor, constructor *physicalNotNullConstructor, current *relationDescriptor, primary *uniqueConstraint) bool {
	if source == nil || source.address != primary.Index || source.name != constructor.indexName || source.namespace != current.namespace || source.SQLIndex == nil || source.SQLIndex.Parent != current.address || source.SQLIndex.Constraint != nil || len(source.SQLIndex.Keys) != len(primary.Attributes) || len(source.Columns) < len(primary.Attributes) {
		return false
	}
	for i, attribute := range primary.Attributes {
		key, expression, err := indexCatalogKey(constructor.before, source.SQLIndex.Keys[i])
		slot := current.indexForAttribute(attribute)
		if err != nil || expression != nil || key != attribute || slot < 0 || source.Columns[i].Usage != current.Columns[slot].Usage {
			return false
		}
	}
	return true
}

// A keyless physical cleanup may be followed immediately by the captured,
// unrelated ordinary primary declaration. It cannot certify one of its own
// flag-only attributes as a primary key (or as an INCLUDE payload key).
func (e *Engine) prepareDeferredPhysicalPrimary(op *Operation, raw *parser.AlterTable, pair *restoredPhysicalNotNull, created map[ids.ObjectAddress]ids.ObjectRef) (*physicalNotNullConstructor, error) {
	if pair == nil || len(raw.Actions) != 1 || raw.Actions[0].Action != parser.AddUnique {
		return nil, nil
	}
	reject := func() (*physicalNotNullConstructor, error) {
		return nil, sqlError("0A000", restorePhysicalNotNullBoundary)
	}
	definition := raw.Actions[0].Unique
	if !raw.Only || raw.IfExists || len(raw.Name.Parts) != 2 || definition == nil || !definition.Primary || definition.NullsNotDistinct || definition.Name.Text == "" || len(definition.Columns) == 0 || pair.position != len(pair.repairs) || pair.primary != nil || pair.op != op {
		return reject()
	}
	auth, err := op.Authorization()
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !partitionConstraintReplayImport(e, op, auth) || auth.roleRecord != pair.authority.roleRecord || auth.catalog.Get(pair.ref.Address) != pair.current || created[pair.ref.Address] != pair.ref || pair.ref.Epoch != auth.catalog.Epoch() {
		return reject()
	}
	ref, err := e.resolveRelationReference(auth, raw.Name, false)
	if err != nil {
		return nil, err
	}
	old := pair.current
	if ref != pair.ref || old.RelationKind != tableRelation || old.Partition != nil || old.Membership != nil || inheritanceHasChildren(auth.catalog, old.address) || slices.ContainsFunc(old.UniqueConstraints, func(c uniqueConstraint) bool { return c.Primary }) {
		return reject()
	}
	for _, column := range definition.Columns {
		slot, exists := old.Index[column.Text]
		if !exists {
			return reject()
		}
		if _, physical := old.PhysicalNotNull[old.Attributes[slot]]; physical {
			return reject()
		}
	}
	return &physicalNotNullConstructor{before: old, ref: ref, deferred: definition}, nil
}

func validateDeferredPhysicalPrimary(constructor *physicalNotNullConstructor, current *relationDescriptor) error {
	reject := func() error { return sqlError("0A000", restorePhysicalNotNullBoundary) }
	definition := constructor.deferred
	var primary *uniqueConstraint
	for i := range current.UniqueConstraints {
		if current.UniqueConstraints[i].Primary == definition.Primary && current.UniqueConstraints[i].Name == definition.Name.Text {
			primary = &current.UniqueConstraints[i]
			break
		}
	}
	if primary == nil || primary.OID == 0 || len(primary.Attributes) != len(definition.Columns) {
		return reject()
	}
	for i, attribute := range primary.Attributes {
		slot, before := current.indexForAttribute(attribute), constructor.before.indexForAttribute(attribute)
		if slot < 0 || before < 0 || current.Columns[slot].Name != definition.Columns[i].Text || current.Columns[slot].Usage != constructor.before.Columns[before].Usage {
			return reject()
		}
	}
	for attribute, proof := range constructor.before.PhysicalNotNull {
		retained, exists := current.PhysicalNotNull[attribute]
		if !exists || retained.Cause != proof.Cause || retained.Usage != proof.Usage {
			return reject()
		}
		if constructor.competing {
			if retained.Support != proof.Support || retained.Retired != proof.Retired {
				return reject()
			}
		} else if physicalPrimaryForAttribute(current, attribute) != nil {
			return reject()
		}
	}
	return nil
}
