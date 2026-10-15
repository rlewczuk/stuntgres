package engine

import (
	"fmt"
)

func physicalPrimaryDeferred(table *relationDescriptor, primary *uniqueConstraint) bool {
	return primary.Primary && len(table.physicalNotNull) != 0
}

func emitPhysicalPrimaryCleanup(view CatalogRead, table *relationDescriptor, primary *uniqueConstraint, emit func(string) error, setOwner func(string) error) error {
	hasPhysicalKey := false
	for _, attribute := range primary.Attributes {
		if _, present := table.physicalNotNull[attribute]; present {
			hasPhysicalKey = true
			break
		}
	}
	if !hasPhysicalKey {
		return nil
	}
	qualified := quoteIdentifier(view.state.descriptors[table.namespace].Name()) + "." + quoteIdentifier(table.name)
	keys := make([]string, len(primary.Attributes))
	for i, attribute := range primary.Attributes {
		keys[i] = table.columns[table.indexForAttribute(attribute)].Name
	}
	if err := setOwner("postgres"); err != nil {
		return err
	}
	for _, attribute := range primary.Attributes {
		if _, physical := table.physicalNotNull[attribute]; !physical {
			continue
		}
		column := table.columns[table.indexForAttribute(attribute)].Name
		if err := emit(physicalNotNullCleanupSQL(qualified, column, primary.Name, keys) + ";\n"); err != nil {
			return err
		}
	}
	return nil
}

// Run at declaration time, before descendants or rows. The constructor creates
// its real NN record; the CTE selects that record by key, never an assumed name.
func (t *dumpRelation) emitPhysicalNotNull(view CatalogRead, emit func(string) error, setOwner func(string) error) error {
	if len(t.descriptor.physicalNotNull) == 0 {
		return nil
	}
	qualified := quoteIdentifier(t.schema) + "." + quoteIdentifier(t.name)
	for _, attribute := range t.attributes {
		if _, physical := t.descriptor.physicalNotNull[attribute]; !physical || physicalPrimaryForAttribute(t.descriptor, attribute) != nil {
			continue
		}
		column := t.columns[t.descriptor.indexForAttribute(attribute)].Name
		if err := setOwner(t.owner); err != nil {
			return err
		}
		if err := emit("ALTER TABLE ONLY " + qualified + " ALTER COLUMN " + quoteIdentifier(column) + " SET NOT NULL;\n"); err != nil {
			return err
		}
		if err := setOwner("postgres"); err != nil {
			return err
		}
		if err := emit(physicalNotNullCleanupSQL(qualified, column, "", nil) + ";\n"); err != nil {
			return err
		}
	}
	for i := range t.descriptor.uniqueConstraints {
		primary := &t.descriptor.uniqueConstraints[i]
		if !primary.Primary {
			continue
		}
		index, ok := view.state.descriptors[primary.Index].(*relationDescriptor)
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
		if err := emit("ALTER TABLE ONLY " + qualified + " ADD CONSTRAINT " + quoteIdentifier(primary.Name) + " " + text + ";\n"); err != nil {
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
func physicalNotNullDumpDependencies(view CatalogRead, graph map[dumpEventKey][]dumpEventKey) error {
	var deferred map[ObjectAddress]*relationDescriptor
	for _, descriptor := range view.state.descriptors {
		table, ok := descriptor.(*relationDescriptor)
		if !ok || len(table.physicalNotNull) == 0 {
			continue
		}
		if _, emitted := graph[dumpEventKey{oid: table.address.ObjectID}]; !emitted {
			continue
		}
		for i := range table.uniqueConstraints {
			primary := &table.uniqueConstraints[i]
			if !primary.Primary {
				continue
			}
			index, ok := view.state.descriptors[primary.Index].(*relationDescriptor)
			if !ok {
				return fmt.Errorf("stuntgres: missing physical primary event")
			}
			if deferred == nil {
				deferred = make(map[ObjectAddress]*relationDescriptor)
			}
			deferred[table.address] = index
		}
	}
	if len(deferred) == 0 {
		return nil
	}
	for _, descriptor := range view.state.descriptors {
		relation, ok := descriptor.(*relationDescriptor)
		if !ok {
			continue
		}
		event := dumpEventKey{oid: relation.address.ObjectID}
		if _, emitted := graph[event]; !emitted {
			continue
		}
		if relation.sqlIndex != nil {
			producer, moved, err := physicalCompetingIndexSource(view, relation)
			if err != nil {
				return err
			}
			if moved {
				oid, err := dumpPredecessor(view, producer.address, ObjectAddress{})
				if err != nil {
					return err
				}
				addDumpDependency(graph, event, dumpEventKey{oid: oid})
			}
			continue
		}
		if relation.inheritance != nil {
			for _, parent := range relation.inheritance.parents {
				index := deferred[parent]
				if index == nil || !partitionReplaySeparateConstraint(view, index) {
					continue
				}
				table := view.state.descriptors[parent].(*relationDescriptor)
				var primary *uniqueConstraint
				for i := range table.uniqueConstraints {
					if table.uniqueConstraints[i].Primary && table.uniqueConstraints[i].Index == index.address {
						primary = &table.uniqueConstraints[i]
						break
					}
				}
				if primary == nil {
					return fmt.Errorf("stuntgres: missing reciprocal physical primary event")
				}
				temporaryNN := false
				for _, attribute := range primary.Attributes {
					if _, missing := table.physicalNotNull[attribute]; missing {
						temporaryNN = true
						break
					}
				}
				if temporaryNN {
					addDumpDependency(graph, event, dumpEventKey{oid: index.address.ObjectID})
				}
			}
		}
		for _, foreign := range relation.foreignKeys {
			primary := deferred[foreign.Target]
			if foreign.NotValid || foreign.Target == relation.address || primary == nil {
				continue
			}
			if foreign.Index != primary.address {
				index, ok := view.state.descriptors[foreign.Index].(*relationDescriptor)
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
			oid, err := dumpPredecessor(view, foreign.Index, relation.address)
			if err != nil {
				return err
			}
			addDumpDependency(graph, event, dumpEventKey{oid: oid})
		}
	}
	return nil
}
