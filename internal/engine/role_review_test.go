package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func TestAuthorizationNamespaceLookupCheckpoint(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER reader")
	roleExecute(t, e, op, "CREATE TABLE secret(id INT)")
	roleExecute(t, e, op, "INSERT INTO secret VALUES(7)")
	ns := e.catalog.domain.Ref(e.catalog.state.epoch, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID})
	if err := e.SetObjectACL(context.Background(), ns, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
	catalog, roles := e.catalog.state, e.roles
	for _, tc := range []struct{ query, code, message, position string }{
		{"SELECT id FROM public.secret", "42501", "permission denied for schema public", "16"},
		{"SELECT missing FROM public.secret", "42501", "permission denied for schema public", "21"},
		{"INSERT INTO public.secret VALUES('bad')", "42501", "permission denied for schema public", "13"},
		{"INSERT INTO public.secret VALUES(9)", "42501", "permission denied for schema public", "13"},
		{"INSERT INTO public.secret VALUES($1)", "42501", "permission denied for schema public", "13"},
		{"INSERT INTO public.secret(missing) VALUES(2147483648)", "42501", "permission denied for schema public", "13"},
		{"SELECT id FROM secret", "42P01", `relation "secret" does not exist`, "16"},
		{"SELECT missing FROM secret", "42P01", `relation "secret" does not exist`, "21"},
		{"INSERT INTO secret VALUES('bad')", "42P01", `relation "secret" does not exist`, "13"},
		{"INSERT INTO secret VALUES(9)", "42P01", `relation "secret" does not exist`, "13"},
		{"INSERT INTO secret VALUES($1)", "42P01", `relation "secret" does not exist`, "13"},
		{"INSERT INTO secret(missing) VALUES(2147483648)", "42P01", `relation "secret" does not exist`, "13"},
	} {
		for _, prepare := range []bool{false, true} {
			stmt := statement(t, tc.query)
			var err error
			if prepare {
				_, err = e.Describe(op, stmt)
			} else {
				_, err = e.Execute(op, stmt, 0, nil)
			}
			var diagnostic *sqlstate.Error
			want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: tc.position}
			if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
				t.Fatalf("prepare=%v %s: %#v", prepare, tc.query, err)
			}
			if e.catalog.state != catalog || e.roles != roles {
				t.Fatal("failed lookup published state")
			}
		}
	}
	// Lookup privileges and table ACLs are different checkpoints. A fresh
	// view sees USAGE restored, Prepare succeeds, and Execute still denies.
	bootstrap := e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	if err := e.SetObjectACL(context.Background(), ns, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: bootstrap, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege}}}); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"SELECT id FROM public.secret", "INSERT INTO secret VALUES(9)"} {
		if _, err := e.Describe(op, statement(t, query)); err != nil {
			t.Fatal("ordinary ACL checked during Prepare", err)
		}
		roleError(t, e, op, query, "42501", "permission denied for table secret", "")
	}
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	if got := tuples(roleExecute(t, e, op, "SELECT id FROM secret")); !reflect.DeepEqual(got, [][]driver.Value{{int64(7)}}) {
		t.Fatal("namespace failures changed sentinel", got)
	}
}

func TestRoleColumnDependencyConsumerBoundary(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		e := New()
		op, done := roleOperation(t, e, "postgres")
		func() {
			defer done()
			roleExecute(t, e, op, "CREATE USER reader")
			roleExecute(t, e, op, `CREATE TABLE "odd table"("odd column" INT)`)
			roleExecute(t, e, op, `INSERT INTO "odd table" VALUES(7)`)
			ref := roleTableRef(t, e, `"odd table"`)
			ref.Address.SubID = 1
			reader, _ := e.Roles().Lookup("reader")
			acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege}}}
			if err := e.SetObjectACL(context.Background(), ref, acl); err != nil {
				t.Fatal(err)
			}
			roleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
			query := `SELECT "odd column" FROM "odd table"`
			if prepared {
				if _, err := e.Describe(op, statement(t, query)); err != nil {
					t.Fatal(err)
				}
			}
			if got := tuples(roleExecute(t, e, op, query)); !reflect.DeepEqual(got, [][]driver.Value{{int64(7)}}) {
				t.Fatal(got)
			}
			roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
			if prepared {
				if _, err := e.Describe(op, statement(t, "DROP ROLE reader")); err != nil {
					t.Fatal(err)
				}
			}
			roleError(t, e, op, "DROP ROLE reader", "2BP01", `role "reader" cannot be dropped because some objects depend on it`, `privileges for column odd column of table "odd table"`)
			if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{}); err != nil {
				t.Fatal(err)
			}
			roleExecute(t, e, op, "DROP ROLE reader")
		}()
	}
}

