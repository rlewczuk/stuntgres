package control

import (
	"context"
	"hash/fnv"
	"slices"
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
)

const AttributePlaceholderStem = "__stuntgres_retired_"

type attributeSlot struct {
	Relation ids.ObjectAddress
	Number   int32
}

type attributeHistory struct {
	names   map[attributeSlot]string
	origins map[attributeSlot]attributeSlot
	shared  map[attributeSlot][]attributeSlot
}

func (s *dumpScript) prepareAttributeHistory() error {
	h := &attributeHistory{names: make(map[attributeSlot]string), origins: make(map[attributeSlot]attributeSlot), shared: make(map[attributeSlot][]attributeSlot)}
	s.attributeHistory = h
	var relations []*catalog.Relation
	usedNames := make(map[string]bool)
	for _, d := range s.OriginalCatalog.Descriptors() {
		if r, ok := d.(*catalog.Relation); ok {
			for _, c := range r.Columns {
				usedNames[c.Name] = true
			}
		}
	}
	for i := range s.Tables {
		relations = append(relations, s.Tables[i].Descriptor)
	}
	identity := func(r *catalog.Relation) string { return s.OriginalCatalog.Get(r.Namespace()).Name() + "." + r.Name() }
	slices.SortFunc(relations, func(a, b *catalog.Relation) int { return strings.Compare(identity(a), identity(b)) })
	// Dropped source names/origins no longer exist. Choose a deterministic
	// synthetic history using only scalar tombstone facts and current edges.
	for _, r := range relations {
		if r.Inheritance == nil {
			continue
		}
		var parents []ids.ObjectAddress
		if r.Inheritance != nil {
			for _, edge := range r.Inheritance.Parents {
				parents = append(parents, edge.Relation)
			}
		}
		used := make(map[attributeSlot]bool)
		numbers := make([]int32, 0, len(r.ColumnMeta.Dropped))
		for number := range r.ColumnMeta.Dropped {
			numbers = append(numbers, number)
		}
		slices.Sort(numbers)
		for _, number := range numbers {
			fact := r.ColumnMeta.Dropped[number]
			if fact.Local || fact.Inheritance == 0 {
				continue
			}
			var candidates []attributeSlot
			for _, address := range parents {
				parent := s.OriginalCatalog.MustRelation(address)
				for parentNumber := range parent.ColumnMeta.Dropped {
					candidate := attributeSlot{parent.Address(), parentNumber}
					if !used[candidate] {
						candidates = append(candidates, candidate)
					}
				}
			}
			slices.SortFunc(candidates, func(a, b attributeSlot) int {
				if order := strings.Compare(identity(s.OriginalCatalog.MustRelation(a.Relation)), identity(s.OriginalCatalog.MustRelation(b.Relation))); order != 0 {
					return order
				}
				return int(a.Number - b.Number)
			})
			if len(candidates) == 0 {
				continue
			} // The staged matcher may require a different construction frontier.
			chosen := candidates[0]
			used[chosen] = true
			h.origins[attributeSlot{r.Address(), number}] = chosen
		}
	}
	var root func(attributeSlot) attributeSlot
	root = func(slot attributeSlot) attributeSlot {
		if parent, ok := h.origins[slot]; ok {
			return root(parent)
		}
		return slot
	}
	for _, r := range relations {
		numbers := make([]int32, 0, len(r.ColumnMeta.Dropped))
		for number := range r.ColumnMeta.Dropped {
			numbers = append(numbers, number)
		}
		slices.Sort(numbers)
		for _, number := range numbers {
			slot := attributeSlot{r.Address(), number}
			source := root(slot)
			name := h.names[source]
			if name == "" {
				hash := fnv.New64a()
				_, _ = hash.Write([]byte(identity(s.OriginalCatalog.MustRelation(source.Relation))))
				base := AttributePlaceholderStem + strconv.FormatUint(hash.Sum64(), 16) + "_" + strconv.Itoa(int(source.Number))
				name = base
				for collision := 1; usedNames[name]; collision++ {
					name = base + "_" + strconv.Itoa(collision)
				}
				usedNames[name] = true
				h.names[source] = name
			}
			h.names[slot] = name
			if slot != source {
				h.shared[source] = append(h.shared[source], slot)
			}
		}
	}
	return nil
}

