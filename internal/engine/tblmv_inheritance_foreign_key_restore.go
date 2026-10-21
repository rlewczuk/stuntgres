package engine

import (
	"fmt"
	"slices"
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

func InheritedForeignKeyEvents(view catalog.Read, graph dump.Graph, tables []dumpRelation, nameReady func(ids.ObjectAddress) (dump.Event, bool, error)) error {
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

func (table *dumpRelation) EmitInheritedForeignKeys(view catalog.Read, emit func(string) error) error {
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

type restoredInheritedForeignColumn struct {
	name      string
	Attribute int32
	usage     sqltype.Usage
}

type restoredInheritedForeignIndex struct {
	Current  *catalog.Relation
	Eligible bool
}

type restoredInheritedForeignKey struct {
	Source, Target      ids.ObjectRef
	BeforeSource        *catalog.Relation
	BeforeTarget        *catalog.Relation
	Raw                 parser.ForeignKeyConstraint
	Columns, Referenced []restoredInheritedForeignColumn
	Deleted             []restoredInheritedForeignColumn
	Providers           map[ids.ObjectAddress]restoredInheritedForeignIndex
}

func CaptureInheritedForeignColumns(table *catalog.Relation, names []parser.Identifier) ([]restoredInheritedForeignColumn, bool) {
	columns := make([]restoredInheritedForeignColumn, len(names))
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
		columns[i] = restoredInheritedForeignColumn{name: name.Text, Attribute: table.Attributes[slot], usage: table.Columns[slot].Usage}
	}
	return columns, true
}

func InheritedForeignColumnsUnchanged(table *catalog.Relation, columns []restoredInheritedForeignColumn) bool {
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

func (receipt *restoredInheritedForeignKey) Accepts(foreign *catalog.ForeignKeyConstraint) bool {
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