func TestRoleDependencyAddressOrderAndQuoting(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		e := New()
		op, done := roleOperation(t, e, "postgres")
		func() {
			defer done()
			roleExecute(t, e, op, "CREATE USER owner")
			roleExecute(t, e, op, "SET SESSION AUTHORIZATION owner")
			var previous ids.OID
			for _, name := range []string{"z", "a", `"odd table"`, `"select"`, `"quote""table"`, `"integer"`, `"Mixed"`} {
				withPublicSchemaUnitOwner(t, e, "owner", func() {
					roleExecute(t, e, op, "CREATE TABLE "+name+"(id INT)")
				})
				ref := roleTableRef(t, e, name)
				if ref.Address.ObjectID <= previous {
					t.Fatal("creation identity ordering", ref, previous)
				}
				previous = ref.Address.ObjectID
			}
			roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
			if prepared {
				if _, err := e.Describe(op, statement(t, "DROP ROLE owner")); err != nil {
					t.Fatal(err)
				}
			}
			roleError(t, e, op, "DROP ROLE owner", "2BP01", `role "owner" cannot be dropped because some objects depend on it`, "owner of table z\nowner of table a\nowner of table \"odd table\"\nowner of table \"select\"\nowner of table \"quote\"\"table\"\nowner of table \"integer\"\nowner of table \"Mixed\"")
		}()
	}
}

func TestAuthorizationPublicGrantOptionsRejectAtomically(t *testing.T) {
	ctx := context.Background()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE TABLE sentinel(id INT)")
	roleExecute(t, e, op, "INSERT INTO sentinel VALUES(7)")
	ref := roleTableRef(t, e, "sentinel")
	beforeCatalog, beforeRoles := e.catalog.state, e.roles
	beforeOID, beforeRevision := e.catalog.nextOID, e.catalog.nextRevision
	original := e.catalog.state.descriptors[ref.Address].(*relationDescriptor)
	dependencies := descriptorReferences(original)
	invalid := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege, GrantOptions: authorization.SelectPrivilege}}}
	unchanged := func() {
		t.Helper()
		if e.catalog.state != beforeCatalog || e.roles != beforeRoles || e.catalog.nextOID != beforeOID || e.catalog.nextRevision != beforeRevision || !reflect.DeepEqual(descriptorReferences(e.catalog.state.descriptors[ref.Address]), dependencies) {
			t.Fatal("invalid PUBLIC authority changed catalog/revisions/dependencies")
		}
		if got := tuples(roleExecute(t, e, op, "SELECT id FROM sentinel")); !reflect.DeepEqual(got, [][]driver.Value{{int64(7)}}) {
			t.Fatal(got)
		}
	}
	namespace := e.catalog.domain.Ref(e.catalog.state.epoch, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID})
	namespaceACL := invalid
	namespaceACL.Entries = append([]authorization.ACLEntry(nil), invalid.Entries...)
	namespaceACL.Entries[0].Privileges = authorization.UsagePrivilege
	namespaceACL.Entries[0].GrantOptions = authorization.UsagePrivilege
	if err := e.SetObjectACL(ctx, namespace, namespaceACL); err == nil || !strings.Contains(err.Error(), "PUBLIC grant options") {
		t.Fatal("namespace mutation accepted PUBLIC grant options", err)
	}
	unchanged()
	namespaceDescriptor := *e.catalog.state.descriptors[namespace.Address].(*namespaceDescriptor)
	namespaceDescriptor.acl = namespaceACL
	namespaceView := cloneCatalog(e.catalog.state)
	namespaceView.descriptors[namespace.Address] = &namespaceDescriptor
	if _, err := e.ReplaceCatalog(ctx, e, e.catalog.read(namespaceView)); err == nil || !strings.Contains(err.Error(), "PUBLIC grant options") {
		t.Fatal("namespace replacement accepted PUBLIC grant options", err)
	}
	unchanged()
	for _, column := range []bool{false, true} {
		object := ref
		if column {
			object.Address.SubID = 1
		}
		if err := e.SetObjectACL(ctx, object, invalid); err == nil || !strings.Contains(err.Error(), "PUBLIC grant options") {
			t.Fatal("mutation accepted PUBLIC grant options", err)
		}
		unchanged()
		bad := *original
		if column {
			bad.columnACL = map[int32]authorization.ACLValue{1: invalid}
		} else {
			bad.acl = invalid
		}
		view := cloneCatalog(e.catalog.state)
		view.descriptors[ref.Address] = &bad
		if _, err := e.ReplaceCatalog(ctx, e, e.catalog.read(view)); err == nil || !strings.Contains(err.Error(), "PUBLIC grant options") {
			t.Fatal("replacement accepted PUBLIC grant options", err)
		}
		unchanged()
		stage := e.BeginCatalogStage(e.CatalogRead())
		bad.address.ObjectID = e.catalog.nextOID
		bad.name = "invalid_stage"
		if err := stage.Create(&bad); err != nil {
			t.Fatal(err)
		}
		if _, err := e.ValidateCatalogStage(ctx, stage); err == nil || !strings.Contains(err.Error(), "PUBLIC grant options") {
			t.Fatal("stage accepted PUBLIC grant options", err)
		}
		unchanged()
		// A foreign immutable catalog payload exercises principal remapping and
		// the shared validator without publishing the impossible source ACL.
		source := New()
		sourceOp, sourceDone := roleOperation(t, source, "postgres")
		roleExecute(t, source, sourceOp, "CREATE TABLE imported(id INT)")
		foreignRef := roleTableRef(t, source, "imported")
		foreign := *source.catalog.state.descriptors[foreignRef.Address].(*relationDescriptor)
		foreignACL := invalid
		foreignACL.Entries = append([]authorization.ACLEntry(nil), invalid.Entries...)
		foreignACL.Entries[0].Grantor = source.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
		if column {
			foreign.columnACL = map[int32]authorization.ACLValue{1: foreignACL}
		} else {
			foreign.acl = foreignACL
		}
		foreignView := cloneCatalog(source.catalog.state)
		foreignView.descriptors[foreignRef.Address] = &foreign
		_, err := e.ReplaceCatalog(ctx, source, source.catalog.read(foreignView))
		sourceDone()
		if err == nil || !strings.Contains(err.Error(), "PUBLIC grant options") {
			t.Fatal("import accepted PUBLIC grant options", err)
		}
		unchanged()
	}
}

