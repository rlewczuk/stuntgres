package authorization

import (
	"context"
	"reflect"
	"testing"
)

func TestRoleTransactionalRootMerge(t *testing.T) {
	domain := new(Domain)
	base := Seed(domain)
	refs := []PrincipalRef{domain.Principal(20001), domain.Principal(20002), domain.Principal(20003)}
	for i, name := range []string{"a", "b", "c"} {
		next, err := base.PutDescriptor(RoleDescriptor{Ref: refs[i], Name: name, Inherit: true, AuthIDVersion: uint64(i + 2)})
		if err != nil {
			t.Fatal(err)
		}
		base = next
	}
	mutate := func(root *Root, ref PrincipalRef, version uint64, login bool) Root {
		d, err := root.Read(PrincipalRef{}).Descriptor(ref)
		if err != nil {
			t.Fatal(err)
		}
		d.AuthIDVersion, d.Login = version, login
		next, err := root.PutDescriptor(d)
		if err != nil {
			t.Fatal(err)
		}
		return next
	}
	t.Run("disjoint_descriptors_and_edges", func(t *testing.T) {
		candidate := mutate(&base, refs[0], 10, true)
		current := mutate(&base, refs[1], 11, true)
		candidate.edges = append(append([]Membership(nil), candidate.edges...), Membership{Role: refs[0], Member: refs[2], Grantor: domain.Principal(BootstrapPrincipalID), Inherit: true, Set: true})
		current.edges = append(append([]Membership(nil), current.edges...), Membership{Role: refs[1], Member: refs[2], Grantor: domain.Principal(BootstrapPrincipalID), Admin: true})
		before, left, right := base.Read(PrincipalRef{}).Memberships(), candidate.Read(PrincipalRef{}).Memberships(), current.Read(PrincipalRef{}).Memberships()
		merged, err := MergeRoots(context.Background(), &base, &candidate, &current)
		if err != nil {
			t.Fatal(err)
		}
		for _, ref := range refs[:2] {
			d, _ := merged.Read(PrincipalRef{}).Find(ref)
			if !d.Login {
				t.Fatal("lost disjoint role change", ref)
			}
		}
		if len(merged.edges) != len(base.edges)+2 {
			t.Fatal("lost independent edge")
		}
		if !reflect.DeepEqual(before, base.Read(PrincipalRef{}).Memberships()) || !reflect.DeepEqual(left, candidate.Read(PrincipalRef{}).Memberships()) || !reflect.DeepEqual(right, current.Read(PrincipalRef{}).Memberships()) {
			t.Fatal("merge mutated an input")
		}
		old, _ := base.Read(PrincipalRef{}).Find(refs[0])
		if old.Login {
			t.Fatal("merge changed retained descriptor")
		}
	})
	t.Run("noop_and_changed_back_versions_conflict", func(t *testing.T) {
		candidate := mutate(&base, refs[0], 20, true)
		for _, current := range []Root{mutate(&base, refs[0], 21, false), mutate(&base, refs[0], 22, false)} {
			if _, err := MergeRoots(context.Background(), &base, &candidate, &current); err == nil {
				t.Fatal("equal semantic values concealed a different authid tuple")
			}
		}
		if merged, err := MergeRoots(context.Background(), &base, &candidate, &base); err != nil || merged != &candidate {
			t.Fatal("unchanged predecessor rejected", err)
		}
	})
	t.Run("whole_descriptor_comment_precondition", func(t *testing.T) {
		candidate := mutate(&base, refs[0], 25, true)
		d := base.roles[refs[0]]
		d.Comment = "committed peer comment"
		current, err := base.PutDescriptor(d)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := MergeRoots(context.Background(), &base, &candidate, &current); err == nil {
			t.Fatal("same touched descriptor silently overwrote a peer comment")
		}
		retired := base.Retire(map[PrincipalRef]RoleDescriptor{refs[0]: base.roles[refs[0]]})
		if _, err := MergeRoots(context.Background(), &base, &retired, &current); err == nil {
			t.Fatal("retirement ignored the whole-descriptor precondition")
		}
		if current.roles[refs[0]] != d || current.roles[refs[0]].AuthIDVersion != base.roles[refs[0]].AuthIDVersion {
			t.Fatal("failed merge changed committed peer state or COMMENT changed tuple version")
		}
		sequential := mutate(&current, refs[0], 26, true)
		if sequential.roles[refs[0]].Comment != d.Comment {
			t.Fatal("sequential attributes discarded Comment")
		}
	})
	t.Run("merged_cycle_and_retired_endpoint", func(t *testing.T) {
		candidate, current := base, base
		candidate.edges = append(append([]Membership(nil), base.edges...), Membership{Role: refs[0], Member: refs[1], Grantor: domain.Principal(BootstrapPrincipalID), Set: true})
		current.edges = append(append([]Membership(nil), base.edges...), Membership{Role: refs[1], Member: refs[0], Grantor: domain.Principal(BootstrapPrincipalID), Inherit: true})
		if _, err := MergeRoots(context.Background(), &base, &candidate, &current); err == nil {
			t.Fatal("merged cycle accepted")
		}
		retired := base.Retire(map[PrincipalRef]RoleDescriptor{refs[0]: base.roles[refs[0]]})
		if _, err := MergeRoots(context.Background(), &base, &retired, &current); err == nil {
			t.Fatal("surviving peer edge adopted a retired endpoint")
		}
	})
	t.Run("name_and_grantor_identity", func(t *testing.T) {
		candidate, err := base.PutDescriptor(RoleDescriptor{Ref: domain.Principal(30001), Name: "same", AuthIDVersion: 30})
		if err != nil {
			t.Fatal(err)
		}
		current, err := base.PutDescriptor(RoleDescriptor{Ref: domain.Principal(30002), Name: "same", AuthIDVersion: 31})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := MergeRoots(context.Background(), &base, &candidate, &current); err == nil {
			t.Fatal("same-name independent principals merged")
		}
		candidate = base
		candidate.edges = append(append([]Membership(nil), base.edges...), Membership{Role: refs[0], Member: refs[1], Grantor: refs[2], Inherit: true})
		current = base.Retire(map[PrincipalRef]RoleDescriptor{refs[2]: base.roles[refs[2]]})
		if _, err := MergeRoots(context.Background(), &base, &candidate, &current); err == nil {
			t.Fatal("retired grantor survived merged validation")
		}
	})
}
