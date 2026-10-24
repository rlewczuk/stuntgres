package engine

import (
	"context"
	"stuntgres/internal/engine/access"
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
	ref := e.catalog.Domain.Ref(e.catalog.State.Epoch(), d.Address())
	admin, done := roleOperation(t, e, "postgres")
	defer done()
	role := roleAttributeCreate(t, e, admin, "class_metadata_reader", true, parser.RoleOptions{})
	reader, readerDone := roleOperation(t, e, role.Name)
	defer readerDone()
	requirement := access.Requirement{Object: ref, Privileges: authorization.SelectPrivilege}
	retained, err := operationAuthorization(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := retained.Check(requirement); err != nil {
		t.Fatal("initial PUBLIC catalog permission", err)
	}
	if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	denied, err := operationAuthorization(reader)
	if err != nil {
		t.Fatal(err)
	}
	domainUnitDiagnostic(t, denied.Check(requirement), "42501", "permission denied for table pg_class")
	if err := retained.Check(requirement); err != nil {
		t.Fatal("ACL replacement mutated retained authorization", err)
	}
	current := e.CatalogRead().MustRelation(d.Address())
	if current == d || len(current.Security.ACL.Entries) != 0 || !current.Security.ACL.Explicit || len(d.Security.ACL.Entries) != 1 || !d.Security.ACL.Entries[0].Grantee.Public {
		t.Fatal("ACL publication/source-root immutability", current.Security.ACL, d.Security.ACL)
	}
	if err := e.SetObjectACL(context.Background(), ref, d.Security.ACL); err != nil {
		t.Fatal(err)
	}
	restored, err := operationAuthorization(reader)
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