func TestRoleLaterDependentBatchPreservesEndpoints(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		e := New()
		op, done := roleOperation(t, e, "postgres")
		func() {
			defer done()
			for _, name := range []string{"endpoint", "member", "owner"} {
				roleExecute(t, e, op, "CREATE ROLE "+name)
			}
			lookup := func(name string) authorization.PrincipalRef { d, _ := e.Roles().Lookup(name); return d.Ref }
			edge := authorization.Membership{Role: lookup("endpoint"), Member: lookup("member"), Grantor: lookup("postgres"), Inherit: true, Set: true}
			expectedEdges := append(e.Roles().Memberships(), edge)
			if err := grantMembership(t, e, edge); err != nil {
				t.Fatal(err)
			}
			roleExecute(t, e, op, "SET SESSION AUTHORIZATION owner")
			withPublicSchemaUnitOwner(t, e, "owner", func() {
				roleExecute(t, e, op, "CREATE TABLE sentinel(id INT)")
			})
			roleExecute(t, e, op, "INSERT INTO sentinel VALUES(7)")
			roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
			roles, catalog := e.roles, e.catalog.state
			beforeAuth, err := op.Authorization()
			if err != nil {
				t.Fatal(err)
			}
			beforeOID, beforeRevision, beforePrincipal := e.catalog.nextOID, e.catalog.nextRevision, e.nextPrincipal
			if prepared {
				if _, err := e.Describe(op, statement(t, "DROP ROLE endpoint,owner")); err != nil {
					t.Fatal(err)
				}
			}
			roleError(t, e, op, "DROP ROLE endpoint,owner", "2BP01", `role "owner" cannot be dropped because some objects depend on it`, "owner of table sentinel")
			if e.roles != roles || e.catalog.state != catalog || !reflect.DeepEqual(e.Roles().Memberships(), expectedEdges) {
				t.Fatal("later dependency changed state")
			}
			afterAuth, err := op.Authorization()
			if err != nil || afterAuth.Stamp() != beforeAuth.Stamp() || afterAuth.state != beforeAuth.state ||
				e.catalog.nextOID != beforeOID || e.catalog.nextRevision != beforeRevision || e.nextPrincipal != beforePrincipal {
				t.Fatal("later dependency changed authority/counters", err)
			}
			for _, name := range []string{"endpoint", "member", "owner"} {
				if _, ok := e.Roles().Lookup(name); !ok {
					t.Fatal("role removed", name)
				}
			}
			if got := tuples(roleExecute(t, e, op, "SELECT id FROM sentinel")); !reflect.DeepEqual(got, [][]driver.Value{{int64(7)}}) {
				t.Fatal("sentinel changed", got)
			}
		}()
	}
}

