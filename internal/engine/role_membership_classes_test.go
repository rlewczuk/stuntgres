package engine

import (
	"context"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestMembershipInheritedClassMasks(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER owner", "CREATE USER reader", "CREATE ROLE rights", "CREATE SCHEMA membership_classes AUTHORIZATION owner", "SET SESSION AUTHORIZATION owner"} {
		roleExecute(t, e, op, q)
	}
	withPublicSchemaUnitOwner(t, e, "owner", func() {
		for _, q := range []string{"CREATE TABLE class_table(a INT)", "CREATE SEQUENCE class_sequence", "CREATE TYPE class_enum AS ENUM ('one')", "CREATE DOMAIN class_domain AS INT", "CREATE VIEW class_view AS SELECT a FROM class_table"} {
			roleExecute(t, e, op, q)
		}
	})
	for _, q := range []string{"RESET SESSION AUTHORIZATION", "GRANT owner,rights TO reader"} {
		roleExecute(t, e, op, q)
	}
	owner, _ := e.Roles().Lookup("owner")
	rights, _ := e.Roles().Lookup("rights")
	var refs []ids.ObjectRef
	for _, tc := range []struct {
		class ids.OID
		name  string
	}{{ids.NamespaceClassID, "membership_classes"}, {ids.RelationClassID, "class_table"}, {ids.RelationClassID, "class_sequence"}, {ids.RelationClassID, "class_view"}, {ids.TypeClassID, "class_enum"}, {ids.TypeClassID, "class_domain"}} {
		key := ids.NameKey{ClassID: tc.class, Name: tc.name}
		if tc.class != ids.NamespaceClassID {
			key.Namespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
		}
		addresses := e.CatalogRead().Lookup(key)
		if len(addresses) != 1 {
			t.Fatal(tc, addresses)
		}
		ref := e.catalog.Domain.Ref(e.catalog.state.Epoch(), addresses[0])
		refs = append(refs, ref)
	}
	// Namespace/relation ACLs use their existing internal publication seam.
	// Type ACLs use the same validated immutable descriptor staging as domain
	// fixtures; this does not expand the production SetObjectACL capability.
	setACL := func(ref ids.ObjectRef, acl authorization.ACLValue) error {
		view, err := op.Authorization()
		if err != nil {
			return err
		}
		d, err := view.object(ref)
		if err != nil {
			return err
		}
		typ, ok := d.(*typeDescriptor)
		if !ok {
			return e.SetObjectACL(context.Background(), ref, acl)
		}
		next := *typ
		next.ObjectRevision++
		acl.Entries = append([]authorization.ACLEntry(nil), acl.Entries...)
		next.ACL = acl
		next.ACLSyntheticDefault = false
		stage := e.BeginCatalogStage(view.catalog)
		defer e.DiscardCatalogStage(stage)
		if err = stage.Replace(&next); err != nil {
			return err
		}
		prepared, err := e.ValidateCatalogStage(context.Background(), stage)
		if err != nil {
			return err
		}
		_, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()})
		return err
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
	for _, ref := range refs {
		view, err := op.Authorization()
		if err != nil {
			t.Fatal(err)
		}
		d, err := view.object(ref)
		if err != nil {
			t.Fatal(err)
		}
		mask := classPrivileges(d)
		got, err := view.Effective(ref, false)
		if err != nil || got != mask {
			t.Fatal(d.Name(), got, mask, err)
		}
		if err := view.Check(AccessRequirement{Object: ref, OwnerOnly: true}); err != nil {
			t.Fatal(d.Name(), err)
		}
		before := e.CatalogRead().State()
		invalid := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner.Ref, Grantee: authorization.ACLGrantee{Role: rights.Ref}, Privileges: authorization.Privileges(1 << 31)}}}
		if err := setACL(ref, invalid); err == nil || e.CatalogRead().State() != before {
			t.Fatal("invalid class mask published", d.Name(), err)
		}
		if err := setACL(ref, authorization.ACLValue{Explicit: true}); err != nil {
			t.Fatal(err)
		}
		view, err = op.Authorization()
		if err != nil {
			t.Fatal(err)
		}
		got, err = view.Effective(ref, false)
		if err != nil || got != 0 {
			t.Fatal("explicit-empty use", d.Name(), got, err)
		}
		got, err = view.Effective(ref, true)
		if err != nil || got != mask {
			t.Fatal("implicit owner options", d.Name(), got, mask, err)
		}
		acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner.Ref, Grantee: authorization.ACLGrantee{Role: rights.Ref}, Privileges: mask, GrantOptions: mask}}}
		if err := setACL(ref, acl); err != nil {
			t.Fatal(err)
		}
	}
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	roleExecute(t, e, op, "REVOKE INHERIT OPTION FOR owner FROM reader")
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
	for _, ref := range refs {
		view, err := op.Authorization()
		if err != nil {
			t.Fatal(err)
		}
		d, err := view.object(ref)
		if err != nil {
			t.Fatal(err)
		}
		mask := classPrivileges(d)
		if err := view.Check(AccessRequirement{Object: ref, Privileges: mask}); err != nil {
			t.Fatal("all inherited bits", d.Name(), err)
		}
		got, err := view.Effective(ref, true)
		if err != nil || got != mask {
			t.Fatal("inherited options", d.Name(), got, mask, err)
		}
		if err := view.Check(AccessRequirement{Object: ref, OwnerOnly: true}); err == nil {
			t.Fatal("nonowner ACL became ownership", d.Name())
		}
	}
}
