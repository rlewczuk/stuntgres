package authorization

import (
	"context"
	"reflect"
	"testing"
)

func TestRoleOwnershipACLScopes(t *testing.T) {
	old, next, third, reader := grantUnitRoles()
	for _, mask := range []Privileges{SelectPrivilege | UpdatePrivilege, UsagePrivilege | CreatePrivilege} {
		original := ACLValue{Explicit: true, Entries: []ACLEntry{
			{Grantor: old, Grantee: ACLGrantee{Role: old}, Privileges: mask, GrantOptions: mask},
			{Grantor: next, Grantee: ACLGrantee{Role: next}, Privileges: SelectPrivilege},
			{Grantor: old, Grantee: ACLGrantee{Role: reader}, Privileges: mask},
			{Grantor: third, Grantee: ACLGrantee{Public: true}, Privileges: UsagePrivilege},
		}}
		retained := append([]ACLEntry(nil), original.Entries...)
		got := SchemaACLOwner(original, old, next)
		want := ACLValue{Explicit: true, Entries: []ACLEntry{
			{Grantor: next, Grantee: ACLGrantee{Role: next}, Privileges: mask | SelectPrivilege, GrantOptions: mask},
			{Grantor: next, Grantee: ACLGrantee{Role: reader}, Privileges: mask},
			{Grantor: third, Grantee: ACLGrantee{Public: true}, Privileges: UsagePrivilege},
		}}
		if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(original.Entries, retained) {
			t.Fatal("owner ACL collision/provenance/immutability", got, want, original)
		}
	}
	for _, acl := range []ACLValue{{}, {Explicit: true}} {
		if got := SchemaACLOwner(acl, old, next); !reflect.DeepEqual(got, acl) {
			t.Fatal("NULL/empty distinction", acl, got)
		}
	}
}
func TestRoleOwnershipGrantOptionsRespectObjectMask(t *testing.T) {
	owner, _, _, reader := grantUnitRoles()
	for _, mask := range []Privileges{CreatePrivilege, UsagePrivilege, SelectPrivilege | UpdatePrivilege} {
		if got, err := ScopedACLGrantOptions(context.Background(), grantUnitRead(owner), ACLValue{}, owner, owner, mask); err != nil || got != mask {
			t.Fatal("implicit ownership leaked foreign-class privilege bits", mask, got, err)
		}
		acl := ACLValue{Explicit: true, Entries: []ACLEntry{{Grantor: owner, Grantee: ACLGrantee{Role: reader}, Privileges: CreatePrivilege | SelectPrivilege, GrantOptions: CreatePrivilege | SelectPrivilege}}}
		if got, err := ScopedACLGrantOptions(context.Background(), grantUnitRead(owner), acl, owner, reader, mask); err != nil || got != (CreatePrivilege|SelectPrivilege)&mask {
			t.Fatal("explicit grant options leaked foreign-class privilege bits", mask, got, err)
		}
	}
}
