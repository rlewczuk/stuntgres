package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// A valid FK to an ordinary descendant cannot live in its ancestor's CREATE:
// the referenced relation must first inherit that declaration. Replay the real
// constraint after both physical row streams and its actual key provider exist.
func inheritedForeignKeyDeferred(view CatalogRead, source *relationDescriptor, foreign *foreignKeyConstraint) bool {
	if foreign.NotValid || source.relationKind != tableRelation || source.partition != nil || source.membership != nil || foreign.Target == source.address {
		return false
	}
	target, ok := view.Relation(foreign.Target)
	return ok && target.relationKind == tableRelation && target.partition == nil && target.membership == nil && inheritedForeignKeyDescendant(view, target, source.address)
}

func inheritedForeignKeyDescendant(view CatalogRead, target *relationDescriptor, ancestor ids.ObjectAddress) bool {
	if target.inheritance == nil {
		return false
	}
	for _, parent := range target.inheritance.parents {
		if parent == ancestor {
			return true
		}
	}
	visited := make(map[ids.ObjectAddress]bool)
	var walk func(*relationDescriptor) bool
	walk = func(relation *relationDescriptor) bool {
		if visited[relation.address] || relation.inheritance == nil {
			return false
		}
		visited[relation.address] = true
		for _, address := range relation.inheritance.parents {
			if address == ancestor {
				return true
			}
			if parent, ok := view.Relation(address); ok && walk(parent) {
				return true
			}
		}
		return false
	}
	return walk(target)
}

func inheritedForeignKeyEvents(view CatalogRead, graph map[dumpEventKey][]dumpEventKey, tables []dumpRelation, nameReady func(ids.ObjectAddress) (dumpEventKey, bool, error)) error {
	for i := range tables {
		source := tables[i].descriptor
		event := dumpEventKey{oid: source.address.ObjectID, phase: dumpInheritedForeignKeys}
		for i := range source.foreignKeys {
			foreign := &source.foreignKeys[i]
			if !inheritedForeignKeyDeferred(view, source, foreign) {
				continue
			}
			provider, present, err := nameReady(foreign.Index)
			if err != nil {
				return err
			}
			if !present {
				return fmt.Errorf("stuntgres: missing inherited foreign-key replay provider")
			}
			addDumpDependency(graph, event, dumpEventKey{oid: source.address.ObjectID, phase: dumpRows})
			addDumpDependency(graph, event, dumpEventKey{oid: foreign.Target.ObjectID, phase: dumpRows})
			addDumpDependency(graph, event, dumpEventKey{oid: foreign.Target.ObjectID, phase: dumpTableCompletion})
			addDumpDependency(graph, event, provider)
			addDumpDependency(graph, dumpEventKey{oid: source.address.ObjectID, phase: dumpTableCompletion}, event)
		}
	}
	return nil
}

func inheritedForeignKeyPrimary(target *relationDescriptor, foreign *foreignKeyConstraint) bool {
	for _, key := range target.uniqueConstraints {
		if key.Primary && key.Index == foreign.Index && slices.Equal(key.Attributes, foreign.Referenced) {
			return true
		}
	}
	return false
}

func (table *dumpRelation) emitInheritedForeignKeys(view CatalogRead, emit func(string) error) error {
	qualified := quoteIdentifier(table.schema) + "." + quoteIdentifier(table.name)
	for i := range table.descriptor.foreignKeys {
		foreign := &table.descriptor.foreignKeys[i]
		if !inheritedForeignKeyDeferred(view, table.descriptor, foreign) {
			continue
		}
		target := view.MustRelation(foreign.Target)
		text, err := (pgRenderer{view: view}).foreignKeyConstraintSQLWithPrimary(table.descriptor, *foreign, inheritedForeignKeyPrimary(target, foreign))
		if err != nil {
			return err
		}
		if err := emit("ALTER TABLE ONLY " + qualified + " ADD CONSTRAINT " + quoteIdentifier(foreign.Name) + " " + text + ";\n"); err != nil {
			return err
		}
	}
	return nil
}

const restoreInheritedForeignKeyBoundary = "restore accepts valid foreign-key ALTER only for freshly imported ordinary ancestor/descendant relations"

type restoredInheritedForeignColumn struct {
	name      string
	attribute int32
	usage     sqltype.Usage
}

type restoredInheritedForeignIndex struct {
	current  *relationDescriptor
	eligible bool
}

type restoredInheritedForeignKey struct {
	source, target      ids.ObjectRef
	beforeSource        *relationDescriptor
	beforeTarget        *relationDescriptor
	raw                 parser.ForeignKeyConstraint
	columns, referenced []restoredInheritedForeignColumn
	deleted             []restoredInheritedForeignColumn
	providers           map[ids.ObjectAddress]restoredInheritedForeignIndex
}