func (s *dumpScript) addAttributeRetirementEvents() {
	for source, participants := range s.attributeHistory.shared {
		if s.attributeSchedules[source.Relation] != nil {
			continue
		}
		event := dump.Event{OID: source.Relation.ObjectID, Phase: dump.AttributeRetirement}
		s.Graph.Add(event, dump.Event{OID: source.Relation.ObjectID})
		s.Graph.Add(dump.Event{OID: source.Relation.ObjectID, Phase: dump.Rows}, event)
		for _, slot := range participants {
			s.Graph.Add(event, dump.Event{OID: slot.Relation.ObjectID})
			s.Graph.Add(dump.Event{OID: slot.Relation.ObjectID, Phase: dump.Rows}, event)
		}
		// A child not participating in this synthetic retired origin must be
		// created after retirement, so it cannot inherit an extra physical slot.
		for _, child := range catalog.InheritanceChildren(s.OriginalCatalog, source.Relation) {
			participates := slices.ContainsFunc(participants, func(slot attributeSlot) bool { return slot.Relation == child.Address() })
			if !participates {
				s.Graph.Add(dump.Event{OID: child.Address().ObjectID}, event)
			}
		}
	}
}

func (s *dumpScript) emitLocalAttributeRetirements(w *dumpWriter, r *catalog.Relation) error {
	if len(r.ColumnMeta.Dropped) == 0 {
		return nil
	}
	for number := int32(1); number <= r.ColumnMeta.HighWater; number++ {
		slot := attributeSlot{r.Address(), number}
		name := s.attributeHistory.names[slot]
		if name == "" {
			continue
		}
		if _, inherited := s.attributeHistory.origins[slot]; inherited {
			continue
		}
		if len(s.attributeHistory.shared[slot]) != 0 {
			continue
		}
		qualified := catalog.QuoteIdentifier(s.OriginalCatalog.Get(r.Namespace()).Name()) + "." + catalog.QuoteIdentifier(r.Name())
		if err := w.emit("ALTER TABLE ONLY " + qualified + " DROP COLUMN " + catalog.QuoteIdentifier(name) + ";\n"); err != nil {
			return err
		}
	}
	return nil
}

func emitDumpAttributeRetirement(ctx context.Context, s *dumpScript, w *dumpWriter, event dump.Event) error {
	r := s.OriginalCatalog.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: event.OID})
	qualified := catalog.QuoteIdentifier(s.OriginalCatalog.Get(r.Namespace()).Name()) + "." + catalog.QuoteIdentifier(r.Name())
	if schedule := s.attributeSchedules[r.Address()]; schedule != nil && event.Step != 0 {
		op := schedule.events[event]
		model := schedule.models[op.Relation]
		qualified = catalog.QuoteIdentifier(s.OriginalCatalog.Get(model.Namespace()).Name()) + "." + catalog.QuoteIdentifier(model.Name())
		only := "ONLY "
		if len(op.Participants) > 1 {
			only = ""
		}
		return w.emit("ALTER TABLE " + only + qualified + " DROP COLUMN " + catalog.QuoteIdentifier(s.attributeHistory.names[attributeSlot{op.Relation, op.Number}]) + ";\n")
	}
	for number := int32(1); number <= r.ColumnMeta.HighWater; number++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		slot := attributeSlot{r.Address(), number}
		if len(s.attributeHistory.shared[slot]) == 0 {
			continue
		}
		if err := w.emit("ALTER TABLE " + qualified + " DROP COLUMN " + catalog.QuoteIdentifier(s.attributeHistory.names[slot]) + ";\n"); err != nil {
			return err
		}
	}
	return nil
}
