package engine

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestPartitionAttachEligibility(t *testing.T) {
	e := New()
	parent := inheritanceUnitRelation(e, "parent", 32200, "k")
	child := inheritanceUnitRelation(e, "child", 32201, "k")
	view := inheritanceUnitView(e, parent, child)
	if err := partitionAttachEligibility(view, parent, child); err != nil {
		t.Fatal(err)
	}
	ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 32202}
	view.state.setDescriptor(ns, &namespaceDescriptor{address: ns, temporary: e.catalog.domain.Session(99)})
	child.namespace = ns
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: `cannot attach a temporary relation as partition of permanent relation "parent"`}
	var got *sqlstate.Error
	if err := partitionAttachEligibility(view, parent, child); !errors.As(err, &got) || !reflect.DeepEqual(got, want) {
		t.Fatalf("temporary eligibility: got %v want %v", err, want)
	}
	if child.Membership != nil || parent.Partition != nil {
		t.Fatal("eligibility changed topology")
	}
}

func TestPartitionAttachPersistenceInvariant(t *testing.T) {
	e := New()
	parent := inheritanceUnitRelation(e, "parent", 32210, "k")
	child := inheritanceUnitRelation(e, "child", 32211, "k")
	parent.RelationKind = partitionedRelation
	usage := parent.Columns[0].Usage
	class, supported := sqltype.DefaultIndexClass(usage.Type)
	if !supported {
		t.Fatal("missing default ordering class")
	}
	ordering, err := sqltype.BindIndexOrdering(usage, class)
	if err != nil {
		t.Fatal(err)
	}
	parent.Partition = &partitionDefinition{keys: []partitionKeyEntry{{attribute: 1, usage: usage, class: class, comparison: ordering, label: "k"}}, children: []ids.ObjectAddress{child.address}, defaultChild: child.address}
	child.Membership = &partitionMembership{parent: parent.address, defaultBound: true}
	view := inheritanceUnitView(e, parent, child)
	check := func(wantValid bool) {
		t.Helper()
		err := validatePartitionDescriptor(view, child)
		if (err == nil) != wantValid {
			t.Fatalf("persistence validity got %v want valid=%v", err, wantValid)
		}
	}
	check(true)
	one := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 32212}
	two := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 32213}
	token := e.catalog.domain.Session(99)
	view.state.setDescriptor(one, &namespaceDescriptor{address: one, temporary: token})
	view.state.setDescriptor(two, &namespaceDescriptor{address: two, temporary: e.catalog.domain.Session(100)})
	child.namespace = one
	check(false) // Permanent parent, temporary child.
	parent.namespace, child.namespace = one, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	check(false) // Temporary parent, permanent child.
	child.namespace = two
	check(false) // Equal persistence cannot bridge physical-session authority.
	child.namespace = one
	check(true) // Catalog invariant only: this does not admit TEMP partition DDL.
}

func TestPartitionAttachNotNullValidity(t *testing.T) {
	for _, c := range []struct {
		name                                  string
		parentInvalid, childInvalid, sameName bool
	}{
		{name: "validated_different_names"},
		{name: "invalid_different_names", childInvalid: true},
		{name: "invalid_same_name", childInvalid: true, sameName: true},
		{name: "both_invalid", parentInvalid: true, childInvalid: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			parent := inheritanceUnitRelation(e, "parent", 32220, "k", "payload")
			parent.Attributes = []int32{3, 8}
			parent.AttributeHighWater = 8
			parent.Columns[1].Nullable = false
			parent.NotNullConstraints = []notNullConstraint{{OID: 32221, Name: "parent_nn", Attribute: 8, NotValid: c.parentInvalid}}
			child := inheritanceUnitRelation(e, "child", 32222, "payload", "k")
			child.Attributes = []int32{2, 9}
			child.AttributeHighWater = 9
			child.Columns[0].Nullable, child.Columns[1].Nullable = false, false
			name := "child_nn"
			if c.sameName {
				name = "parent_nn"
			}
			child.NotNullConstraints = []notNullConstraint{{OID: 32223, Name: name, Attribute: 2, NotValid: c.childInvalid, partitionLocal: true}, {OID: 32224, Name: "stronger_local_nn", Attribute: 9, NotValid: true, partitionLocal: true}}
			original := append([]notNullConstraint(nil), child.NotNullConstraints...)
			view := inheritanceUnitView(e, parent, child)
			next, err := partitionAttachSchema(&StatementScope{op: op}, view, parent, child)
			if !c.parentInvalid && c.childInvalid {
				want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P17", Message: `constraint "` + name + `" conflicts with NOT VALID constraint on child table "child"`}
				var got *sqlstate.Error
				if next != nil || !errors.As(err, &got) || !reflect.DeepEqual(got, want) {
					t.Fatalf("invalid mapped NOT NULL got %v, %v want %v", next, err, want)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				matched := original[0]
				matched.partitionLocal = false
				if !reflect.DeepEqual(next.NotNullConstraints, []notNullConstraint{matched, original[1]}) {
					t.Fatal("mapped identity/validity or stronger-local provenance changed", next.NotNullConstraints)
				}
			}
			if !reflect.DeepEqual(child.NotNullConstraints, original) || child.Membership != nil {
				t.Fatal("schema validation mutated original constraints or membership")
			}
		})
	}
}