func captureInheritedForeignColumns(table *relationDescriptor, names []parser.Identifier) ([]restoredInheritedForeignColumn, bool) {
	columns := make([]restoredInheritedForeignColumn, len(names))
	for i, name := range names {
		slot, exists := table.index[name.Text]
		if !exists {
			return nil, false
		}
		for _, previous := range columns[:i] {
			if previous.attribute == table.attributes[slot] {
				return nil, false
			}
		}
		columns[i] = restoredInheritedForeignColumn{name: name.Text, attribute: table.attributes[slot], usage: table.columns[slot].Usage}
	}
	return columns, true
}

// This is not a general valid ALTER whitelist. Only a successful isolated
// import of both actual relations, with a strict ordinary inheritance edge,
// can issue a receipt. The incumbent pending binder remains the sole FK binder.
func (e *Engine) captureInheritedForeignKey(op *Operation, raw *parser.AlterTable, created map[ids.ObjectAddress]ids.ObjectRef) (restoredForeignKeys, bool, error) {
	if len(raw.Actions) != 1 || raw.Actions[0].Action != parser.AddForeignKey || raw.Actions[0].ForeignKey == nil || raw.Actions[0].ForeignKey.NotValid {
		return restoredForeignKeys{}, false, nil
	}
	reject := func() (restoredForeignKeys, bool, error) {
		return restoredForeignKeys{}, false, sqlError("0A000", restoreInheritedForeignKeyBoundary)
	}
	foreign := raw.Actions[0].ForeignKey
	if !raw.Only || raw.IfExists || len(raw.Name.Parts) != 2 || len(foreign.Target.Parts) != 2 || foreign.Name.Text == "" || len(foreign.Columns) == 0 {
		return reject()
	}
	if raw.Name.Parts[0].Text == foreign.Target.Parts[0].Text && raw.Name.Parts[1].Text == foreign.Target.Parts[1].Text {
		return restoredForeignKeys{}, false, nil
	}
	pending, err := e.captureRestoredForeignKeys(op, []parser.ForeignKeyConstraint{*foreign})
	if err != nil {
		return restoredForeignKeys{}, false, err
	}
	auth := pending.auth
	e.mu.Lock()
	defer e.mu.Unlock()
	if !partitionConstraintReplayImport(e, op, auth) {
		return reject()
	}
	sourceRef, err := e.resolveRelationReference(auth, raw.Name, false)
	if err != nil {
		return restoredForeignKeys{}, false, err
	}
	targetRef, err := e.resolveRelationReference(auth, foreign.Target, false)
	if err != nil {
		return restoredForeignKeys{}, false, err
	}
	source, sourceOK := auth.catalog.Relation(sourceRef.Address)
	target, targetOK := auth.catalog.Relation(targetRef.Address)
	if !sourceOK || !targetOK || created[sourceRef.Address] != sourceRef || created[targetRef.Address] != targetRef || source.relationKind != tableRelation || target.relationKind != tableRelation || source.partition != nil || source.membership != nil || target.partition != nil || target.membership != nil || sourceRef == targetRef || !inheritedForeignKeyDescendant(auth.catalog, target, source.address) {
		return reject()
	}
	if auth.catalog.MustNamespace(source.namespace).temporary != (ids.SessionToken{}) || auth.catalog.MustNamespace(target.namespace).temporary != (ids.SessionToken{}) {
		return reject()
	}
	if err := auth.Check(AccessRequirement{Object: sourceRef, OwnerOnly: true}); err != nil {
		return restoredForeignKeys{}, false, err
	}
	receipt := &restoredInheritedForeignKey{source: sourceRef, target: targetRef, beforeSource: source, beforeTarget: target, raw: *foreign, providers: make(map[ids.ObjectAddress]restoredInheritedForeignIndex)}
	var valid bool
	receipt.columns, valid = captureInheritedForeignColumns(source, foreign.Columns)
	if !valid {
		return reject()
	}
	receipt.deleted, valid = captureInheritedForeignColumns(source, foreign.DeleteColumns)
	if !valid {
		return reject()
	}
	referenced := foreign.Referenced
	if len(referenced) == 0 {
		for _, key := range target.uniqueConstraints {
			if key.Primary {
				pending.definitions[0].supporting = key.Index
				referenced = make([]parser.Identifier, len(key.Attributes))
				for i, attribute := range key.Attributes {
					slot := target.indexForAttribute(attribute)
					if slot < 0 {
						return reject()
					}
					referenced[i] = parser.Identifier{Text: target.columns[slot].Name}
				}
				break
			}
		}
	}
	receipt.referenced, valid = captureInheritedForeignColumns(target, referenced)
	if !valid || len(receipt.referenced) != len(receipt.columns) {
		return reject()
	}
	attributes := make([]int32, len(receipt.referenced))
	for i, column := range receipt.referenced {
		attributes[i] = column.attribute
	}
	for address, descriptor := range auth.catalog.Descriptors() {
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.sqlIndex == nil || index.sqlIndex.parent != target.address {
			continue
		}
		eligible, err := foreignKeyIndexEligible(target, index, attributes)
		if err != nil {
			return restoredForeignKeys{}, false, err
		}
		receipt.providers[address] = restoredInheritedForeignIndex{current: index, eligible: eligible}
	}
	pending.child = sourceRef
	pending.definitions[0].target = targetRef.Address
	pending.definitions[0].path = []ids.ObjectAddress{target.namespace}
	pending.definitions[0].inherited = receipt
	return pending, true, nil
}