func TestRoleColumnDependencySubIDOrderAndOwnerSuppression(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		e := New()
		op, done := roleOperation(t, e, "postgres")
		func() {
			defer done()
			roleExecute(t, e, op, "CREATE USER owner")
			roleExecute(t, e, op, "CREATE USER reader")
			roleExecute(t, e, op, "SET SESSION AUTHORIZATION owner")
			withPublicSchemaUnitOwner(t, e, "owner", func() {
				roleExecute(t, e, op, "CREATE TABLE secret(a INT,b INT)")
			})
			roleExecute(t, e, op, "INSERT INTO secret VALUES(7,8)")
			ref := roleTableRef(t, e, "secret")
			reader, _ := e.Roles().Lookup("reader")
			owner := op.RoleState().CurrentUser
			acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
				{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege},
				{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege},
			}}
			column := ref
			column.Address.SubID = 1
			if err := e.SetObjectACL(context.Background(), column, acl); err != nil {
				t.Fatal(err)
			}
			roleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
			if prepared {
				if _, err := e.Describe(op, statement(t, "SELECT a FROM secret")); err != nil {
					t.Fatal(err)
				}
			}
			if got := tuples(roleExecute(t, e, op, "SELECT a FROM secret")); !reflect.DeepEqual(got, [][]driver.Value{{int64(7)}}) {
				t.Fatal(got)
			}
			roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
			if prepared {
				if _, err := e.Describe(op, statement(t, "DROP ROLE reader")); err != nil {
					t.Fatal(err)
				}
			}
			roleError(t, e, op, "DROP ROLE reader", "2BP01", `role "reader" cannot be dropped because some objects depend on it`, "privileges for column a of table secret")
			// Multiple column dependencies follow positive stable SubIDs, not
			// map iteration or duplicate ACL entry order.
			column.Address.SubID = 2
			if err := e.SetObjectACL(context.Background(), column, acl); err != nil {
				t.Fatal(err)
			}
			column.Address.SubID = 1
			if err := e.SetObjectACL(context.Background(), column, acl); err != nil {
				t.Fatal(err)
			}
			roleError(t, e, op, "DROP ROLE reader", "2BP01", `role "reader" cannot be dropped because some objects depend on it`, "privileges for column a of table secret\nprivileges for column b of table secret")
			roleError(t, e, op, "DROP ROLE owner", "2BP01", `role "owner" cannot be dropped because some objects depend on it`, "owner of table secret")
			for _, sub := range []int32{2, 1} {
				column.Address.SubID = sub
				if err := e.SetObjectACL(context.Background(), column, authorization.ACLValue{}); err != nil {
					t.Fatal(err)
				}
			}
			roleExecute(t, e, op, "DROP ROLE reader")
		}()
	}
}

func TestAuthorizationPublicOrdinaryAndRoleGrantOptions(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER reader")
	roleExecute(t, e, op, "CREATE TABLE secret(id INT)")
	roleExecute(t, e, op, "INSERT INTO secret VALUES(7)")
	ref := roleTableRef(t, e, "secret")
	grantor := e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: grantor, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege}}}
	if err := e.SetObjectACL(context.Background(), ref, acl); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
	view, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	options, err := view.Effective(ref, true)
	if err != nil || options != 0 {
		t.Fatal("PUBLIC implied delegation", options, err)
	}
	if got := tuples(roleExecute(t, e, op, "SELECT id FROM secret")); !reflect.DeepEqual(got, [][]driver.Value{{int64(7)}}) {
		t.Fatal(got)
	}
	reader := op.RoleState().CurrentUser
	acl.Entries[0].Grantee = authorization.ACLGrantee{Role: reader}
	acl.Entries[0].GrantOptions = authorization.SelectPrivilege
	if err := e.SetObjectACL(context.Background(), ref, acl); err != nil {
		t.Fatal("real role options rejected", err)
	}
	view, err = op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	options, err = view.Effective(ref, true)
	if err != nil || options != authorization.SelectPrivilege {
		t.Fatal("real role options lost", options, err)
	}
	oldOptions, err := view.Effective(ref, false)
	if err != nil || oldOptions != authorization.SelectPrivilege {
		t.Fatal(oldOptions, err)
	}
}

