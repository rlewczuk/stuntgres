package control

import (
	"context"
	"slices"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
)

type attributeSchedule struct {
	relations  []*catalog.Relation
	simulation *attributeSimulation
	models     map[ids.ObjectAddress]*catalog.Relation
	creates    map[ids.ObjectAddress]attributeOperation
	events     map[dump.Event]attributeOperation
	columns    map[attributeSlot]dump.Event
	ready      dump.Event
}

func (s *dumpScript) sourceAttributeShape(r *catalog.Relation) bool {
	if r.Membership != nil {
		return true
	}
	var actual []string
	appendUnique := func(name string) {
		if !slices.Contains(actual, name) {
			actual = append(actual, name)
		}
	}
	identity := func(r *catalog.Relation, n int32) string {
		if i := r.IndexForAttribute(n); i >= 0 {
			return "n:" + r.Columns[i].Name
		}
		return "h:" + s.attributeHistory.names[attributeSlot{r.Address(), n}]
	}
	if r.Inheritance != nil {
		for _, edge := range r.Inheritance.Parents {
			parent := s.OriginalCatalog.MustRelation(edge.Relation)
			for number := int32(1); number <= parent.ColumnMeta.HighWater; number++ {
				if parent.HasAttribute(number) {
					appendUnique(identity(parent, number))
					continue
				}
				for slot, origin := range s.attributeHistory.origins {
					if slot.Relation == r.Address() && origin == (attributeSlot{parent.Address(), number}) {
						appendUnique(identity(parent, number))
					}
				}
			}
		}
	}
	for number := int32(1); number <= r.ColumnMeta.HighWater; number++ {
		_, _, local, _, exists := expectedAttribute(r, number)
		if exists && local {
			appendUnique(identity(r, number))
		}
	}
	if len(actual) != int(r.ColumnMeta.HighWater) {
		return false
	}
	for i, name := range actual {
		if name != identity(r, int32(i+1)) {
			return false
		}
	}
	// One source-shaped terminal root event can batch only closures that need
	// the same child-constructor frontier. Other histories use action events.
	for source, participants := range s.attributeHistory.shared {
		if source.Relation != r.Address() {
			continue
		}
		for other, others := range s.attributeHistory.shared {
			if other.Relation != r.Address() {
				continue
			}
			for _, child := range catalog.InheritanceChildren(s.OriginalCatalog, r.Address()) {
				a := slices.ContainsFunc(participants, func(slot attributeSlot) bool { return slot.Relation == child.Address() })
				b := slices.ContainsFunc(others, func(slot attributeSlot) bool { return slot.Relation == child.Address() })
				if a != b {
					return false
				}
			}
		}
	}
	return true
}

func (s *dumpScript) prepareAttributeSchedules(ctx context.Context) error {
	s.attributeSchedules = make(map[ids.ObjectAddress]*attributeSchedule)
	s.deferredColumnStorage = make(map[ids.ObjectAddress]bool)
	byAddress := make(map[ids.ObjectAddress]*catalog.Relation, len(s.Tables))
	var ordered []*catalog.Relation
	for i := range s.Tables {
		r := s.Tables[i].Descriptor
		byAddress[r.Address()] = r
		ordered = append(ordered, r)
	}
	identity := func(r *catalog.Relation) string { return s.OriginalCatalog.Get(r.Namespace()).Name() + "." + r.Name() }
	slices.SortFunc(ordered, func(a, b *catalog.Relation) int { return strings.Compare(identity(a), identity(b)) })
	seen := make(map[ids.ObjectAddress]bool)
	for _, root := range ordered {
		if seen[root.Address()] {
			continue
		}
		var component []*catalog.Relation
		queue := []ids.ObjectAddress{root.Address()}
		for len(queue) != 0 {
			address := queue[len(queue)-1]
			queue = queue[:len(queue)-1]
			if seen[address] {
				continue
			}
			seen[address] = true
			r := byAddress[address]
			if r == nil {
				continue
			}
			component = append(component, r)
			if r.Inheritance != nil {
				for _, edge := range r.Inheritance.Parents {
					queue = append(queue, edge.Relation)
				}
			}
			for _, child := range catalog.InheritanceChildren(s.OriginalCatalog, address) {
				queue = append(queue, child.Address())
			}
		}
		// Even source-shaped ordinary hierarchies need neutral constructor
		// policies: final ONLY changes may have made their parents incompatible.
		if len(component) > 1 {
			for _, r := range component {
				s.deferredColumnStorage[r.Address()] = true
			}
		}
		staged := false
		for _, r := range component {
			staged = staged || !s.sourceAttributeShape(r)
		}
		if !staged {
			continue
		}
		slices.SortFunc(component, func(a, b *catalog.Relation) int { return strings.Compare(identity(a), identity(b)) })
		models, helpers := s.attributeModels(component)
		simulation, err := scheduleAttributes(ctx, models, helpers)
		if err != nil {
			return err
		}
		plan := &attributeSchedule{relations: component, simulation: simulation, models: make(map[ids.ObjectAddress]*catalog.Relation), creates: make(map[ids.ObjectAddress]attributeOperation), events: make(map[dump.Event]attributeOperation), columns: make(map[attributeSlot]dump.Event)}
		for _, model := range models {
			plan.models[model.Address()] = model
		}
		for slot, group := range simulation.Groups {
			s.attributeHistory.names[slot] = s.attributeHistory.names[group]
		}
		for i, op := range simulation.Operations {
			event := attributeOperationEvent(op, i)
			if op.Kind == 'c' {
				plan.creates[op.Relation] = op
				for _, number := range op.Locals {
					plan.columns[attributeSlot{op.Relation, number}] = event
				}
			}
			if op.Kind == 'a' {
				plan.columns[attributeSlot{op.Relation, op.Number}] = event
			}
			plan.events[event] = op
			plan.ready = event
		}
		for _, r := range component {
			s.attributeSchedules[r.Address()] = plan
		}
	}
	return nil
}

