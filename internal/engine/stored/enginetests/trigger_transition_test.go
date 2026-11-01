package enginetests_test

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// SQL constructs actual providers; these units assert definition invariants,
// captured alias facts and destination ownership, not SQL execution outcomes.
func transitionDefinitionFixture(t *testing.T, e *core.Engine) (catalog.Read, *catalog.Relation, stored.Trigger) {
	t.Helper()
	enginetest.Run(t, e, "CREATE TABLE transition_owner(x integer)")
	enginetest.Run(t, e, "CREATE FUNCTION transition_function() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$")
	enginetest.Run(t, e, `CREATE TRIGGER transition_definition AFTER UPDATE ON transition_owner REFERENCING NEW TABLE AS "New bag" OLD TABLE AS "Old ""bag" EXECUTE FUNCTION transition_function('owned')`)
	read := e.CatalogRead()
	for _, relation := range read.Relations() {
		if relation.Name() == "transition_owner" {
			return read, relation, stored.TriggersOf(relation)[0]
		}
	}
	t.Fatal("missing definition owner")
	return catalog.Read{}, nil, stored.Trigger{}
}

func TestTriggerTransitionDefinition(t *testing.T) {
	read, relation, original := transitionDefinitionFixture(t, engine.New())
	parts := original.Parts()
	if parts.OldTable != `Old "bag` || parts.NewTable != "New bag" || parts.Level != stored.TriggerStatement || parts.Events != stored.TriggerUpdate {
		t.Fatalf("lost immutable aliases: %+v", parts)
	}
	if err := (stored.TriggerList{original}).Validate(read, relation); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*stored.TriggerParts){
		"before":      func(p *stored.TriggerParts) { p.Timing = stored.TriggerBefore },
		"instead":     func(p *stored.TriggerParts) { p.Timing = stored.TriggerInstead },
		"truncate":    func(p *stored.TriggerParts) { p.Events = stored.TriggerTruncate },
		"multievent":  func(p *stored.TriggerParts) { p.Events |= stored.TriggerInsert },
		"old insert":  func(p *stored.TriggerParts) { p.Events = stored.TriggerInsert; p.NewTable = "" },
		"new delete":  func(p *stored.TriggerParts) { p.Events = stored.TriggerDelete; p.OldTable = "" },
		"column list": func(p *stored.TriggerParts) { p.UpdateColumns = []int32{relation.Attributes[0]} },
		"same alias":  func(p *stored.TriggerParts) { p.NewTable = p.OldTable },
		"constraint": func(p *stored.TriggerParts) {
			p.Level = stored.TriggerRow
			p.Constraint = catalog.TriggerConstraint{OID: p.OID + 1, Name: p.Name}
		},
		"clone": func(p *stored.TriggerParts) {
			p.Level = stored.TriggerRow
			p.Parent = ids.ObjectAddress{ClassID: 2620, ObjectID: 1}
			p.ParentRevision = 1
		},
	} {
		t.Run(name, func(t *testing.T) {
			p := parts
			mutate(&p)
			if _, err := stored.NewTrigger(p); err == nil {
				t.Fatal("invalid transition definition accepted")
			}
		})
	}
	for _, control := range []struct {
		event    stored.TriggerEvent
		old, new string
	}{
		{stored.TriggerInsert, "", "new"}, {stored.TriggerDelete, "old", ""}, {stored.TriggerUpdate, "old", "new"}, {stored.TriggerUpdate, "", "new"}, {stored.TriggerUpdate, "old", ""},
	} {
		for _, level := range []stored.TriggerLevel{stored.TriggerStatement, stored.TriggerRow} {
			p := parts
			p.Events, p.OldTable, p.NewTable, p.Level = control.event, control.old, control.new, level
			definition, err := stored.NewTrigger(p)
			if err != nil {
				t.Fatal("legal constructor", err)
			}
			if err := (stored.TriggerList{definition}).Validate(read, relation); err != nil {
				t.Fatal("legal relation", err)
			}
		}
	}
	for _, kind := range []catalog.RelationKind{catalog.ViewRelation, catalog.ForeignRelation, catalog.MaterializedViewRelation} {
		owner := *relation
		owner.RelationKind = kind
		if err := (stored.TriggerList{original}).Validate(read, &owner); err == nil {
			t.Fatal("transition accepted on forbidden relation", kind)
		}
	}
	rowParts := parts
	rowParts.Level = stored.TriggerRow
	row, err := stored.NewTrigger(rowParts)
	if err != nil {
		t.Fatal(err)
	}
	for _, hierarchy := range []string{"root", "partition", "inheritance"} {
		owner := *relation
		switch hierarchy {
		case "root":
			owner.RelationKind = catalog.PartitionedRelation
		case "partition":
			owner.Membership = &catalog.PartitionMembership{Parent: relation.Address()}
		case "inheritance":
			owner.Inheritance = &catalog.TableInheritance{Parents: []catalog.InheritanceParent{{Relation: relation.Address()}}}
		}
		if err := (stored.TriggerList{row}).Validate(read, &owner); err == nil {
			t.Fatal("ROW transition accepted on hierarchy", hierarchy)
		}
	}
	if _, err := stored.CloneTrigger(read, row, relation, relation, row.OID()+1, 0, nil); err == nil {
		t.Fatal("cloned ROW transition onto partition")
	}
	for _, changeOld := range []bool{false, true} {
		p := parts
		if changeOld {
			p.OldTable += " changed"
		} else {
			p.NewTable += " changed"
		}
		next, err := stored.NewTrigger(p)
		if err != nil {
			t.Fatal(err)
		}
		if original.SameExecutable(next) {
			t.Fatal("alias change did not invalidate executable identity")
		}
	}
	mode, err := original.WithMode(catalog.TriggerDisabled)
	if err != nil || !original.SameExecutable(mode) || mode.Parts().OldTable != parts.OldTable || mode.Parts().NewTable != parts.NewTable {
		t.Fatal("mode replacement lost aliases", err)
	}
	copy := original.Parts()
	copy.OldTable, copy.NewTable = "changed", "changed"
	if original.Parts().OldTable != parts.OldTable || original.Parts().NewTable != parts.NewTable {
		t.Fatal("parts envelope mutation changed definition")
	}
	ctx := context.Background()
	seen := false
	err = stored.VisitTriggerDefinitions(ctx, read, relation, stored.Presentation{}, true, func(f stored.TriggerPresentation) error {
		seen = true
		if f.OldTable != parts.OldTable || f.NewTable != parts.NewTable {
			t.Fatal("presentation lost aliases", f)
		}
		return nil
	})
	if err != nil || !seen {
		t.Fatal("presentation visit", err)
	}
	typ, ok := read.LookupType(sqltype.PgNodeTree)
	if !ok {
		t.Fatal("missing tree type")
	}
	seen = false
	err = stored.VisitTriggerCatalog(ctx, read, relation, typ, func(f stored.TriggerCatalogDefinition) error {
		seen = true
		if f.OldTable != parts.OldTable || f.NewTable != parts.NewTable {
			t.Fatal("catalog facts lost aliases", f)
		}
		return nil
	})
	if err != nil || !seen {
		t.Fatal("catalog visit", err)
	}
	definition, err := stored.TriggerDefinitionSQL(ctx, relation, original, stored.Deparser{View: read})
	if err != nil || !strings.Contains(definition, ` REFERENCING OLD TABLE AS "Old ""bag" NEW TABLE AS "New bag" FOR EACH STATEMENT`) {
		t.Fatal("canonical quoted alias rendering", definition, err)
	}
	replay := stored.TriggerCloneReplay{ParentSchema: "public", ParentTable: "transition_owner", ParentTrigger: parts.Name, ChildSchema: "public", ChildTable: "child", ChildTrigger: parts.Name, SQL: `CREATE TRIGGER transition_definition AFTER UPDATE ON public.child REFERENCING OLD TABLE o FOR EACH ROW EXECUTE FUNCTION transition_function()`}
	if err := replay.Validate(); err == nil {
		t.Fatal("retained clone replay accepted transition relation")
	}
}

