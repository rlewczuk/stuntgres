package table

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// A valid FK to an ordinary descendant cannot live in its ancestor's CREATE:
// the referenced relation must first inherit that declaration. Replay the real
// constraint after both physical row streams and its actual key provider exist.
func InheritedForeignKeyDeferred(view catalog.Read, source *catalog.Relation, foreign *catalog.ForeignKeyConstraint) bool {
	if foreign.NotValid || source.RelationKind != catalog.TableRelation || source.Partition != nil || source.Membership != nil || foreign.Target == source.Address() {
		return false
	}
	target, ok := view.Relation(foreign.Target)
	return ok && target.RelationKind == catalog.TableRelation && target.Partition == nil && target.Membership == nil && InheritedForeignKeyDescendant(view, target, source.Address())
}

func InheritedForeignKeyDescendant(view catalog.Read, target *catalog.Relation, ancestor ids.ObjectAddress) bool {
	if target.Inheritance == nil {
		return false
	}
	for _, parent := range target.Inheritance.Parents {
		if parent.Relation == ancestor {
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
		for _, edge := range relation.Inheritance.Parents {
			address := edge.Relation
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

func InheritedForeignKeyEvents(view catalog.Read, graph dump.Graph, tables []DumpRelation, nameReady func(ids.ObjectAddress) (dump.Event, bool, error)) error {
	for i := range tables {
		source := tables[i].Descriptor
		event := dump.Event{OID: source.Address().ObjectID, Phase: dump.InheritedForeignKeys}
		for i := range source.Constraints.ForeignKeys {
			foreign := &source.Constraints.ForeignKeys[i]
			if !InheritedForeignKeyDeferred(view, source, foreign) {
				continue
			}
			provider, present, err := nameReady(foreign.Index)
			if err != nil {
				return err
			}
			if !present {
				return fmt.Errorf("stuntgres: missing inherited foreign-key replay provider")
			}
			graph.Add(event, dump.Event{OID: source.Address().ObjectID, Phase: dump.Rows})
			graph.Add(event, dump.Event{OID: foreign.Target.ObjectID, Phase: dump.Rows})
			graph.Add(event, dump.Event{OID: foreign.Target.ObjectID, Phase: dump.TableCompletion})
			graph.Add(event, provider)
			graph.Add(dump.Event{OID: source.Address().ObjectID, Phase: dump.TableCompletion}, event)
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

func (table *DumpRelation) EmitInheritedForeignKeys(view catalog.Read, emit func(string) error) error {
	qualified := catalog.QuoteIdentifier(table.Schema) + "." + catalog.QuoteIdentifier(table.Name)
	for i := range table.Descriptor.Constraints.ForeignKeys {
		foreign := &table.Descriptor.Constraints.ForeignKeys[i]
		if !InheritedForeignKeyDeferred(view, table.Descriptor, foreign) {
			continue
		}
		target := view.MustRelation(foreign.Target)
		text, err := (stored.Deparser{View: view}).ForeignKeyConstraintSQLWithPrimary(table.Descriptor, *foreign, inheritedForeignKeyPrimary(target, foreign))
		if err != nil {
			return err
		}
		if err := emit("ALTER TABLE ONLY " + qualified + " ADD CONSTRAINT " + catalog.QuoteIdentifier(foreign.Name) + " " + text + ";\n"); err != nil {
			return err
		}
	}
	return nil
}

const RestoreInheritedForeignKeyBoundary = "restore accepts valid foreign-key ALTER only for freshly imported ordinary ancestor/descendant relations"

type RestoredInheritedForeignColumn struct {
	name      string
	Attribute int32
	usage     sqltype.Usage
}

type RestoredInheritedForeignIndex struct {
	Current  *catalog.Relation
	Eligible bool
}

type RestoredInheritedForeignKey struct {
	Source, Target      ids.ObjectRef
	BeforeSource        *catalog.Relation
	BeforeTarget        *catalog.Relation
	Raw                 parser.ForeignKeyConstraint
	Columns, Referenced []RestoredInheritedForeignColumn
	Deleted             []RestoredInheritedForeignColumn
	Providers           map[ids.ObjectAddress]RestoredInheritedForeignIndex
}

func CaptureInheritedForeignColumns(table *catalog.Relation, names []parser.Identifier) ([]RestoredInheritedForeignColumn, bool) {
	columns := make([]RestoredInheritedForeignColumn, len(names))
	for i, name := range names {
		slot, exists := table.Index[name.Text]
		if !exists {
			return nil, false
		}
		for _, previous := range columns[:i] {
			if previous.Attribute == table.Attributes[slot] {
				return nil, false
			}
		}
		columns[i] = RestoredInheritedForeignColumn{name: name.Text, Attribute: table.Attributes[slot], usage: table.Columns[slot].Usage}
	}
	return columns, true
}

func InheritedForeignColumnsUnchanged(table *catalog.Relation, columns []RestoredInheritedForeignColumn) bool {
	for _, column := range columns {
		slot, exists := table.Index[column.name]
		if !exists || table.Attributes[slot] != column.Attribute || table.Columns[slot].Usage != column.usage {
			return false
		}
	}
	return true
}

func InheritedForeignRelationUnchanged(current, before *catalog.Relation) bool {
	return current != nil && current.Address() == before.Address() && current.Namespace() == before.Namespace() && current.Name() == before.Name() && current.Owner == before.Owner && current.OfType == before.OfType && current.RelationKind == catalog.TableRelation && current.Partition == nil && current.Membership == nil
}

func (receipt *RestoredInheritedForeignKey) Accepts(foreign *catalog.ForeignKeyConstraint) bool {
	if foreign.Name != receipt.Raw.Name.Text || foreign.Target != receipt.Target.Address || foreign.NotValid || foreign.Full != receipt.Raw.Full || foreign.UpdateAction != receipt.Raw.UpdateAction || foreign.DeleteAction != receipt.Raw.DeleteAction || len(foreign.Attributes) != len(receipt.Columns) || len(foreign.Referenced) != len(receipt.Referenced) || len(foreign.DeleteAttributes) != len(receipt.Deleted) || !receipt.Providers[foreign.Index].Eligible {
		return false
	}
	for i, column := range receipt.Columns {
		if foreign.Attributes[i] != column.Attribute {
			return false
		}
	}
	for i, column := range receipt.Referenced {
		if foreign.Referenced[i] != column.Attribute {
			return false
		}
	}
	for i, column := range receipt.Deleted {
		if foreign.DeleteAttributes[i] != column.Attribute {
			return false
		}
	}
	return true
}

// InheritedForeignKeyShape reports whether raw is a valid, named foreign
// key between two different qualified relations, the only shape an
// ancestor/descendant receipt covers; a self reference or another action is
// not handled, a malformed one rejected.
func InheritedForeignKeyShape(raw *parser.AlterTable) (bool, error) {
	if len(raw.Actions) != 1 || raw.Actions[0].Action != parser.AddForeignKey || raw.Actions[0].ForeignKey == nil || raw.Actions[0].ForeignKey.NotValid {
		return false, nil
	}
	foreign := raw.Actions[0].ForeignKey
	if !raw.Only || raw.IfExists || len(raw.Name.Parts) != 2 || len(foreign.Target.Parts) != 2 || foreign.Name.Text == "" || len(foreign.Columns) == 0 {
		return false, sqlError("0A000", RestoreInheritedForeignKeyBoundary)
	}
	if raw.Name.Parts[0].Text == foreign.Target.Parts[0].Text && raw.Name.Parts[1].Text == foreign.Target.Parts[1].Text {
		return false, nil
	}
	return true, nil
}

// CaptureInheritedForeignKey issues the receipt for foreign between
// sourceRef and targetRef, two freshly imported permanent ordinary tables
// with a strict inheritance edge, and binds pending's one declaration to it.
// This is not a general valid ALTER whitelist; the source's owner must
// declare it, and every index of the target is recorded as observed.
func CaptureInheritedForeignKey(view access.View, env access.Environment, sourceRef, targetRef ids.ObjectRef, created map[ids.ObjectAddress]ids.ObjectRef, foreign parser.ForeignKeyConstraint, pending *RestoredForeignKeys) error {
	reject := func() error { return sqlError("0A000", RestoreInheritedForeignKeyBoundary) }
	source, sourceOK := view.Catalog.Relation(sourceRef.Address)
	target, targetOK := view.Catalog.Relation(targetRef.Address)
	if !sourceOK || !targetOK || created[sourceRef.Address] != sourceRef || created[targetRef.Address] != targetRef || source.RelationKind != catalog.TableRelation || target.RelationKind != catalog.TableRelation || source.Partition != nil || source.Membership != nil || target.Partition != nil || target.Membership != nil || sourceRef == targetRef || !InheritedForeignKeyDescendant(view.Catalog, target, source.Address()) {
		return reject()
	}
	if view.Catalog.MustNamespace(source.Namespace()).Temporary != (ids.SessionToken{}) || view.Catalog.MustNamespace(target.Namespace()).Temporary != (ids.SessionToken{}) {
		return reject()
	}
	if err := view.Check(env, access.Requirement{Object: sourceRef, OwnerOnly: true}); err != nil {
		return err
	}
	receipt := &RestoredInheritedForeignKey{Source: sourceRef, Target: targetRef, BeforeSource: source, BeforeTarget: target, Raw: foreign, Providers: make(map[ids.ObjectAddress]RestoredInheritedForeignIndex)}
	var valid bool
	receipt.Columns, valid = CaptureInheritedForeignColumns(source, foreign.Columns)
	if !valid {
		return reject()
	}
	receipt.Deleted, valid = CaptureInheritedForeignColumns(source, foreign.DeleteColumns)
	if !valid {
		return reject()
	}
	referenced := foreign.Referenced
	if len(referenced) == 0 {
		for _, key := range target.Constraints.Unique {
			if key.Primary {
				pending.Definitions[0].Supporting = key.Index
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
	receipt.Referenced, valid = CaptureInheritedForeignColumns(target, referenced)
	if !valid || len(receipt.Referenced) != len(receipt.Columns) {
		return reject()
	}
	attributes := make([]int32, len(receipt.Referenced))
	for i, column := range receipt.Referenced {
		attributes[i] = column.Attribute
	}
	for address, descriptor := range view.Catalog.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != target.Address() {
			continue
		}
		eligible, err := ForeignKeyIndexEligible(target, index, attributes)
		if err != nil {
			return err
		}
		receipt.Providers[address] = RestoredInheritedForeignIndex{Current: index, Eligible: eligible}
	}
	pending.Child = sourceRef
	pending.Definitions[0].Target = targetRef.Address
	pending.Definitions[0].Path = []ids.ObjectAddress{target.Namespace()}
	pending.Definitions[0].Inherited = receipt
	return nil
}

// Unchanged reports whether view still holds the
// relations, inheritance edge, key columns and target indexes receipt
// observed. Comments and final attribute-rank repairs do not alter these
// observations; no later key, owner, type or provider incarnation can reuse
// them.
func (receipt *RestoredInheritedForeignKey) Unchanged(view catalog.Read) bool {
	source, _ := view.Relation(receipt.Source.Address)
	target, _ := view.Relation(receipt.Target.Address)
	if !InheritedForeignRelationUnchanged(source, receipt.BeforeSource) || !InheritedForeignRelationUnchanged(target, receipt.BeforeTarget) || !InheritedForeignKeyDescendant(view, target, source.Address()) || !InheritedForeignColumnsUnchanged(source, receipt.Columns) || !InheritedForeignColumnsUnchanged(source, receipt.Deleted) || !InheritedForeignColumnsUnchanged(target, receipt.Referenced) {
		return false
	}
	seen := 0
	for address, descriptor := range view.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != target.Address() {
			continue
		}
		observed, exists := receipt.Providers[address]
		if !exists || index.SQLIndex != observed.Current.SQLIndex || index.Owner != observed.Current.Owner || index.Namespace() != observed.Current.Namespace() || index.Name() != observed.Current.Name() || len(index.Columns) != len(observed.Current.Columns) {
			return false
		}
		for i, column := range index.Columns {
			if column.Usage != observed.Current.Columns[i].Usage {
				return false
			}
		}
		seen++
	}
	return seen == len(receipt.Providers)
}