// installAttributeScheduleGraph runs before declaration phase chaining, so its
// new edges are genuine constructor/provider/consumer edges, never inferred
// from the later stable declaration order.
func (s *dumpScript) installAttributeScheduleGraph() {
	if len(s.attributeSchedules) == 0 {
		return
	}
	plans := make(map[*attributeSchedule]bool)
	for _, plan := range s.attributeSchedules {
		plans[plan] = true
	}
	for plan := range plans {
		dependencies := make(map[ids.ObjectAddress][]dump.Event)
		for _, r := range plan.relations {
			event := dump.Event{OID: r.Address().ObjectID}
			for _, pred := range s.Graph[event] {
				address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pred.OID}
				if s.attributeSchedules[address] != plan {
					dependencies[r.Address()] = append(dependencies[r.Address()], pred)
				}
			}
			s.Graph[event] = nil
		}
		// Existing consumers need the completed attribute layout, not just the
		// empty shell. Completion repairs can themselves depend on those users.
		for event, pred := range s.Graph {
			address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: event.OID}
			if event.Phase == dump.Declaration && s.attributeSchedules[address] == plan {
				continue
			}
			for i, p := range pred {
				if p.Phase == dump.Declaration && s.attributeSchedules[ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: p.OID}] == plan {
					pred[i] = plan.ready
				}
			}
			s.Graph[event] = pred
		}
		var previous dump.Event
		for i, op := range plan.simulation.Operations {
			event := attributeOperationEvent(op, i)
			if _, ok := s.Graph[event]; !ok {
				s.Graph[event] = nil
			}
			if i != 0 {
				s.Graph.Add(event, previous)
			}
			if op.Kind == 'a' || op.Kind == 'c' && len(op.Locals) != 0 {
				for _, pred := range dependencies[op.Relation] {
					s.Graph.Add(event, pred)
				}
			}
			previous = event
		}
		for _, r := range plan.relations {
			completion := dump.Event{OID: r.Address().ObjectID, Phase: dump.AttributeCompletion}
			s.Graph.Add(completion, plan.ready)
			for _, pred := range dependencies[r.Address()] {
				s.Graph.Add(completion, pred)
			}
			checks := dump.Event{OID: r.Address().ObjectID, Phase: dump.AttributeCompletion, Step: 1}
			s.Graph.Add(checks, plan.ready)
			for _, pred := range dependencies[r.Address()] {
				s.Graph.Add(checks, pred)
			}
			s.Graph.Add(completion, checks)
			if r.Inheritance != nil {
				for _, edge := range r.Inheritance.Parents {
					s.Graph.Add(dump.Event{OID: edge.Relation.ObjectID, Phase: dump.AttributeCompletion, Step: 1}, checks)
					s.Graph.Add(completion, dump.Event{OID: edge.Relation.ObjectID, Phase: dump.AttributeCompletion})
				}
			}
		}
	}
}

