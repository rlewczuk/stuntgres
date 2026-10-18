package engine

import (
	"context"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
	"testing"
)

// Catalog GRANT/REVOKE SQL is not admitted natively. Exercise the actual owning
// ACL publication/check seam, including fresh use of the same operation and
// immutable retained authorization, rather than inventing successful SQL.
func TestPgClassCatalogACLRecheck(t *testing.T) {
	e := New()
	d := pgClassUnitSource(t, e)
	ref := ObjectRef{owner: e.domain, Epoch: e.state.epoch, Address: d.address}
	admin, done := roleOperation(t, e, "postgres")
	defer done()
	role := roleAttributeCreate(t, e, admin, "class_metadata_reader", true, parser.RoleOptions{})
	reader, readerDone := roleOperation(t, e, role.Name)
	defer readerDone()
	requirement := AccessRequirement{Object: ref, Privileges: authorization.SelectPrivilege}
	retained, err := reader.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	if err := retained.Check(requirement); err != nil {
		t.Fatal("initial PUBLIC catalog permission", err)
	}
	if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	denied, err := reader.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	domainUnitDiagnostic(t, denied.Check(requirement), "42501", "permission denied for table pg_class")
	if err := retained.Check(requirement); err != nil {
		t.Fatal("ACL replacement mutated retained authorization", err)
	}
	current := e.CatalogRead().state.descriptors[d.address].(*relationDescriptor)
	if current == d || len(current.acl.Entries) != 0 || !current.acl.Explicit || len(d.acl.Entries) != 1 || !d.acl.Entries[0].Grantee.Public {
		t.Fatal("ACL publication/source-root immutability", current.acl, d.acl)
	}
	if err := e.SetObjectACL(context.Background(), ref, d.acl); err != nil {
		t.Fatal(err)
	}
	restored, err := reader.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.Check(requirement); err != nil {
		t.Fatal("restored PUBLIC catalog permission", err)
	}
	domainUnitDiagnostic(t, denied.Check(requirement), "42501", "permission denied for table pg_class")
	if err := retained.Check(requirement); err != nil {
		t.Fatal("restoration mutated retained PUBLIC authorization", err)
	}
}
