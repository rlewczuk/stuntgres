package enginetests_test

import (
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// SQL only constructs real catalog providers; assertions exercise the checked
// payload, immutable backing and destination mapping rather than SQL outcomes.
func TestTriggerInsteadDefinitionKinds(t *testing.T) {
	source, destination := engine.New(), engine.New()
	enginetest.Run(t, destination, "CREATE TABLE trigger_padding(x integer)")
	for _, e := range []*core.Engine{source, destination} {
		enginetest.Run(t, e, "CREATE TABLE trigger_base(x integer)")
		enginetest.Run(t, e, "CREATE VIEW trigger_view AS SELECT x FROM trigger_base")
		enginetest.Run(t, e, "CREATE FUNCTION trigger_function() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$")
		enginetest.Run(t, e, "CREATE TRIGGER a INSTEAD OF INSERT OR UPDATE OR DELETE ON trigger_view FOR EACH ROW EXECUTE FUNCTION trigger_function('original')")
		enginetest.Run(t, e, "CREATE TRIGGER conditional BEFORE UPDATE OF x ON trigger_base FOR EACH ROW WHEN (NEW.x > 0) EXECUTE FUNCTION trigger_function()")
	}
	find := func(read catalog.Read, name string) *catalog.Relation {
		t.Helper()
		for _, descriptor := range read.Descriptors() {
			if relation, ok := descriptor.(*catalog.Relation); ok && relation.Name() == name {
				return relation
			}
		}
		t.Fatalf("missing relation %s", name)
		return nil
	}
	read := source.CatalogRead()
	view, base := find(read, "trigger_view"), find(read, "trigger_base")
	list := stored.TriggerList(stored.TriggersOf(view))
	if err := list.Validate(read, view); err != nil {
		t.Fatal(err)
	}
	original := list[0].Parts()
	if original.Timing != stored.TriggerInstead || original.Level != stored.TriggerRow || original.Events != stored.TriggerInsert|stored.TriggerUpdate|stored.TriggerDelete {
		t.Fatalf("incorrect definition: %+v", original)
	}
	for _, control := range []struct {
		timing stored.TriggerTiming
		level  stored.TriggerLevel
		events stored.TriggerEvent
	}{
		{stored.TriggerBefore, stored.TriggerRow, stored.TriggerInsert | stored.TriggerUpdate | stored.TriggerDelete},
		{stored.TriggerAfter, stored.TriggerRow, stored.TriggerInsert | stored.TriggerUpdate | stored.TriggerDelete},
		{stored.TriggerAfter, stored.TriggerStatement, stored.TriggerInsert | stored.TriggerUpdate | stored.TriggerDelete | stored.TriggerTruncate},
	} {
		parts := original
		parts.Relation = read.Owner().Domain.Ref(read.Epoch(), base.Address())
		parts.Timing, parts.Level, parts.Events = control.timing, control.level, control.events
		definition, err := stored.NewTrigger(parts)
		if err != nil {
			t.Fatal("ordinary control constructor", err)
		}
		if err := (stored.TriggerList{definition}).Validate(read, base); err != nil {
			t.Fatal("ordinary control catalog", err)
		}
	}
	for _, events := range []stored.TriggerEvent{stored.TriggerInsert, stored.TriggerUpdate, stored.TriggerDelete, stored.TriggerInsert | stored.TriggerUpdate | stored.TriggerDelete} {
		parts := original
		parts.Events = events
		definition, err := stored.NewTrigger(parts)
		if err != nil {
			t.Fatal(err)
		}
		if err := (stored.TriggerList{definition}).Validate(read, view); err != nil {
			t.Fatalf("events %d: %v", events, err)
		}
	}
	conditional := stored.TriggersOf(base)[0].Parts()
	invalid := map[string]func(*stored.TriggerParts){
		"unknown timing": func(p *stored.TriggerParts) { p.Timing = 255 },
		"unknown level":  func(p *stored.TriggerParts) { p.Level = 255 },
		"statement":      func(p *stored.TriggerParts) { p.Level = stored.TriggerStatement },
		"truncate":       func(p *stored.TriggerParts) { p.Events |= stored.TriggerTruncate },
		"empty events":   func(p *stored.TriggerParts) { p.Events = 0 },
		"unknown event":  func(p *stored.TriggerParts) { p.Events = 128 },
		"columns":        func(p *stored.TriggerParts) { p.UpdateColumns = []int32{view.Attributes[0]} },
		"when": func(p *stored.TriggerParts) {
			p.When, p.Presentation, p.Binding = conditional.When, conditional.Presentation, conditional.Binding
		},
		"presentation": func(p *stored.TriggerParts) { p.Presentation = conditional.Presentation },
		"binding":      func(p *stored.TriggerParts) { p.Binding = &analyze.Binding{} },
		"parent": func(p *stored.TriggerParts) {
			p.Parent = ids.ObjectAddress{ClassID: 2620, ObjectID: 1}
			p.ParentRevision = 1
		},
		"parent revision": func(p *stored.TriggerParts) { p.ParentRevision = 1 },
	}
	for name, mutate := range invalid {
		t.Run(name, func(t *testing.T) {
			parts := original
			mutate(&parts)
			if _, err := stored.NewTrigger(parts); err == nil {
				t.Fatal("invalid definition accepted")
			}
		})
	}
	if err := (stored.TriggerList{{}}).Validate(read, view); err == nil {
		t.Fatal("forged empty payload accepted")
	}
	for _, kind := range []catalog.RelationKind{catalog.TableRelation, catalog.PartitionedRelation, catalog.ForeignRelation, catalog.MaterializedViewRelation} {
		wrong := *view
		wrong.RelationKind = kind
		if err := list.Validate(read, &wrong); err == nil {
			t.Fatalf("INSTEAD OF accepted on kind %v", kind)
		}
	}
	for _, timing := range []stored.TriggerTiming{stored.TriggerBefore, stored.TriggerAfter} {
		parts := original
		parts.Timing = timing
		definition, err := stored.NewTrigger(parts)
		if err != nil {
			t.Fatal(err)
		}
		if err := (stored.TriggerList{definition}).Validate(read, view); err == nil {
			t.Fatal("ordinary row trigger accepted on view")
		}
	}
	statement := original
	statement.Timing, statement.Level = stored.TriggerAfter, stored.TriggerStatement
	definition, err := stored.NewTrigger(statement)
	if err != nil {
		t.Fatal(err)
	}
	if err := (stored.TriggerList{definition}).Validate(read, view); err != nil {
		t.Fatal("AFTER STATEMENT view control", err)
	}
	statement.Events = stored.TriggerTruncate
	definition, err = stored.NewTrigger(statement)
	if err != nil {
		t.Fatal(err)
	}
	if err := (stored.TriggerList{definition}).Validate(read, view); err == nil {
		t.Fatal("TRUNCATE view trigger accepted")
	}

	// Constructors own caller slices; replacing a list does not mutate snapshots.
	arguments := []string{"detached"}
	references := slices.Clone(original.References)
	parts := original
	parts.Arguments, parts.References = arguments, references
	detached, err := stored.NewTrigger(parts)
	if err != nil {
		t.Fatal(err)
	}
	arguments[0], references[0] = "changed", ids.ObjectAddress{}
	if detached.Parts().Arguments[0] != "detached" || detached.Parts().References[0] != original.Procedure.Address {
		t.Fatal("constructor retained mutable inputs")
	}
	replacement := slices.Clone(list)
	replacement[0] = detached
	if list[0].Parts().Arguments[0] != "original" || &replacement[0] == &list[0] {
		t.Fatal("replacement changed old list backing")
	}

	destRead := destination.CatalogRead()
	destView := find(destRead, "trigger_view")
	destParts := stored.TriggersOf(destView)[0].Parts()
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{view.Address(): destView.Address(), original.Procedure.Address: destParts.Procedure.Address}
	remapRef := func(ref ids.ObjectRef) ids.ObjectRef {
		address, ok := mapping[ref.Address]
		if !ok {
			address = ref.Address
		}
		return destRead.Owner().Domain.Ref(destRead.Epoch(), address)
	}
	mapped, err := stored.RemapTriggers(destRead, view, mapping, remapRef, func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old.ID() == view.RowType {
			typ, _ := destRead.LookupType(destView.RowType)
			return typ
		}
		return old
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The enclosing relation remapper owns its header and rowtype, while this
	// operation owns only the trigger payload.
	owner := *destView
	owner.Triggers = mapped.Triggers
	mappedList := stored.TriggerList(stored.TriggersOf(&owner))
	if err := mappedList.Validate(destRead, &owner); err != nil {
		t.Fatal(err)
	}
	mappedParts := mappedList[0].Parts()
	if mappedParts.Relation != destParts.Relation || mappedParts.Procedure != destParts.Procedure || mappedParts.Timing != stored.TriggerInstead || &mappedList[0] == &list[0] || &mappedParts.Arguments[0] == &original.Arguments[0] {
		t.Fatal("remapping did not own destination identities/backing")
	}
	if list[0].Parts().Relation != original.Relation || list[0].Parts().Procedure != original.Procedure || list[0].Parts().Arguments[0] != "original" {
		t.Fatal("remapping mutated source")
	}
}