func (s *dumpScript) finishAttributeScheduleGraph() {
	for address, plan := range s.attributeSchedules {
		completion := dump.Event{OID: address.ObjectID, Phase: dump.AttributeCompletion}
		for _, phase := range []dump.Phase{dump.Rows, dump.Defaults, dump.InheritanceRecipes, dump.InheritanceChecks, dump.InvalidConstraints} {
			event := dump.Event{OID: address.ObjectID, Phase: phase}
			if _, exists := s.Graph[event]; exists {
				s.Graph.Add(event, completion)
			}
		}
		s.Graph.Add(completion, plan.ready)
		r := s.OriginalCatalog.MustRelation(address)
		for _, fk := range r.Constraints.ForeignKeys {
			if fk.NotValid || table.PhysicalSelfForeignKeyDeferred(r, fk) || table.InheritedForeignKeyDeferred(s.OriginalCatalog, r, &fk) {
				continue
			}
			final := dump.Event{OID: address.ObjectID, Phase: dump.AttributeCompletion, Step: 2}
			s.Graph.Add(final, completion)
			for i := range s.Tables {
				s.Graph.Add(final, dump.Event{OID: s.Tables[i].Descriptor.Address().ObjectID, Phase: dump.Rows})
			}
			break
		}
	}
}

func (s *dumpScript) stagedTableCreateSQL(ctx context.Context, t *table.DumpRelation, op attributeOperation) (string, error) {
	var line strings.Builder
	line.WriteString("CREATE TABLE " + catalog.QuoteIdentifier(t.Schema) + "." + catalog.QuoteIdentifier(t.Name) + " (")
	for i, number := range op.Locals {
		if i != 0 {
			line.WriteString(", ")
		}
		if slot := t.Descriptor.IndexForAttribute(number); slot >= 0 {
			if err := writeDumpColumn(ctx, s, t, slot, false, &line); err != nil {
				return "", err
			}
		} else {
			line.WriteString(catalog.QuoteIdentifier(s.attributeHistory.names[attributeSlot{t.Descriptor.Address(), number}]) + " " + s.retiredAttributeType(t.Descriptor, number))
		}
	}
	line.WriteString(")")
	model := s.attributeSchedules[op.Relation].models[op.Relation]
	if model.Inheritance != nil && len(model.Inheritance.Parents) != 0 {
		line.WriteString(" INHERITS (")
		for i, edge := range model.Inheritance.Parents {
			if i != 0 {
				line.WriteString(", ")
			}
			parent := s.attributeSchedules[op.Relation].models[edge.Relation]
			line.WriteString(catalog.QuoteIdentifier(s.OriginalCatalog.Get(parent.Namespace()).Name()) + "." + catalog.QuoteIdentifier(parent.Name()))
		}
		line.WriteString(")")
	}
	line.WriteString(";\n")
	return line.String(), nil
}

func emitDumpAttributeConstruction(ctx context.Context, s *dumpScript, w *dumpWriter, event dump.Event) error {
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: event.OID}
	plan := s.attributeSchedules[address]
	op := plan.events[event]
	if op.Relation.SubID != 0 || op.Kind == 'n' || op.Kind == 'x' {
		return s.emitHelperOperation(ctx, w, plan, op, event)
	}
	t := s.table(event.OID)
	t.Descriptor = s.CatalogView.MustRelation(address)
	var line strings.Builder
	line.WriteString("ALTER TABLE " + catalog.QuoteIdentifier(t.Schema) + "." + catalog.QuoteIdentifier(t.Name) + " ADD COLUMN ")
	if i := t.Descriptor.IndexForAttribute(op.Number); i >= 0 {
		if err := writeDumpColumn(ctx, s, &t, i, false, &line); err != nil {
			return err
		}
	} else {
		line.WriteString(catalog.QuoteIdentifier(s.attributeHistory.names[attributeSlot{op.Relation, op.Number}]) + " " + s.retiredAttributeType(t.Descriptor, op.Number))
	}
	line.WriteString(";\n")
	return s.emitBound(w, castConsumer{Event: event}, line.String())
}

func emitDumpAttributeCompletion(ctx context.Context, s *dumpScript, w *dumpWriter, event dump.Event) error {
	t := s.table(event.OID)
	t.Descriptor = s.CatalogView.MustRelation(t.Descriptor.Address())
	if err := s.emitStagedTableConstraints(ctx, w, &t, event.Step); err != nil {
		return err
	}
	if event.Step != 0 {
		return nil
	}
	return emitTableCompletion(ctx, s, w, t, t.Descriptor.OfType != (ids.ObjectAddress{}))
}

func (s *dumpScript) attributeConsumer(site castConsumer, attribute int32, check string) castConsumer {
	if site.Event.Phase != dump.Declaration {
		return site
	}
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: site.Event.OID}
	plan := s.attributeSchedules[address]
	if plan == nil {
		return site
	}
	if check != "" {
		site.Event.Phase = dump.AttributeCompletion
		site.Event.Step = 1
		site.Check = check
		return site
	}
	if event, ok := plan.columns[attributeSlot{address, attribute}]; ok {
		site.Event = event
		site.Attribute = 0
		return site
	}
	site.Event.Phase = dump.AttributeCompletion
	site.Attribute = attribute
	return site
}
