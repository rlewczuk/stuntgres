package partitioncmds_test

import (
	"context"
	"database/sql/driver"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func TestRangePartitionInheritedTypeUsageAndCookedSchemaIdentity(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, query := range []string{
		"CREATE ROLE actor LOGIN",
		"CREATE SCHEMA private_types",
		"CREATE TYPE private_types.state AS ENUM('low','high')",
		"CREATE DOMAIN private_types.quantity AS INT",
	} {
		enginetest.RoleExecute(t, e, op, query)
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
	namespace := e.Catalog.Domain.Ref(view.Epoch(), namespaces[0])
	typeRefs := make([]ids.ObjectRef, 2)
	for i, name := range []string{"state", "quantity"} {
		addresses := view.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: namespaces[0], Name: name})
		if len(addresses) != 1 {
			t.Fatal(addresses)
		}
		typeRefs[i] = e.Catalog.Domain.Ref(view.Epoch(), addresses[0])
	}
	setACL := func(ref ids.ObjectRef, acl authorization.ACLValue) error {
		catalogView := e.CatalogRead()
		descriptor, ok := catalogView.Descriptor(ref.Address)
		if !ok {
			t.Fatal("ACL fixture descriptor missing", ref)
		}
		if descriptor.Kind() == ids.NamespaceObject {
			return enginetest.SetObjectACL(e, context.Background(), ref, acl)
		}
		typ, ok := descriptor.(*catalog.Type)
		if !ok {
			t.Fatal("ACL fixture is not a user type", descriptor)
		}
		stage := e.BeginCatalogStage(catalogView)
		next := *typ
		next.ACL = acl
		next.ACLSyntheticDefault = false
		next.ObjectRevision++
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
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION actor")
	enginetest.RoleExecute(t, e, op, "SET search_path=private_types,public")
	// Destination is explicitly public; namespace authority on stored type OIDs
	// and cooked expressions is distinct from fresh qualified name binding.
	withPublicSchemaUnitOwner(t, e, "actor", func() {
		enginetest.RoleExecute(t, e, op, "CREATE TABLE public.root(k INT,state private_types.state DEFAULT 'low'::private_types.state,quantity private_types.quantity DEFAULT 11,CONSTRAINT state_ok CHECK(state<>'high'::private_types.state)) PARTITION BY RANGE(k)")
	})
	parent := rangePartitionRelation(t, e, "public.root")
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION postgres")
	if err := setACL(typeRefs[0], authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION actor")
	withPublicSchemaUnitOwner(t, e, "actor", func() {
		enginetest.RoleError(t, e, op, "CREATE TABLE public.enum_denied PARTITION OF public.root FOR VALUES FROM(0) TO(1)", "42501", "permission denied for type state", "")
	})
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION postgres")
	if err := setACL(typeRefs[0], grant); err != nil {
		t.Fatal(err)
	}
	if err := setACL(typeRefs[1], authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION actor")
	withPublicSchemaUnitOwner(t, e, "actor", func() {
		enginetest.RoleError(t, e, op, "CREATE TABLE public.domain_denied PARTITION OF public.root FOR VALUES FROM(0) TO(1)", "42501", "permission denied for type quantity", "")
	})
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION postgres")
	if err := setACL(typeRefs[1], grant); err != nil {
		t.Fatal(err)
	}
	if err := setACL(namespace, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION actor")
	withPublicSchemaUnitOwner(t, e, "actor", func() {
		enginetest.RoleExecute(t, e, op, "CREATE TABLE public.leaf PARTITION OF public.root FOR VALUES FROM(0) TO(10)")
	})
	child := rangePartitionRelation(t, e, "public.leaf")
	if child.Columns[1].Usage != parent.Columns[1].Usage || child.Columns[2].Usage != parent.Columns[2].Usage || child.Columns[1].OriginTable != uint32(child.Address().ObjectID) || child.Columns[1].OriginTable == parent.Columns[1].OriginTable {
		t.Fatal("inherited identity/origin admission did not copy type and refresh child origin")
	}
	if len(child.Constraints.Check) != 1 || child.Constraints.Check[0].OID == parent.Constraints.Check[0].OID {
		t.Fatal("inherited CHECK identity was not refreshed")
	}
	enginetest.RoleExecute(t, e, op, "INSERT INTO public.root(k) VALUES(1)")
	if !reflect.DeepEqual(enginetest.Tuples(enginetest.RoleExecute(t, e, op, "SELECT k,state::TEXT,quantity::INT FROM public.root")), [][]driver.Value{{int64(1), "low", int64(11)}}) {
		t.Fatal("cooked inherited default/CHECK/type identity failed after schema USAGE revocation")
	}
	for _, query := range []string{
		"CREATE TABLE public.cast_denied PARTITION OF public.root(state DEFAULT 'low'::private_types.state) FOR VALUES FROM(10) TO(20)",
		"CREATE TABLE public.check_denied PARTITION OF public.root(CONSTRAINT fresh CHECK(state<>'high'::private_types.state)) FOR VALUES FROM(10) TO(20)",
	} {
		position := strconv.Itoa(strings.Index(query, "private_types") + 1)
		withPublicSchemaUnitOwner(t, e, "actor", func() {
			enginetest.RoleErrorPosition(t, e, op, query, "42501", "permission denied for schema private_types", "", position)
		})
	}
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION postgres")
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
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, query := range []string{"CREATE ROLE writer LOGIN", "CREATE TABLE root(k INT) PARTITION BY RANGE(k)", "CREATE TABLE leaf PARTITION OF root DEFAULT"} {
		enginetest.RoleExecute(t, e, op, query)
	}
	writer, _ := e.Roles().Lookup("writer")
	rootRef := enginetest.RoleTableRef(t, e, "root")
	leafRef := enginetest.RoleTableRef(t, e, "leaf")
	if err := enginetest.SetObjectACL(e, context.Background(), rootRef, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: op.RoleState().CurrentUser, Grantee: authorization.ACLGrantee{Role: writer.Ref}, Privileges: authorization.SelectPrivilege | authorization.InsertPrivilege}}}); err != nil {
		t.Fatal(err)
	}
	if err := enginetest.SetObjectACL(e, context.Background(), leafRef, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION writer")
	enginetest.RoleExecute(t, e, op, "INSERT INTO root VALUES(1)")
	if !reflect.DeepEqual(enginetest.Tuples(enginetest.RoleExecute(t, e, op, "SELECT k FROM root")), [][]driver.Value{{int64(1)}}) {
		t.Fatal("root ACL did not authorize internal physical demand")
	}
	enginetest.RoleError(t, e, op, "SELECT k FROM leaf", "42501", "permission denied for table leaf", "")
	enginetest.RoleError(t, e, op, "INSERT INTO leaf VALUES(2)", "42501", "permission denied for table leaf", "")
	if !reflect.DeepEqual(enginetest.Tuples(enginetest.RoleExecute(t, e, op, "SELECT k FROM root")), [][]driver.Value{{int64(1)}}) {
		t.Fatal("denied named leaf insert mutated routing state")
	}
}

func domainUnitPublish(t *testing.T, e *core.Engine, stage *core.CatalogStage) {
	t.Helper()
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
}

// withPublicSchemaUnitOwner provisions only a positive CREATE or owner-successor
// precondition. It leaves the tested operation's identity and object ACLs alone.
func withPublicSchemaUnitOwner(t *testing.T, e *core.Engine, owner string, body func()) {
	t.Helper()
	view := e.CatalogRead()
	addresses := view.Lookup(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
	if len(addresses) != 1 {
		t.Fatal("public schema fixture requires one public namespace")
	}
	descriptor, ok := view.Descriptor(addresses[0])
	if !ok {
		t.Fatal("public schema fixture requires its namespace descriptor")
	}
	namespace, ok := descriptor.(*catalog.Namespace)
	if !ok {
		t.Fatal("public schema fixture has a non-namespace descriptor")
	}
	roles := e.Roles()
	prior, err := roles.Descriptor(namespace.Owner)
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, err := roles.Descriptor(e.RoleStore.Domain.Principal(10))
	if err != nil {
		t.Fatal(err)
	}
	login, err := e.ResolveLogin(bootstrap.Name)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := e.StartSession(context.Background(), session.SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Authenticated: login})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		sess.RequestEnd(session.CleanClose)
		if err := sess.WaitEnd(context.Background()); err != nil {
			t.Error("closing public schema fixture session:", err)
		}
	}()
	admin, err := sess.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Done()
	quote := func(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }
	// Restore while the borrowed operation and the prior owner are still live,
	// including when the body terminates through testing.T.Fatal.
	defer func() {
		restore, err := parser.Parse("ALTER SCHEMA public OWNER TO " + quote(prior.Name))
		if err != nil {
			t.Error("parsing public schema owner restoration:", err)
			return
		}
		if len(restore) != 1 {
			t.Errorf("expected single public schema owner restoration statement, got %d", len(restore))
			return
		}
		if _, err := e.Execute(admin, restore[0], 0, nil); err != nil {
			t.Error("restoring public schema owner:", err)
		}
	}()
	enginetest.RoleExecute(t, e, admin, "ALTER SCHEMA public OWNER TO "+quote(owner))
	body()
}

var testSessionID atomic.Uint64