func TestRoleTypedIdentityCatalogAndTransaction(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER typed_owner")
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION typed_owner")
	withPublicSchemaUnitOwner(t, e, "typed_owner", func() {
		roleExecute(t, e, op, "CREATE TABLE typed_owned(id INT NOT NULL)")
	})
	roleExecute(t, e, op, "INSERT INTO typed_owned VALUES(7)")
	query := statement(t, "SELECT session_user,current_user")
	description, err := e.Describe(op, query)
	if err != nil {
		t.Fatal(err)
	}
	typ, ok := e.CatalogRead().LookupType(19)
	if !ok || typ.ID() != sqltype.Name || typ.DatabaseName() != "NAME" {
		t.Fatal("missing catalog NAME identity", typ)
	}
	for i, label := range []string{"session_user", "current_user"} {
		column := description.Results[i]
		if column.Name != label || column.Usage != typ.Usage() || column.OriginTable != 0 || column.NullabilityKnown {
			t.Fatal("identity description", column)
		}
	}
	ref, err := e.Begin(op, TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	retained := roleExecute(t, e, op, "SELECT session_user,current_user")
	defer retained.Rows.Close()
	roleExecute(t, e, op, "INSERT INTO typed_owned VALUES(8)")
	if got := tuples(roleExecute(t, e, op, "SELECT id FROM typed_owned ORDER BY id")); !reflect.DeepEqual(got, [][]driver.Value{{int64(7)}, {int64(8)}}) {
		t.Fatal("owner private rows", got)
	}
	peer, peerDone := roleOperation(t, e, "postgres")
	defer peerDone()
	if got := tuples(roleExecute(t, e, peer, "SELECT id FROM typed_owned")); !reflect.DeepEqual(got, [][]driver.Value{{int64(7)}}) {
		t.Fatal("peer saw private row", got)
	}
	if _, err := e.Rollback(op, ref); err != nil {
		t.Fatal(err)
	}
	roleIdentity(t, e, op, "typed_owner")
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := e.Restore(context.Background(), strings.NewReader("SET SESSION AUTHORIZATION 'typed_owner'; CREATE TABLE typed_owned(id INT NOT NULL); INSERT INTO typed_owned VALUES(9)")); err != nil {
		t.Fatal(err)
	}
	current, _ := e.CatalogRead().LookupType(19)
	if current == typ {
		t.Fatal("replacement reused type generation")
	}
	values := make([]sqltype.Value, 2)
	if present, err := retained.Rows.Next(context.Background(), values); !present || err != nil {
		t.Fatal(present, err)
	}
	for i, value := range values {
		text, err := value.Text()
		if err != nil || value.Type() != typ || text != "typed_owner" || retained.Rows.Schema()[i].Usage.Type != typ {
			t.Fatal("retained typed identity", value, err)
		}
	}
	roleIdentity(t, e, op, "typed_owner")
	// Host AST input shares normal NAME column admission and value semantics.
	withPublicSchemaUnitOwner(t, e, "typed_owner", func() {
		_, err = e.Execute(op, &parser.CreateTable{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "stored_name"}}}, Elements: []parser.TableElement{{Column: &parser.Column{Name: "id", TypeName: parser.TypeName{Name: "name"}}}}}, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
	})
	roleExecute(t, e, op, "INSERT INTO stored_name VALUES('MiXeD')")
	if got := tuples(roleExecute(t, e, op, "SELECT id FROM stored_name")); !reflect.DeepEqual(got, [][]driver.Value{{"MiXeD"}}) {
		t.Fatal("stored NAME value", got)
	}
	if got := tuples(roleExecute(t, e, op, "SELECT id FROM typed_owned")); !reflect.DeepEqual(got, [][]driver.Value{{int64(9)}}) {
		t.Fatal("NAME creation changed restored sentinel", got)
	}
}

