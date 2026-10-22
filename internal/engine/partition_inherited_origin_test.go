package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/table"
	"testing"
)

func TestPartitionInheritedProvenance(t *testing.T) {
	e := New()
	parent := inheritanceUnitRelation(e, "origin_parent", 48000, "a")
	child := inheritanceUnitRelation(e, "origin_child", 48001, "a")
	parent.Constraints.Check = []catalog.CheckConstraint{{OID: 48002, Name: "ck"}}
	parent.Constraints.NotNull = []catalog.NotNullConstraint{{OID: 48003, Name: "parent_nn", Attribute: 1}}
	child.Membership = &catalog.PartitionMembership{Parent: parent.Address()}
	child.Constraints.Check = []catalog.CheckConstraint{{OID: 48004, Name: "ck", PartitionOrigin: &catalog.InheritedConstraint{Origins: []catalog.InheritanceOrigin{{Parent: parent.Address(), Constraint: 48002}}}}}
	child.Constraints.NotNull = []catalog.NotNullConstraint{{OID: 48005, Name: "child_nn", Attribute: 1, PartitionOrigin: &catalog.InheritedConstraint{Local: true, Origins: []catalog.InheritanceOrigin{{Parent: parent.Address(), Attribute: 1, Constraint: 48003}}}}}
	view := inheritanceUnitView(e, parent, child)
	if err := partition.ValidateConstraintOrigins(view, child); err != nil {
		t.Fatal(err)
	}
	source := child.Constraints.NotNull[0].PartitionOrigin
	clone := catalog.CloneConstraintOrigin(source)
	clone.Origins[0].Constraint = 49999
	if source.Origins[0].Constraint != 48003 {
		t.Fatal("clone changed captured origin")
	}
	bad := *child
	bad.Constraints.NotNull = append([]catalog.NotNullConstraint(nil), child.Constraints.NotNull...)
	bad.Constraints.NotNull[0].PartitionOrigin = clone
	if partition.ValidateConstraintOrigins(view, &bad) == nil {
		t.Fatal("missing constraint origin admitted")
	}
	clone.Origins[0] = source.Origins[0]
	clone.Origins = append(clone.Origins, clone.Origins[0])
	if partition.ValidateConstraintOrigins(view, &bad) == nil {
		t.Fatal("duplicate immediate contribution admitted")
	}
	clone.Origins = nil
	if err := partition.ValidateConstraintOrigins(view, &bad); err != nil {
		t.Fatal("local survivor rejected", err)
	}
	clone.Local = false
	if partition.ValidateConstraintOrigins(view, &bad) == nil {
		t.Fatal("nonlocal unsupported fact admitted")
	}
}

func TestPartitionInheritedOriginRemap(t *testing.T) {
	e := New()
	p := inheritanceUnitRelation(e, "origin_p", 48100, "a")
	q := inheritanceUnitRelation(e, "origin_q", 48101, "a")
	c := inheritanceUnitRelation(e, "origin_c", 48102, "a")
	d := inheritanceUnitRelation(e, "origin_d", 48103, "a")
	// The same old numeric ID in two parents must not become a global OID map.
	p.Constraints.Check = []catalog.CheckConstraint{{OID: 48104, Name: "ck"}}
	q.Constraints.Check = []catalog.CheckConstraint{{OID: 48104, Name: "ck"}}
	for _, pair := range []struct{ child, parent *catalog.Relation }{{c, p}, {d, q}} {
		pair.child.Membership = &catalog.PartitionMembership{Parent: pair.parent.Address()}
		pair.child.Constraints.Check = []catalog.CheckConstraint{{OID: 48105, Name: "ck", PartitionOrigin: &catalog.InheritedConstraint{Origins: []catalog.InheritanceOrigin{{Parent: pair.parent.Address(), Constraint: 48104}}}}}
	}
	view := inheritanceUnitView(e, p, q, c, d)
	previous := map[ids.ObjectAddress]catalog.Descriptor{}
	for a, descriptor := range view.Descriptors() {
		previous[a] = descriptor
	}
	next := catalog.CloneState(view.State())
	for i, parent := range []*catalog.Relation{p, q} {
		copy := *parent
		copy.Constraints.Check = append([]catalog.CheckConstraint(nil), parent.Constraints.Check...)
		copy.Constraints.Check[0].OID = ids.OID(48200 + i)
		next.SetDescriptor(copy.Address(), &copy)
	}
	if err := table.RemapInheritanceConstraintOrigins(next, previous); err != nil {
		t.Fatal(err)
	}
	for i, child := range []*catalog.Relation{c, d} {
		origin := next.MustRelation(child.Address()).Constraints.Check[0].PartitionOrigin
		if origin.Origins[0].Constraint != ids.OID(48200+i) || child.Constraints.Check[0].PartitionOrigin.Origins[0].Constraint != 48104 {
			t.Fatal("parent-qualified remap or immutable capture lost")
		}
	}
	copy := *c
	moved := p.Address()
	moved.ObjectID = 48300
	table.RemapPartitionConstraintOrigins(&copy, func(a ids.ObjectAddress) ids.ObjectAddress {
		if a == p.Address() {
			return moved
		}
		return a
	}, func(o catalog.InheritanceOrigin) ids.OID { return o.Constraint })
	if copy.Constraints.Check[0].PartitionOrigin.Origins[0].Parent != moved || c.Constraints.Check[0].PartitionOrigin.Origins[0].Parent != p.Address() {
		t.Fatal("address remap mutated source")
	}
}
