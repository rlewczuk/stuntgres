package authorization

import "testing"

func TestSessionIdentityMembershipClosure(t *testing.T) {
	domain := &Domain{}
	root := Seed(domain)
	admin := domain.Principal(10)
	a, b, c, d := domain.Principal(100), domain.Principal(101), domain.Principal(102), domain.Principal(103)
	for i, ref := range []PrincipalRef{a, b, c, d} {
		root.roles[ref] = RoleDescriptor{Ref: ref, Name: []string{"a", "b", "c", "d"}[i], Login: i == 0}
	}
	root.edges = append(root.edges, Membership{Role: b, Member: a, Grantor: admin, Set: true}, Membership{Role: c, Member: b, Grantor: admin, Set: true}, Membership{Role: c, Member: a, Grantor: admin, Set: true}, Membership{Role: d, Member: a, Grantor: admin, Inherit: true, Admin: true})
	read := root.Read(admin)
	for _, tc := range []struct {
		from, to PrincipalRef
		want     bool
	}{{a, a, true}, {a, b, true}, {a, c, true}, {a, d, false}, {d, c, false}, {admin, d, true}} {
		got, err := read.CanSetRole(tc.from, tc.to)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	root = root.Retire(map[PrincipalRef]RoleDescriptor{d: root.roles[d]})
	if _, err := root.Read(admin).CanSetRole(a, d); err == nil {
		t.Fatal("stale role adopted")
	}
}
