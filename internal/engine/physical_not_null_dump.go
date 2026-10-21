package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
)

func physicalPrimaryDeferred(table *catalog.Relation, primary *catalog.UniqueConstraint) bool {
	return primary.Primary && len(table.Constraints.PhysicalNotNull) != 0
}

func emitPhysicalPrimaryCleanup(view catalog.Read, table *catalog.Relation, primary *catalog.UniqueConstraint, emit func(string) error, setOwner func(string) error) error {
	hasPhysicalKey := false
	for _, attribute := range primary.Attributes {
		if _, present := table.Constraints.PhysicalNotNull[attribute]; present {
			hasPhysicalKey = true
			break
		}
	}
	if !hasPhysicalKey {
		return nil
	}
	qualified := catalog.QuoteIdentifier(view.Get(table.Namespace()).Name()) + "." + catalog.QuoteIdentifier(table.Name())
	keys := make([]string, len(primary.Attributes))
	for i, attribute := range primary.Attributes {
		keys[i] = table.Columns[table.IndexForAttribute(attribute)].Name
	}
	if err := setOwner("postgres"); err != nil {
		return err
	}
	for _, attribute := range primary.Attributes {
		if _, physical := table.Constraints.PhysicalNotNull[attribute]; !physical {
			continue
		}
		column := table.Columns[table.IndexForAttribute(attribute)].Name
		if err := emit(physicalNotNullCleanupSQL(qualified, column, primary.Name, keys) + ";\n"); err != nil {
			return err
		}
	}
	return nil
}

// Run at declaration time, before descendants or rows. The constructor creates
// its real NN record; the CTE selects that record by key, never an assumed name.
func (t *dumpRelation) emitPhysicalNotNull(view catalog.Read, emit func(string) error, setOwner func(string) error) error {
	if len(t.descriptor.Constraints.PhysicalNotNull) == 0 {
		return nil
	}
	qualified := catalog.QuoteIdentifier(t.schema) + "." + catalog.QuoteIdentifier(t.name)
	for _, attribute := range t.attributes {
		if _, physical := t.descriptor.Constraints.PhysicalNotNull[attribute]; !physical || physicalPrimaryForAttribute(t.descriptor, attribute) != nil {
			continue
		}
		column := t.columns[t.descriptor.IndexForAttribute(attribute)].Name
		if err := setOwner(t.owner); err != nil {
			return err
		}
		if err := emit("ALTER TABLE ONLY " + qualified + " ALTER COLUMN " + catalog.QuoteIdentifier(column) + " SET NOT NULL;\n"); err != nil {
			return err
		}
		if err := setOwner("postgres"); err != nil {
			return err
		}
		if err := emit(physicalNotNullCleanupSQL(qualified, column, "", nil) + ";\n"); err != nil {
			return err
		}
	}
	for i := range t.descriptor.Constraints.Unique {
		primary := &t.descriptor.Constraints.Unique[i]
		if !primary.Primary {
			continue
		}
		index, ok := view.Relation(primary.Index)
		if !ok {
			return fmt.Errorf("stuntgres: missing physical primary dump index")
		}
		if partitionReplaySeparateConstraint(view, index) {
			continue
		}
		text, err := (pgRenderer{view: view}).uniqueConstraintSQL(t.descriptor, primary)
		if err != nil {
			return err
		}
		if err := setOwner(t.owner); err != nil {
			return err
		}
		if err := emit("ALTER TABLE ONLY " + qualified + " ADD CONSTRAINT " + catalog.QuoteIdentifier(primary.Name) + " " + text + ";\n"); err != nil {
			return err
		}
		if err := emitPhysicalPrimaryCleanup(view, t.descriptor, primary, emit, setOwner); err != nil {
			return err
		}
	}
	return nil
}

// Track only actual moved providers in the existing event graph. Consumers do
// not wait for unrelated keys, omitted TEMP objects, or synthetic catalog nodes.
func physicalNotNullDumpDependencies(view catalog.Read, graph dump.Graph) error {
	var deferred map[ids.ObjectAddress]*catalog.Relation
	for _, descriptor := range view.Descriptors() {
		table, ok := descriptor.(*catalog.Relation)
		if !ok || len(table.Constraints.PhysicalNotNull) == 0 {
			continue
		}
		if _, emitted := graph[dump.Event{OID: table.Address().ObjectID}]; !emitted {
			continue
		}
		for i := range table.Constraints.Unique {
			primary := &table.Constraints.Unique[i]
			if !primary.Primary {
				continue
			}
			index, ok := view.Relation(primary.Index)
			if !ok {
				return fmt.Errorf("stuntgres: missing physical primary event")
			}
			if deferred == nil {
				deferred = make(map[ids.ObjectAddress]*catalog.Relation)
			}
			deferred[table.Address()] = index
		}
	}
	if len(deferred) == 0 {
		return nil
	}
	for _, descriptor := range view.Descriptors() {
		relation, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		event := dump.Event{OID: relation.Address().ObjectID}
		if _, emitted := graph[event]; !emitted {
			continue
		}
		if relation.SQLIndex != nil {
			producer, moved, err := physicalCompetingIndexSource(view, relation)
			if err != nil {
				return err
			}
			if moved {
				oid, err := dumpPredecessor(view, producer.Address(), ids.ObjectAddress{})
				if err != nil {
					return err
				}
				graph.Add(event, dump.Event{OID: oid})
			}
			continue
		}
		if relation.Inheritance != nil {
			for _, parent := range relation.Inheritance.Parents {
				index := deferred[parent]
				if index == nil || !partitionReplaySeparateConstraint(view, index) {
					continue
				}
				table := view.MustRelation(parent)
				var primary *catalog.UniqueConstraint
				for i := range table.Constraints.Unique {
					if table.Constraints.Unique[i].Primary && table.Constraints.Unique[i].Index == index.Address() {
						primary = &table.Constraints.Unique[i]
						break
					}
				}
				if primary == nil {
					return fmt.Errorf("stuntgres: missing reciprocal physical primary event")
				}
				temporaryNN := false
				for _, attribute := range primary.Attributes {
					if _, missing := table.Constraints.PhysicalNotNull[attribute]; missing {
						temporaryNN = true
						break
					}
				}
				if temporaryNN {
					graph.Add(event, dump.Event{OID: index.Address().ObjectID})
				}
			}
		}
		for _, foreign := range relation.Constraints.ForeignKeys {
			primary := deferred[foreign.Target]
			if foreign.NotValid || foreign.Target == relation.Address() || primary == nil {
				continue
			}
			if foreign.Index != primary.Address() {
				index, ok := view.Relation(foreign.Index)
				if !ok {
					return fmt.Errorf("stuntgres: missing physical foreign-key provider")
				}
				_, moved, err := physicalCompetingIndexSource(view, index)
				if err != nil {
					return err
				}
				if !moved {
					continue
				}
			}
			oid, err := dumpPredecessor(view, foreign.Index, relation.Address())
			if err != nil {
				return err
			}
			graph.Add(event, dump.Event{OID: oid})
		}
	}
	return nil
}
