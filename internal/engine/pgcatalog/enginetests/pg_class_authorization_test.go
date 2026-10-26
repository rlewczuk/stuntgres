package enginetests_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// Exercise ACL publication and rechecks directly, including fresh use of the
// same operation and immutable retained authorization. Public catalog GRANT
// behavior is covered separately by the shared reference scenarios.
func TestPgClassCatalogACLRecheck(t *testing.T) {
	e := engine.New()
	d := pgClassUnitSource(t, e)
	ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.Address())
	admin, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	role := roleAttributeCreate(t, e, admin, "class_metadata_reader", true, parser.RoleOptions{})
	reader, readerDone := enginetest.RoleOperation(t, e, role.Name)
	defer readerDone()
	requirement := access.Requirement{Object: ref, Privileges: authorization.SelectPrivilege}
	retained, err := core.OperationAuthorization(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := retained.Check(requirement); err != nil {
		t.Fatal("initial PUBLIC catalog permission", err)
	}
	if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	denied, err := core.OperationAuthorization(reader)
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
	if err := enginetest.SetObjectACL(e, context.Background(), ref, d.Security.ACL); err != nil {
		t.Fatal(err)
	}
	restored, err := core.OperationAuthorization(reader)
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

func pgClassUnitSource(t *testing.T, e *core.Engine) *catalog.Relation {
	t.Helper()
	return e.CatalogRead().MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1259})
}
