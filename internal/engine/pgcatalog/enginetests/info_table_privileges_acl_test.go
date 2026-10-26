package enginetests_test

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/parser"
	"testing"
)

func TestInfoTablePrivilegesCatalogACLRecheck(t *testing.T) {
	f := newDefaultUnitFixture(t)
	e := f.e
	d := e.CatalogRead().MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.TablePrivilegesOID})
	ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.Address())
	admin, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	role := roleAttributeCreate(t, e, admin, "table_privileges_reader", true, parser.RoleOptions{})
	reader, readerDone := enginetest.RoleOperation(t, e, role.Name)
	defer readerDone()
	requirement := access.Requirement{Object: ref, Privileges: authorization.SelectPrivilege}
	retained, err := core.OperationAuthorization(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := retained.Check(requirement); err != nil {
		t.Fatal("PUBLIC SELECT", err)
	}
	if err := enginetest.SetObjectACL(e, t.Context(), ref, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	denied, err := core.OperationAuthorization(reader)
	if err != nil {
		t.Fatal(err)
	}
	domainUnitDiagnostic(t, denied.Check(requirement), "42501", "permission denied for view table_privileges")
	if err := retained.Check(requirement); err != nil {
		t.Fatal("retained ACL mutated", err)
	}
	current := e.CatalogRead().MustRelation(d.Address())
	if current == d || !current.Security.ACL.Explicit || len(current.Security.ACL.Entries) != 0 || len(d.Security.ACL.Entries) != 1 {
		t.Fatal("ACL roots not isolated")
	}
	if err := enginetest.SetObjectACL(e, t.Context(), ref, d.Security.ACL); err != nil {
		t.Fatal(err)
	}
	restored, err := core.OperationAuthorization(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.Check(requirement); err != nil {
		t.Fatal("restored SELECT", err)
	}
	domainUnitDiagnostic(t, denied.Check(requirement), "42501", "permission denied for view table_privileges")
	if err := retained.Check(requirement); err != nil {
		t.Fatal("restoration mutated retained ACL", err)
	}
	// Schema admission is separate from this globally readable matrix.
	namespace := e.CatalogRead().MustNamespace(d.Namespace())
	nsRef := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), namespace.Address())
	if err := restored.CanUse(nsRef); err != nil {
		t.Fatal("PUBLIC schema USAGE", err)
	}
	if err := enginetest.SetObjectACL(e, t.Context(), nsRef, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	noUsage, err := core.OperationAuthorization(reader)
	if err != nil {
		t.Fatal(err)
	}
	domainUnitDiagnostic(t, noUsage.CanUse(nsRef), "42501", "permission denied for schema information_schema")
	if err := restored.CanUse(nsRef); err != nil {
		t.Fatal("retained schema ACL mutated", err)
	}
	if err := enginetest.SetObjectACL(e, t.Context(), nsRef, namespace.ACL); err != nil {
		t.Fatal(err)
	}
	withUsage, err := core.OperationAuthorization(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := withUsage.CanUse(nsRef); err != nil {
		t.Fatal("restored schema USAGE", err)
	}
	domainUnitDiagnostic(t, noUsage.CanUse(nsRef), "42501", "permission denied for schema information_schema")
}