func inheritedForeignColumnsUnchanged(table *relationDescriptor, columns []restoredInheritedForeignColumn) bool {
	for _, column := range columns {
		slot, exists := table.index[column.name]
		if !exists || table.attributes[slot] != column.attribute || table.columns[slot].Usage != column.usage {
			return false
		}
	}
	return true
}

func inheritedForeignRelationUnchanged(current, before *relationDescriptor) bool {
	return current != nil && current.address == before.address && current.namespace == before.namespace && current.name == before.name && current.owner == before.owner && current.ofType == before.ofType && current.relationKind == tableRelation && current.partition == nil && current.membership == nil
}

// Comments and final attribute-rank repairs do not alter these key/provider
// observations. No later key, owner, type or provider incarnation can reuse them.
func (receipt *restoredInheritedForeignKey) validate(e *Engine, op *Operation, declaration restoredForeignKeys, view CatalogRead) error {
	auth := declaration.auth
	auth.catalog = view
	auth.recaptureCatalog(view)
	if receipt.source != declaration.child || receipt.source.Domain() != e.catalog.domain || receipt.target.Domain() != e.catalog.domain || receipt.source.Epoch != view.Epoch() || receipt.target.Epoch != view.Epoch() || declaration.auth.roleRecord != e.roles || !partitionConstraintReplayImport(e, op, auth) {
		return ErrCatalogReplaced
	}
	source, _ := view.Relation(receipt.source.Address)
	target, _ := view.Relation(receipt.target.Address)
	if !inheritedForeignRelationUnchanged(source, receipt.beforeSource) || !inheritedForeignRelationUnchanged(target, receipt.beforeTarget) || !inheritedForeignKeyDescendant(view, target, source.address) || !inheritedForeignColumnsUnchanged(source, receipt.columns) || !inheritedForeignColumnsUnchanged(source, receipt.deleted) || !inheritedForeignColumnsUnchanged(target, receipt.referenced) {
		return ErrCatalogReplaced
	}
	seen := 0
	for address, descriptor := range view.Descriptors() {
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.sqlIndex == nil || index.sqlIndex.parent != target.address {
			continue
		}
		observed, exists := receipt.providers[address]
		if !exists || index.sqlIndex != observed.current.sqlIndex || index.owner != observed.current.owner || index.namespace != observed.current.namespace || index.name != observed.current.name || len(index.columns) != len(observed.current.columns) {
			return ErrCatalogReplaced
		}
		for i, column := range index.columns {
			if column.Usage != observed.current.columns[i].Usage {
				return ErrCatalogReplaced
			}
		}
		seen++
	}
	if seen != len(receipt.providers) {
		return ErrCatalogReplaced
	}
	return auth.Check(AccessRequirement{Object: receipt.source, OwnerOnly: true})
}

func (receipt *restoredInheritedForeignKey) accepts(foreign *foreignKeyConstraint) bool {
	if foreign.Name != receipt.raw.Name.Text || foreign.Target != receipt.target.Address || foreign.NotValid || foreign.Full != receipt.raw.Full || foreign.UpdateAction != receipt.raw.UpdateAction || foreign.DeleteAction != receipt.raw.DeleteAction || len(foreign.Attributes) != len(receipt.columns) || len(foreign.Referenced) != len(receipt.referenced) || len(foreign.DeleteAttributes) != len(receipt.deleted) || !receipt.providers[foreign.Index].eligible {
		return false
	}
	for i, column := range receipt.columns {
		if foreign.Attributes[i] != column.attribute {
			return false
		}
	}
	for i, column := range receipt.referenced {
		if foreign.Referenced[i] != column.attribute {
			return false
		}
	}
	for i, column := range receipt.deleted {
		if foreign.DeleteAttributes[i] != column.attribute {
			return false
		}
	}
	return true
}
