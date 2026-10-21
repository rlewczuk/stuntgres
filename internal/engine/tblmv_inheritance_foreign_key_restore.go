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

func inheritedForeignKeyEvents(view catalog.Read, graph dump.Graph, tables []dumpRelation, nameReady func(ids.ObjectAddress) (dump.Event, bool, error)) error {
	for i := range tables {
		source := tables[i].descriptor
		event := dump.Event{OID: source.Address().ObjectID, Phase: dump.InheritedForeignKeys}
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

func (table *dumpRelation) emitInheritedForeignKeys(view catalog.Read, emit func(string) error) error {
	qualified := catalog.QuoteIdentifier(table.schema) + "." + catalog.QuoteIdentifier(table.name)
	for i := range table.descriptor.Constraints.ForeignKeys {
		foreign := &table.descriptor.Constraints.ForeignKeys[i]
		if !inheritedForeignKeyDeferred(view, table.descriptor, foreign) {
			continue
		}
		target := view.MustRelation(foreign.Target)
		text, err := (stored.Deparser{View: view}).ForeignKeyConstraintSQLWithPrimary(table.descriptor, *foreign, inheritedForeignKeyPrimary(target, foreign))
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
