package role

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqlstate"
)

func TestPGInitPrivsRoleCleanup(t *testing.T) {
	domain := &authorization.Domain{}
	root := authorization.Seed(domain)
	roles := root.Read(authorization.PrincipalRef{})
	owner := domain.Principal(authorization.BootstrapPrincipalID)
	reader, _ := roles.Lookup("pg_monitor")
	other, _ := roles.Lookup("pg_read_all_stats")
	sources := map[authorization.PrincipalRef]bool{reader.Ref: true}
	entry := authorization.ACLEntry{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege}
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{entry}}
	next, changed, err := cleanupInitialACL(context.Background(), roles, acl, owner, sources)
	if err != nil || !changed || !next.Explicit || len(next.Entries) != 0 {
		t.Fatal("current-owner revoke did not produce empty historical ACL", next, changed, err)
	}
	if !reflect.DeepEqual(acl.Entries, []authorization.ACLEntry{entry}) {
		t.Fatal("cleanup mutated retained ACL backing")
	}
	// ALTER OWNER leaves the historical grantor untouched. DROP OWNED must
	// not silently adopt it as the new owner's grant and erase the dependency.
	next, changed, err = cleanupInitialACL(context.Background(), roles, acl, other.Ref, sources)
	if err != nil || changed || &next.Entries[0] != &acl.Entries[0] {
		t.Fatal("old-owner entry was rewritten or copied", next, changed, err)
	}
	dependent := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
		{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege, GrantOptions: authorization.SelectPrivilege},
		{Grantor: reader.Ref, Grantee: authorization.ACLGrantee{Role: other.Ref}, Privileges: authorization.SelectPrivilege},
	}}
	next, changed, err = cleanupInitialACL(context.Background(), roles, dependent, owner, sources)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "2BP01" || diagnostic.Message != "dependent privileges exist" || diagnostic.Hint != "Use CASCADE to revoke them too." || changed || &next.Entries[0] != &dependent.Entries[0] {
		t.Fatal("initial cleanup did not retain RESTRICT/atomicity", next, changed, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, changed, err := cleanupInitialACL(ctx, roles, dependent, owner, sources); !errors.Is(err, context.Canceled) || changed {
		t.Fatal("canceled historical cleanup produced a candidate", changed, err)
	}
}
