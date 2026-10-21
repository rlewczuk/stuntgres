package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/table"
)

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
			producer, moved, err := table.PhysicalCompetingIndexSource(view, relation)
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
				idx := deferred[parent]
				if idx == nil || !index.PartitionReplaySeparateConstraint(view, idx) {
					continue
				}
				table := view.MustRelation(parent)
				var primary *catalog.UniqueConstraint
				for i := range table.Constraints.Unique {
					if table.Constraints.Unique[i].Primary && table.Constraints.Unique[i].Index == idx.Address() {
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
					graph.Add(event, dump.Event{OID: idx.Address().ObjectID})
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
				_, moved, err := table.PhysicalCompetingIndexSource(view, index)
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
