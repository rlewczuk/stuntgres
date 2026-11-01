package core_test

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/stored"
)

func TestTriggerDeferredLookupOwnership(t *testing.T) {
	e, _, read, relation, procedure := triggerUnitFixture(t, "BEGIN RETURN NEW; END")
	definition := deferredUnitDefinition(t, read, relation, procedure)
	namespace := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 9500030}, ObjectName: "later", ObjectRevision: 1, Owner: relation.Owner}
	state := catalog.CloneState(e.Catalog.State)
	state.SetDescriptor(namespace.Address(), namespace)
	state.BindName(catalog.DescriptorNameKey(namespace), namespace.Address())
	var expected []ids.OID
	for i := range 3 {
		copy := *relation
		copy.ObjectAddress.ObjectID += ids.OID(i)
		copy.ObjectName += string(rune('a' + i))
		if i == 2 {
			copy.ObjectNamespace = namespace.Address()
		}
		p := definition.Parts()
		p.OID += ids.OID(2 * i)
		p.Constraint.OID += ids.OID(2 * i)
		p.Relation = read.Owner().Domain.Ref(read.Epoch(), copy.Address())
		p.Name = "not_the_constraint_name"
		if i != 0 {
			p.Parent = ids.ObjectAddress{ClassID: ids.TriggerClassID, ObjectID: definition.OID()}
			p.ParentRevision = 1
		}
		trigger, err := stored.NewTrigger(p)
		if err != nil {
			t.Fatal(err)
		}
		copy.Triggers = stored.TriggersPayload([]stored.Trigger{trigger})
		if i == 0 {
			// Existing table constraints participate in the same first-schema
			// decision as trigger facts, rather than a second fallback lookup.
			copy.Constraints.ForeignKeys = []catalog.ForeignKeyConstraint{{OID: 9500040, Name: "deferred", Deferrable: true}}
		}
		state.SetDescriptor(copy.Address(), &copy)
		expected = append(expected, p.Constraint.OID)
	}
	view := catalog.NewRead(read.Owner(), state)
	path := analyze.NamespaceSelection{Path: []ids.ObjectAddress{relation.Namespace(), namespace.Address()}}
	selected := make(map[ids.OID]bool)
	if err := index.SelectConstraintName(view, path, "deferred", true, selected); err != nil {
		t.Fatal(err)
	}
	if len(selected) != 3 || !selected[expected[0]] || !selected[expected[1]] || !selected[9500040] || selected[expected[2]] {
		t.Fatal("first matching schema did not combine actual constraint declarations", selected)
	}
	index.SelectInheritedConstraints(view, selected)
	if selected[expected[2]] {
		t.Fatal("trigger ancestry fabricated cross-schema constraint selection")
	}
	selected = make(map[ids.OID]bool)
	if err := index.SelectConstraintName(view, analyze.NamespaceSelection{Qualified: namespace.Address()}, "deferred", true, selected); err != nil || len(selected) != 1 || !selected[expected[2]] {
		t.Fatal("qualified name did not select the clone's independent declaration", selected, err)
	}
	var diagnostic interface{ SQLState() string }
	if err := index.SelectConstraintName(view, path, "not_the_constraint_name", false, make(map[ids.OID]bool)); !errors.As(err, &diagnostic) || diagnostic.SQLState() != "42704" {
		t.Fatal("tgname incorrectly selected a constraint", err)
	}
}
