package authorization

import (
	"context"
	"math"
	"reflect"
	"testing"
)

func TestMembershipOIDAssignment(t *testing.T) {
	domain := &Domain{}
	base := Seed(domain)
	before := base.Read(PrincipalRef{}).Memberships()
	same, frontier, err := base.AssignMembershipOIDs(20000)
	if err != nil || frontier != 20000 || &same.edges[0] != &base.edges[0] {
		t.Fatal("unchanged root copied or allocated", frontier, err)
	}
	candidate := base
	candidate.edges = append(append([]Membership(nil), base.edges...), Membership{Role: domain.Principal(3374), Member: domain.Principal(3375), Grantor: domain.Principal(10)}, Membership{Role: domain.Principal(3374), Member: domain.Principal(3375), Grantor: domain.Principal(3377), Admin: true})
	assigned, next, err := candidate.AssignMembershipOIDs(20000)
	if err != nil || next != 20002 || assigned.edges[3].OID != 20000 || assigned.edges[4].OID != 20001 || candidate.edges[3].OID != 0 {
		t.Fatal("assignment", assigned.edges, next, err)
	}
	if !reflect.DeepEqual(before, base.Read(PrincipalRef{}).Memberships()) || &assigned.edges[0] == &candidate.edges[0] {
		t.Fatal("retained backing changed")
	}
	updated := assigned
	updated.edges = append([]Membership(nil), assigned.edges...)
	updated.edges[3].Set = true
	preserved, next, err := updated.AssignMembershipOIDs(next)
	if err != nil || next != 20002 || preserved.edges[3].OID != 20000 {
		t.Fatal("option update identity", err)
	}
	mapping := make(map[PrincipalRef]PrincipalRef)
	copy := assigned.Reprovenance(&Domain{}, mapping)
	for i, edge := range copy.edges {
		if edge.OID != assigned.edges[i].OID || edge.Role == assigned.edges[i].Role {
			t.Fatal("reprovenance changed row identity", edge)
		}
	}
	retired := assigned.Retire(map[PrincipalRef]RoleDescriptor{domain.Principal(3375): assigned.roles[domain.Principal(3375)]})
	for _, edge := range retired.edges {
		if edge.OID == 0 || edge.OID == 20001 {
			t.Fatal("retirement survivor", edge)
		}
	}
}

func TestMembershipOIDExhaustion(t *testing.T) {
	root := Seed(&Domain{})
	root.edges = append(root.edges, Membership{}, Membership{})
	before := root.Read(PrincipalRef{}).Memberships()
	for _, frontier := range []uint64{0, math.MaxUint32, math.MaxUint32 + 1, math.MaxUint64} {
		got, next, err := root.AssignMembershipOIDs(frontier)
		if err == nil || next != frontier || !reflect.DeepEqual(got.edges, before) || &got.edges[0] != &root.edges[0] {
			t.Fatal("partial failed reservation", frontier, next, err)
		}
	}
	root.edges = root.edges[:len(root.edges)-1]
	last, next, err := root.AssignMembershipOIDs(math.MaxUint32)
	if err != nil || next != math.MaxUint32+1 || last.edges[len(last.edges)-1].OID != math.MaxUint32 {
		t.Fatal("last identity unavailable", next, err)
	}
}

func TestMembershipOIDMerge(t *testing.T) {
	domain := &Domain{}
	base := Seed(domain)
	a, b := base, base
	a.edges = append(append([]Membership(nil), base.edges...), Membership{Role: domain.Principal(3374), Member: domain.Principal(3375), Grantor: domain.Principal(10), Set: true})
	b.edges = append(append([]Membership(nil), base.edges...), Membership{Role: domain.Principal(3374), Member: domain.Principal(3377), Grantor: domain.Principal(10), Inherit: true})
	a, next, err := a.AssignMembershipOIDs(20000)
	if err != nil { t.Fatal(err) }
	b, _, err = b.AssignMembershipOIDs(next)
	if err != nil { t.Fatal(err) }
	merged, err := MergeRoots(context.Background(), &base, &a, &b)
	if err != nil { t.Fatal(err) }
	if len(merged.edges) != 5 { t.Fatal(merged.edges) }
	seen := map[uint32]bool{}
	for _, edge := range merged.edges {
		if edge.OID == 0 || seen[edge.OID] { t.Fatal("colliding merge", edge) }
		seen[edge.OID] = true
	}
	changed := base
	changed.edges = append([]Membership(nil), base.edges...)
	changed.edges[0].Admin = true
	foreign := base
	foreign.edges = append([]Membership(nil), base.edges...)
	foreign.edges[0].OID++
	if _, err := MergeRoots(context.Background(), &base, &changed, &foreign); err == nil {
		t.Fatal("changed predecessor identity laundered")
	}
}
