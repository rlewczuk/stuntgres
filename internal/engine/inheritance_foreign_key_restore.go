package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// A valid FK to an ordinary descendant cannot live in its ancestor's CREATE:
// the referenced relation must first inherit that declaration. Replay the real
// constraint after both physical row streams and its actual key provider exist.
func inheritedForeignKeyDeferred(view catalog.Read, source *catalog.Relation, foreign *catalog.ForeignKeyConstraint) bool {
	if foreign.NotValid || source.RelationKind != catalog.TableRelation || source.Partition != nil || source.Membership != nil || foreign.Target == source.Address() {
		return false
	}
	target, ok := view.Relation(foreign.Target)
	return ok && target.RelationKind == catalog.TableRelation && target.Partition == nil && target.Membership == nil && inheritedForeignKeyDescendant(view, target, source.Address())
}

func inheritedForeignKeyDescendant(view catalog.Read, target *catalog.Relation, ancestor ids.ObjectAddress) bool {
	if target.Inheritance == nil {
		return false
	}
	for _, parent := range target.Inheritance.Parents {
		if parent == ancestor {
			return true
		}
	}
	visited := make(map[ids.ObjectAddress]bool)
	var walk func(*catalog.Relation) bool
	walk = func(relation *catalog.Relation) bool {
		if visited[relation.Address()] || relation.Inheritance == nil {
			return false
		}
		visited[relation.Address()] = true
		for _, address := range relation.Inheritance.Parents {
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

func inheritedForeignKeyEvents(view catalog.Read, graph map[dumpEventKey][]dumpEventKey, tables []dumpRelation, nameReady func(ids.ObjectAddress) (dumpEventKey, bool, error)) error {
	for i := range tables {
		source := tables[i].descriptor
		event := dumpEventKey{oid: source.Address().ObjectID, phase: dumpInheritedForeignKeys}
		for i := range source.Constraints.ForeignKeys {
			foreign := &source.Constraints.ForeignKeys[i]
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
			addDumpDependency(graph, event, dumpEventKey{oid: source.Address().ObjectID, phase: dumpRows})
			addDumpDependency(graph, event, dumpEventKey{oid: foreign.Target.ObjectID, phase: dumpRows})
			addDumpDependency(graph, event, dumpEventKey{oid: foreign.Target.ObjectID, phase: dumpTableCompletion})
			addDumpDependency(graph, event, provider)
			addDumpDependency(graph, dumpEventKey{oid: source.Address().ObjectID, phase: dumpTableCompletion}, event)
		}
	}
	return nil
}

func inheritedForeignKeyPrimary(target *catalog.Relation, foreign *catalog.ForeignKeyConstraint) bool {
	for _, key := range target.Constraints.Unique {
		if key.Primary && key.Index == foreign.Index && slices.Equal(key.Attributes, foreign.Referenced) {
			return true
		}
	}
	return false
}

func (table *dumpRelation) emitInheritedForeignKeys(view catalog.Read, emit func(string) error) error {
	qualified := catalog.QuoteIdentifier(table.schema) + "." + catalog.QuoteIdentifier(table.name)
	for i := range table.descriptor.Constraints.ForeignKeys {
		foreign := &table.descriptor.Constraints.ForeignKeys[i]
		if !inheritedForeignKeyDeferred(view, table.descriptor, foreign) {
			continue
		}
		target := view.MustRelation(foreign.Target)
		text, err := (pgRenderer{view: view}).foreignKeyConstraintSQLWithPrimary(table.descriptor, *foreign, inheritedForeignKeyPrimary(target, foreign))
		if err != nil {
			return err
		}
		if err := emit("ALTER TABLE ONLY " + qualified + " ADD CONSTRAINT " + catalog.QuoteIdentifier(foreign.Name) + " " + text + ";\n"); err != nil {
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
	current  *catalog.Relation
	eligible bool
}

type restoredInheritedForeignKey struct {
	source, target      ids.ObjectRef
	beforeSource        *catalog.Relation
	beforeTarget        *catalog.Relation
	raw                 parser.ForeignKeyConstraint
	columns, referenced []restoredInheritedForeignColumn
	deleted             []restoredInheritedForeignColumn
	providers           map[ids.ObjectAddress]restoredInheritedForeignIndex
}

func captureInheritedForeignColumns(table *catalog.Relation, names []parser.Identifier) ([]restoredInheritedForeignColumn, bool) {
	columns := make([]restoredInheritedForeignColumn, len(names))
	for i, name := range names {
		slot, exists := table.Index[name.Text]
		if !exists {
			return nil, false
		}
		for _, previous := range columns[:i] {
			if previous.attribute == table.Attributes[slot] {
				return nil, false
			}
		}
		columns[i] = restoredInheritedForeignColumn{name: name.Text, attribute: table.Attributes[slot], usage: table.Columns[slot].Usage}
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
	source, sourceOK := auth.Catalog.Relation(sourceRef.Address)
	target, targetOK := auth.Catalog.Relation(targetRef.Address)
	if !sourceOK || !targetOK || created[sourceRef.Address] != sourceRef || created[targetRef.Address] != targetRef || source.RelationKind != catalog.TableRelation || target.RelationKind != catalog.TableRelation || source.Partition != nil || source.Membership != nil || target.Partition != nil || target.Membership != nil || sourceRef == targetRef || !inheritedForeignKeyDescendant(auth.Catalog, target, source.Address()) {
		return reject()
	}
	if auth.Catalog.MustNamespace(source.Namespace()).Temporary != (ids.SessionToken{}) || auth.Catalog.MustNamespace(target.Namespace()).Temporary != (ids.SessionToken{}) {
		return reject()
	}
	if err := auth.Check(access.Requirement{Object: sourceRef, OwnerOnly: true}); err != nil {
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
		for _, key := range target.Constraints.Unique {
			if key.Primary {
				pending.definitions[0].supporting = key.Index
				referenced = make([]parser.Identifier, len(key.Attributes))
				for i, attribute := range key.Attributes {
					slot := target.IndexForAttribute(attribute)
					if slot < 0 {
						return reject()
					}
					referenced[i] = parser.Identifier{Text: target.Columns[slot].Name}
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
	for address, descriptor := range auth.Catalog.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != target.Address() {
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
	pending.definitions[0].path = []ids.ObjectAddress{target.Namespace()}
	pending.definitions[0].inherited = receipt
	return pending, true, nil
}

func inheritedForeignColumnsUnchanged(table *catalog.Relation, columns []restoredInheritedForeignColumn) bool {
	for _, column := range columns {
		slot, exists := table.Index[column.name]
		if !exists || table.Attributes[slot] != column.attribute || table.Columns[slot].Usage != column.usage {
			return false
		}
	}
	return true
}

func inheritedForeignRelationUnchanged(current, before *catalog.Relation) bool {
	return current != nil && current.Address() == before.Address() && current.Namespace() == before.Namespace() && current.Name() == before.Name() && current.Owner == before.Owner && current.OfType == before.OfType && current.RelationKind == catalog.TableRelation && current.Partition == nil && current.Membership == nil
}

// Comments and final attribute-rank repairs do not alter these key/provider
// observations. No later key, owner, type or provider incarnation can reuse them.
func (receipt *restoredInheritedForeignKey) validate(e *Engine, op *Operation, declaration restoredForeignKeys, view catalog.Read) error {
	auth := declaration.auth
	auth.Catalog = view
	auth.Recapture(view)
	if receipt.source != declaration.child || receipt.source.Domain() != e.catalog.Domain || receipt.target.Domain() != e.catalog.Domain || receipt.source.Epoch != view.Epoch() || receipt.target.Epoch != view.Epoch() || declaration.auth.RoleRecord != e.roles.published || !partitionConstraintReplayImport(e, op, auth) {
		return ids.ErrCatalogReplaced
	}
	source, _ := view.Relation(receipt.source.Address)
	target, _ := view.Relation(receipt.target.Address)
	if !inheritedForeignRelationUnchanged(source, receipt.beforeSource) || !inheritedForeignRelationUnchanged(target, receipt.beforeTarget) || !inheritedForeignKeyDescendant(view, target, source.Address()) || !inheritedForeignColumnsUnchanged(source, receipt.columns) || !inheritedForeignColumnsUnchanged(source, receipt.deleted) || !inheritedForeignColumnsUnchanged(target, receipt.referenced) {
		return ids.ErrCatalogReplaced
	}
	seen := 0
	for address, descriptor := range view.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != target.Address() {
			continue
		}
		observed, exists := receipt.providers[address]
		if !exists || index.SQLIndex != observed.current.SQLIndex || index.Owner != observed.current.Owner || index.Namespace() != observed.current.Namespace() || index.Name() != observed.current.Name() || len(index.Columns) != len(observed.current.Columns) {
			return ids.ErrCatalogReplaced
		}
		for i, column := range index.Columns {
			if column.Usage != observed.current.Columns[i].Usage {
				return ids.ErrCatalogReplaced
			}
		}
		seen++
	}
	if seen != len(receipt.providers) {
		return ids.ErrCatalogReplaced
	}
	return auth.Check(access.Requirement{Object: receipt.source, OwnerOnly: true})
}

func (receipt *restoredInheritedForeignKey) accepts(foreign *catalog.ForeignKeyConstraint) bool {
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