func TestTriggerTransitionRemap(t *testing.T) {
	source, destination := engine.New(), engine.New()
	enginetest.Run(t, destination, "CREATE TABLE transition_padding(x integer)")
	_, relation, original := transitionDefinitionFixture(t, source)
	read, target, targetDefinition := transitionDefinitionFixture(t, destination)
	parts, targetParts := original.Parts(), targetDefinition.Parts()
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{relation.Address(): target.Address(), parts.Procedure.Address: targetParts.Procedure.Address}
	ref := func(old ids.ObjectRef) ids.ObjectRef {
		address, ok := mapping[old.Address]
		if !ok {
			address = old.Address
		}
		return read.Owner().Domain.Ref(read.Epoch(), address)
	}
	mapped, err := stored.RemapTriggers(read, relation, mapping, ref, func(old *sqltype.Descriptor) *sqltype.Descriptor { return old }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	owner := *target
	owner.Triggers = mapped.Triggers
	list := stored.TriggerList(stored.TriggersOf(&owner))
	if err := list.Validate(read, &owner); err != nil {
		t.Fatal(err)
	}
	p := list[0].Parts()
	if p.Relation != targetParts.Relation || p.Procedure != targetParts.Procedure || p.OldTable != parts.OldTable || p.NewTable != parts.NewTable || &p.Arguments[0] == &parts.Arguments[0] {
		t.Fatal("destination mapping lost aliases or retained mutable backing")
	}
	if original.Parts().Relation != parts.Relation || original.Parts().Procedure != parts.Procedure || original.Parts().OldTable != parts.OldTable || original.Parts().NewTable != parts.NewTable {
		t.Fatal("remap mutated source")
	}
}