func TestRoleCatalogPublicationAuthorityFenceAndRecovery(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER publisher")
	roleExecute(t, e, op, "CREATE USER member")
	publisher, _ := e.Roles().Lookup("publisher")
	member, _ := e.Roles().Lookup("member")
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION publisher")
	peer, peerDone := roleOperation(t, e, "postgres")
	defer peerDone()
	withPublicSchemaUnitOwner(t, e, "publisher", func() {
		entered, release := make(chan struct{}), make(chan struct{})
		released := false
		defer func() {
			if !released {
				close(release)
			}
		}()
		e.catalog.providers[ids.RelationClassID] = &publicationConflictProvider{
			CatalogProvider: e.catalog.providers[ids.RelationClassID],
			publishWinner: func(Descriptor) {
				close(entered)
				<-release
				if err := grantMembership(t, e, authorization.Membership{Role: publisher.Ref, Member: member.Ref, Grantor: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres")}); err != nil {
					t.Error(err)
				}
			},
		}
		query := statement(t, "CREATE TABLE fenced_role(id INT NOT NULL)")
		returned := make(chan error, 1)
		go func() {
			_, err := e.Execute(op, query, 0, nil)
			returned <- err
		}()
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("catalog provider gate not reached")
		}
		peerReturned := make(chan error, 1)
		go func() {
			// While provider validation is gated, session settings, authorization
			// views and explicit transaction identity work must remain independent.
			_, err := e.Execute(peer, statement(t, "SHOW application_name"), 0, nil)
			if err == nil {
				_, err = peer.Authorization()
			}
			var ref ids.TxRef
			if err == nil {
				ref, err = e.Begin(peer, TxOptions{})
			}
			if err == nil {
				var result Result
				result, err = e.Execute(peer, statement(t, "SELECT current_user"), 0, nil)
				if err == nil && !reflect.DeepEqual(tuples(result), [][]driver.Value{{"postgres"}}) {
					err = errors.New("peer transaction identity changed")
				}
				if result.Rows != nil {
					result.Rows.Close()
				}
				_, rollbackErr := e.Rollback(peer, ref)
				if err == nil {
					err = rollbackErr
				}
			}
			peerReturned <- err
		}()
		select {
		case err := <-peerReturned:
			if err != nil {
				t.Fatal("peer work while provider gated", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("provider validation held lifecycle or engine locks")
		}
		close(release)
		released = true
		select {
		case err := <-returned:
			if err == nil || err.Error() != "stuntgres: authorization changed before publication" {
				t.Fatal("authority fence", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("catalog publication did not release lifecycle/authority locks")
		}
		if found := e.CatalogRead().Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "fenced_role"}); len(found) != 0 || op.session.TransactionState() != TxIdle {
			t.Fatal("failed publication leaked table or transaction", found)
		}
		roleIdentity(t, e, op, "publisher")
		roleExecute(t, e, op, "CREATE TABLE fenced_role(id INT NOT NULL)")
		roleExecute(t, e, op, "INSERT INTO fenced_role VALUES(7)")
		roleError(t, e, op, "INSERT INTO fenced_role VALUES(2147483648)", "22003", "integer out of range", "")
		if got := tuples(roleExecute(t, e, op, "SELECT id FROM fenced_role")); !reflect.DeepEqual(got, [][]driver.Value{{int64(7)}}) {
			t.Fatal("post-fence publication", got)
		}
	})
}

func TestRoleUtilityInstallCompletion(t *testing.T) {
	for _, query := range []string{"CREATE ROLE installed", "DROP ROLE retained", "SET SESSION AUTHORIZATION selected", "RESET SESSION AUTHORIZATION"} {
		t.Run(query, func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			roleExecute(t, e, op, "CREATE ROLE retained")
			roleExecute(t, e, op, "CREATE USER selected")
			if query == "RESET SESSION AUTHORIZATION" {
				roleExecute(t, e, op, "SET SESSION AUTHORIZATION selected")
			}
			ctx, cancel := context.WithCancel(op.ctx)
			defer cancel()
			original := op.ctx
			op.ctx = ctx
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			_, err = scope.Execute(statement(t, query), 0, nil)
			if err != nil {
				_ = scope.Finish(err)
				t.Fatal(err)
			}
			cancel() // Successful utility installation cannot be retracted by Finish.
			err = scope.Finish(nil)
			op.ctx = original
			if err != nil || op.session.TransactionState() != TxIdle {
				t.Fatal("post-install cancellation retracted success", err)
			}
			switch query {
			case "CREATE ROLE installed":
				if _, ok := e.Roles().Lookup("installed"); !ok {
					t.Fatal("installed role missing")
				}
			case "DROP ROLE retained":
				if _, ok := e.Roles().Lookup("retained"); ok {
					t.Fatal("dropped role retained")
				}
			case "SET SESSION AUTHORIZATION selected":
				roleIdentity(t, e, op, "selected")
			case "RESET SESSION AUTHORIZATION":
				roleIdentity(t, e, op, "postgres")
			}
		})
	}
}
