package engine

import (
	"context"
	"database/sql/driver"
	"reflect"
	"strconv"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestRangePartitionInheritedTypeUsageAndCookedSchemaIdentity(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, query := range []string{
		"CREATE ROLE actor LOGIN",
		"CREATE SCHEMA private_types",
		"CREATE TYPE private_types.state AS ENUM('low','high')",
		"CREATE DOMAIN private_types.quantity AS INT",
	} {
		roleExecute(t, e, op, query)
	}
	actor, ok := e.Roles().Lookup("actor")
	if !ok {
		t.Fatal("actor missing")
	}
	view := e.CatalogRead()
	namespaces := view.Lookup(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "private_types"})
	if len(namespaces) != 1 {
		t.Fatal(namespaces)
	}
	namespace := e.catalog.Domain.Ref(view.Epoch(), namespaces[0])
	typeRefs := make([]ids.ObjectRef, 2)
	for i, name := range []string{"state", "quantity"} {
		addresses := view.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: namespaces[0], Name: name})
		if len(addresses) != 1 {
			t.Fatal(addresses)
		}
		typeRefs[i] = e.catalog.Domain.Ref(view.Epoch(), addresses[0])
	}
	setACL := func(ref ids.ObjectRef, acl authorization.ACLValue) error {
		catalog := e.CatalogRead()
		descriptor, ok := catalog.Descriptor(ref.Address)
		if !ok {
			t.Fatal("ACL fixture descriptor missing", ref)
		}
		if descriptor.Kind() == ids.NamespaceObject {
			return e.SetObjectACL(context.Background(), ref, acl)
		}
		typ, ok := descriptor.(*typeDescriptor)
		if !ok {
			t.Fatal("ACL fixture is not a user type", descriptor)
		}
		stage := e.BeginCatalogStage(catalog)
		next := *typ
		next.ACL = acl
		next.ACLSyntheticDefault = false
		next.revision++
		if err := stage.Replace(&next); err != nil {
			return err
		}
		domainUnitPublish(t, e, stage)
		return nil
	}
	grant := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: op.RoleState().CurrentUser, Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: authorization.UsagePrivilege}}}
	for _, ref := range append([]ids.ObjectRef{namespace}, typeRefs...) {
		if err := setACL(ref, grant); err != nil {
			t.Fatal(err)
		}
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION actor")
	roleExecute(t, e, op, "SET search_path=private_types,public")
	// Destination is explicitly public; namespace authority on stored type OIDs
	// and cooked expressions is distinct from fresh qualified name binding.
	withPublicSchemaUnitOwner(t, e, "actor", func() {
		roleExecute(t, e, op, "CREATE TABLE public.root(k INT,state private_types.state DEFAULT 'low'::private_types.state,quantity private_types.quantity DEFAULT 11,CONSTRAINT state_ok CHECK(state<>'high'::private_types.state)) PARTITION BY RANGE(k)")
	})
	parent := rangePartitionRelation(t, e, "public.root")
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION postgres")
	if err := setACL(typeRefs[0], authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION actor")
	withPublicSchemaUnitOwner(t, e, "actor", func() {
		roleError(t, e, op, "CREATE TABLE public.enum_denied PARTITION OF public.root FOR VALUES FROM(0) TO(1)", "42501", "permission denied for type state", "")
	})
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION postgres")
	if err := setACL(typeRefs[0], grant); err != nil {
		t.Fatal(err)
	}
	if err := setACL(typeRefs[1], authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION actor")
	withPublicSchemaUnitOwner(t, e, "actor", func() {
		roleError(t, e, op, "CREATE TABLE public.domain_denied PARTITION OF public.root FOR VALUES FROM(0) TO(1)", "42501", "permission denied for type quantity", "")
	})
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION postgres")
	if err := setACL(typeRefs[1], grant); err != nil {
		t.Fatal(err)
	}
	if err := setACL(namespace, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION actor")
	withPublicSchemaUnitOwner(t, e, "actor", func() {
		roleExecute(t, e, op, "CREATE TABLE public.leaf PARTITION OF public.root FOR VALUES FROM(0) TO(10)")
	})
	child := rangePartitionRelation(t, e, "public.leaf")
	if child.Columns[1].Usage != parent.Columns[1].Usage || child.Columns[2].Usage != parent.Columns[2].Usage || child.Columns[1].OriginTable != uint32(child.address.ObjectID) || child.Columns[1].OriginTable == parent.Columns[1].OriginTable {
		t.Fatal("inherited identity/origin admission did not copy type and refresh child origin")
	}
	if len(child.CheckConstraints) != 1 || child.CheckConstraints[0].OID == parent.CheckConstraints[0].OID {
		t.Fatal("inherited CHECK identity was not refreshed")
	}
	roleExecute(t, e, op, "INSERT INTO public.root(k) VALUES(1)")
	if !reflect.DeepEqual(tuples(roleExecute(t, e, op, "SELECT k,state::TEXT,quantity::INT FROM public.root")), [][]driver.Value{{int64(1), "low", int64(11)}}) {
		t.Fatal("cooked inherited default/CHECK/type identity failed after schema USAGE revocation")
	}
	for _, query := range []string{
		"CREATE TABLE public.cast_denied PARTITION OF public.root(state DEFAULT 'low'::private_types.state) FOR VALUES FROM(10) TO(20)",
		"CREATE TABLE public.check_denied PARTITION OF public.root(CONSTRAINT fresh CHECK(state<>'high'::private_types.state)) FOR VALUES FROM(10) TO(20)",
	} {
		position := strconv.Itoa(strings.Index(query, "private_types") + 1)
		withPublicSchemaUnitOwner(t, e, "actor", func() {
			roleErrorPosition(t, e, op, query, "42501", "permission denied for schema private_types", "", position)
		})
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION postgres")
	for _, name := range []string{"enum_denied", "domain_denied", "cast_denied", "check_denied"} {
		if len(e.CatalogRead().Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: name})) != 0 {
			t.Fatal("failed inherited authority published relation", name)
		}
	}
	if len(parent.Partition.Children) != 0 || len(rangePartitionRelation(t, e, "public.root").Partition.Children) != 1 {
		t.Fatal("failure or success mutated retained topology incorrectly")
	}
}

func TestRangePartitionNamedTargetAuthorityDoesNotDemandLeafACL(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, query := range []string{"CREATE ROLE writer LOGIN", "CREATE TABLE root(k INT) PARTITION BY RANGE(k)", "CREATE TABLE leaf PARTITION OF root DEFAULT"} {
		roleExecute(t, e, op, query)
	}
	writer, _ := e.Roles().Lookup("writer")
	rootRef := roleTableRef(t, e, "root")
	leafRef := roleTableRef(t, e, "leaf")
	if err := e.SetObjectACL(context.Background(), rootRef, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: op.RoleState().CurrentUser, Grantee: authorization.ACLGrantee{Role: writer.Ref}, Privileges: authorization.SelectPrivilege | authorization.InsertPrivilege}}}); err != nil {
		t.Fatal(err)
	}
	if err := e.SetObjectACL(context.Background(), leafRef, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION writer")
	roleExecute(t, e, op, "INSERT INTO root VALUES(1)")
	if !reflect.DeepEqual(tuples(roleExecute(t, e, op, "SELECT k FROM root")), [][]driver.Value{{int64(1)}}) {
		t.Fatal("root ACL did not authorize internal physical demand")
	}
	roleError(t, e, op, "SELECT k FROM leaf", "42501", "permission denied for table leaf", "")
	roleError(t, e, op, "INSERT INTO leaf VALUES(2)", "42501", "permission denied for table leaf", "")
	if !reflect.DeepEqual(tuples(roleExecute(t, e, op, "SELECT k FROM root")), [][]driver.Value{{int64(1)}}) {
		t.Fatal("denied named leaf insert mutated routing state")
	}
}
