package core_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Host catalog fixtures resolve as the real bootstrap principal, not as a
// zero/default-allow authorization view. Caller holds e.mu.
func catalogAuthorization(e *core.Engine, view catalog.Read) core.AuthorizationView {
	return e.AuthorityView(access.View{Owner: &e.Catalog.Owner, PairedCatalog: view, RoleRecord: e.RoleStore.Published, Catalog: view, Roles: e.RoleStore.PublishedRead(&e.Catalog.Owner, view), State: authorization.RoleState{CurrentUser: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")}})
}

func roleTableRef(t *testing.T, e *core.Engine, name string) ids.ObjectRef {
	t.Helper()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	ref, err := e.ResolveReference(catalogAuthorization(e, e.Catalog.Current()), enginetest.Statement(t, "SELECT * FROM "+name).(*parser.Select).From.(*parser.TableReference).Name)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func TestAuthorizationACLAndExactColumnUses(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE USER owner")
	enginetest.RoleExecute(t, e, op, "CREATE USER reader")
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION owner")
	withPublicSchemaUnitOwner(t, e, "owner", func() { enginetest.RoleExecute(t, e, op, "CREATE TABLE secret(a INT,b INT,c INT)") })
	enginetest.RoleExecute(t, e, op, "INSERT INTO secret VALUES(7,8,9)")
	ref := roleTableRef(t, e, "secret")
	owner := op.RoleState().CurrentUser
	retained, err := core.OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	view, err := core.OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	if err := retained.Check(access.Requirement{Object: ref, Privileges: authorization.SelectPrivilege | authorization.InsertPrivilege}); err != nil {
		t.Fatal("old view changed", err)
	}
	if err := view.Check(access.Requirement{Object: ref, Privileges: authorization.SelectPrivilege}); err == nil {
		t.Fatal("explicit empty owner rights")
	}
	if err := view.Check(access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
		t.Fatal("owner authority revoked", err)
	}
	options, err := view.Effective(ref, true)
	if err != nil || options != authorization.TableGrantPrivileges {
		t.Fatal(options, err)
	}
	reader, _ := e.Roles().Lookup("reader")
	entry := authorization.ACLEntry{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege}
	column := ref
	column.Address.SubID = 1
	if err := enginetest.SetObjectACL(e, context.Background(), column, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{entry}}); err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
	enginetest.RoleExecute(t, e, op, "SELECT a FROM secret")
	for _, sql := range []string{"SELECT * FROM secret", "SELECT a FROM secret WHERE b=8", "SELECT a FROM secret ORDER BY c", "INSERT INTO secret(a) VALUES(10)"} {
		if _, err := e.Describe(op, enginetest.Statement(t, sql)); err != nil {
			t.Fatal("eager prepare denial", sql, err)
		}
		enginetest.RoleError(t, e, op, sql, "42501", "permission denied for table secret", "")
	}
	view, err = core.OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	if err := view.Check(access.Requirement{Object: column, Privileges: authorization.SelectPrivilege | authorization.InsertPrivilege}); err == nil {
		t.Fatal("any-bit check")
	}
	if err := view.Check(access.Requirement{Object: ref, OwnerOnly: true}); err == nil {
		t.Fatal("nonowner authority")
	}
	for _, req := range []access.Requirement{{Object: ref}, {Object: ref, Privileges: authorization.UsagePrivilege}, {Object: engine.New().Catalog.Domain.Ref(ref.Epoch, ref.Address), Privileges: authorization.SelectPrivilege}} {
		if err := view.Check(req); err == nil {
			t.Fatal("invalid requirement", req)
		}
	}
	entry.Grantee = authorization.ACLGrantee{Public: true}
	entry.Privileges = authorization.SelectPrivilege | authorization.InsertPrivilege
	if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{entry}}); err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, e, op, "SELECT a,b,c FROM secret")
	enginetest.RoleExecute(t, e, op, "INSERT INTO secret(a) VALUES(11)")
	enginetest.RoleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	enginetest.RoleExecute(t, e, op, "CREATE USER future")
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION future")
	enginetest.RoleExecute(t, e, op, "SELECT a FROM secret")
	enginetest.RoleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	var dump bytes.Buffer
	security := e.Catalog.State.MustRelation(ref.Address).Security
	if err := engine.Dump(context.Background(), e, &dump); err != nil {
		t.Fatal(err)
	}
	if err := engine.Restore(context.Background(), e, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	restored := e.Catalog.State.MustRelation(roleTableRef(t, e, "secret").Address)
	if !reflect.DeepEqual(restored.Security.ACL, security.ACL) || !reflect.DeepEqual(restored.Security.ColumnACL, security.ColumnACL) {
		t.Fatal("raw relation/column ACL changed across restore", restored.Security, security)
	}
	namespace := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID})
	entry.Privileges = authorization.UsagePrivilege
	entry.Grantor = e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	if err := enginetest.SetObjectACL(e, context.Background(), namespace, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{entry}}); err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION future")
	enginetest.RoleErrorPosition(t, e, op, "CREATE TABLE forbidden(id INT)", "42501", "permission denied for schema public", "", "14")
}

func TestAuthorizationImportRemapsACLAndDependencies(t *testing.T) {
	source, target := engine.New(), engine.New()
	a, doneA := enginetest.RoleOperation(t, source, "postgres")
	defer doneA()
	b, doneB := enginetest.RoleOperation(t, target, "postgres")
	defer doneB()
	for _, pair := range []struct {
		e  *core.Engine
		op *session.Operation
	}{{source, a}, {target, b}} {
		enginetest.RoleExecute(t, pair.e, pair.op, "CREATE USER owner")
		enginetest.RoleExecute(t, pair.e, pair.op, "CREATE USER reader")
	}
	enginetest.RoleExecute(t, source, a, "SET SESSION AUTHORIZATION owner")
	withPublicSchemaUnitOwner(t, source, "owner", func() { enginetest.RoleExecute(t, source, a, "CREATE TABLE secret(id INT)") })
	enginetest.RoleExecute(t, source, a, "INSERT INTO secret VALUES(7)")
	ref := roleTableRef(t, source, "secret")
	owner := a.RoleState().CurrentUser
	reader, _ := source.Roles().Lookup("reader")
	if err := enginetest.SetObjectACL(source, context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := target.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	targetRef := roleTableRef(t, target, "secret")
	target.Mu.Lock()
	d := target.Catalog.State.MustRelation(targetRef.Address)
	target.Mu.Unlock()
	for _, ref := range descriptorReferences(d) {
		if target.Roles().ValidatePrincipal(ref) != nil {
			t.Fatal("foreign principal survived import")
		}
	}
	enginetest.RoleExecute(t, target, b, "SET SESSION AUTHORIZATION reader")
	r := enginetest.RoleExecute(t, target, b, "SELECT id FROM secret")
	values := enginetest.Tuples(r)
	if !reflect.DeepEqual(values, [][]driver.Value{{int64(7)}}) {
		t.Fatal(values)
	}
	enginetest.RoleExecute(t, target, b, "RESET SESSION AUTHORIZATION")
	enginetest.RoleError(t, target, b, "DROP ROLE reader", "2BP01", `role "reader" cannot be dropped because some objects depend on it`, "privileges for table secret")
	unknown := engine.New()
	before := unknown.Catalog.State
	if _, err := unknown.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err == nil {
		t.Fatal("unknown owner import")
	}
	if unknown.Catalog.State != before {
		t.Fatal("failed import changed target")
	}
}

type roleBlockingReader struct {
	entered chan struct{}
	release chan struct{}
	text    string
}

func (r *roleBlockingReader) Read(p []byte) (int, error) {
	close(r.entered)
	<-r.release
	return strings.NewReader(r.text).Read(p)
}

// One blocked read is followed by EOF, so tests never sleep or leak I/O workers.
type roleGateReader struct {
	gate *roleBlockingReader
	read bool
}

func (r *roleGateReader) Read(p []byte) (int, error) {
	if r.read {
		return 0, io.EOF
	}
	r.read = true
	return r.gate.Read(p)
}
func TestAuthorizationRestoreRejectsRecreatedOwnerDuringIO(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE USER owner")
	enginetest.RoleExecute(t, e, op, "CREATE TABLE sentinel(id INT)")
	enginetest.RoleExecute(t, e, op, "INSERT INTO sentinel VALUES(7)")
	before := e.Catalog.State
	gate := &roleBlockingReader{entered: make(chan struct{}), release: make(chan struct{}),
		text: "SET SESSION AUTHORIZATION 'owner'; CREATE TABLE imported(id INT);"}
	result := make(chan error, 1)
	go func() { result <- engine.Restore(context.Background(), e, &roleGateReader{gate: gate}) }()
	released, finished := false, false
	defer func() {
		if !released {
			close(gate.release)
		}
		if !finished {
			select {
			case <-result:
			case <-time.After(5 * time.Second):
				t.Error("restore worker did not join")
			}
		}
	}()
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("restore did not enter read")
	}
	enginetest.RoleExecute(t, e, op, "DROP ROLE owner")
	enginetest.RoleExecute(t, e, op, "CREATE USER owner")
	enginetest.RoleExecute(t, e, op, "SELECT id FROM sentinel")
	close(gate.release)
	released = true
	select {
	case err := <-result:
		finished = true
		if err == nil {
			t.Fatal("recreated owner accepted")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("restore worker did not finish")
	}
	if e.Catalog.State != before {
		t.Fatal("restore failure changed catalog")
	}
}
func TestAuthorizationExhaustionAndCancellationDoNotPublish(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	before := e.RoleStore.Published
	e.RoleStore.InstallPrincipalFrontierLocked(^uint64(0))
	if _, err := e.Execute(op, enginetest.Statement(t, "CREATE ROLE exhausted"), 0, nil); err == nil {
		t.Fatal("principal exhaustion")
	}
	if e.RoleStore.Published != before {
		t.Fatal("exhaustion publication")
	}
	e.RoleStore.InstallPrincipalFrontierLocked(authorization.LastPredefinedRoleID + 1)
	ctx, cancel := context.WithCancel(context.Background())
	s, err := enginetest.OpenSession(ctx, e, session.SessionStart{ID: ids.SessionID(testSessionID.Add(1))})
	if err != nil {
		t.Fatal(err)
	}
	canceled, err := s.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	for _, sql := range []string{"CREATE ROLE canceled", "DROP ROLE absent", "SET SESSION AUTHORIZATION DEFAULT"} {
		if _, err := e.Execute(canceled, enginetest.Statement(t, sql), 0, nil); !errors.Is(err, context.Canceled) {
			t.Fatal(sql, err)
		}
	}
	canceled.Done()
	s.RequestEnd(session.CleanClose)
	_ = s.WaitEnd(context.Background())
	if e.RoleStore.Published != before {
		t.Fatal("canceled role publication")
	}
	enginetest.RoleExecute(t, e, op, "CREATE ROLE retained")
	before = e.RoleStore.Published
	saturated := *e.RoleStore.Published
	saturated.Revision = ^uint64(0)
	e.RoleStore.PublishLocked(&saturated)
	before = e.RoleStore.Published
	if _, err := e.Execute(op, enginetest.Statement(t, "DROP ROLE retained"), 0, nil); err == nil {
		t.Fatal("role revision exhaustion")
	}
	if e.RoleStore.Published != before {
		t.Fatal("drop exhaustion publication")
	}
	op.Session.Authorization.Revision = ^uint64(0)
	state := op.RoleState()
	if _, err := e.Execute(op, enginetest.Statement(t, "SET SESSION AUTHORIZATION retained"), 0, nil); err == nil {
		t.Fatal("session revision exhaustion")
	}
	if op.RoleState() != state {
		t.Fatal("auth exhaustion mutation")
	}
}

type roleCommitContext struct {
	context.Context
	checks int
}

func (c *roleCommitContext) Err() error {
	c.checks++
	if c.checks >= 3 {
		return context.Canceled
	}
	return nil
}
func TestAuthorizationCancellationAtRoleInstall(t *testing.T) {
	for _, query := range []string{"CREATE ROLE canceled", "DROP ROLE retained", "SET SESSION AUTHORIZATION retained"} {
		t.Run(query, func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			enginetest.RoleExecute(t, e, op, "CREATE ROLE retained")
			beforeRoles, beforeCatalog, beforePrincipal := e.RoleStore.Published, e.Catalog.State, e.RoleStore.NextPrincipal()
			beforeIdentity := op.RoleState()
			beforeRevision := op.Session.Authorization.Revision
			original := op.Ctx
			ctx := &roleCommitContext{Context: original}
			op.Ctx = ctx
			_, err := e.Execute(op, enginetest.Statement(t, query), 0, nil)
			op.Ctx = original
			if !errors.Is(err, context.Canceled) || ctx.checks < 3 {
				t.Fatal("final cancellation checkpoint", ctx.checks, err)
			}
			if e.RoleStore.Published != beforeRoles || e.Catalog.State != beforeCatalog || e.RoleStore.NextPrincipal() != beforePrincipal ||
				op.RoleState() != beforeIdentity || op.Session.Authorization.Revision != beforeRevision {
				t.Fatal("canceled install published a partial change")
			}
		})
	}
}

type roleInitGate struct {
	entered, release chan struct{}
}

func (g *roleInitGate) SessionDefault(ctx context.Context, d settings.Definition) (string, bool, error) {
	if d.Name != "application_name" {
		return "", false, nil
	}
	close(g.entered)
	select {
	case <-g.release:
		return "initial", true, nil
	case <-ctx.Done():
		return "", false, ctx.Err()
	}
}

func TestAuthorizationInitializationDefaults(t *testing.T) {
	e := engine.New()
	gate := &roleInitGate{entered: make(chan struct{}), release: make(chan struct{})}
	close(gate.release)
	s := sessionStart(t, e, gate)
	sessionValue(t, s, "initial")
	sessionSQL(t, s, "SET application_name TO changed")
	sessionSQL(t, s, "SET application_name TO DEFAULT")
	sessionValue(t, s, "initial")
	for _, query := range []string{"SHOW client_min_messages", "SET client_min_messages TO warning", "SET client_min_messages TO DEFAULT", "SHOW client_min_messages"} {
		r := sessionSQL(t, s, query)
		if strings.HasPrefix(query, "SHOW") && !reflect.DeepEqual(enginetest.Tuples(r), [][]driver.Value{{"notice"}}) {
			t.Fatal("integrated boot default was not captured", r, enginetest.Tuples(r))
		}
	}
}
func TestAuthorizationInitializationFencesDeletedPrincipal(t *testing.T) {
	e := engine.New()
	admin, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, admin, "CREATE USER pending")
	role, _ := e.Roles().Lookup("pending")
	gate := &roleInitGate{entered: make(chan struct{}), release: make(chan struct{})}
	type outcome struct {
		session *session.Session
		err     error
	}
	result := make(chan outcome, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		s, err := enginetest.OpenSession(ctx, e, session.SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Defaults: gate}, role.Ref)
		result <- outcome{s, err}
	}()
	released, finished := false, false
	defer func() {
		if !released {
			close(gate.release)
		}
		if !finished {
			select {
			case <-result:
			case <-time.After(5 * time.Second):
				t.Error("initialization worker did not join")
			}
		}
	}()
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("initialization callback not entered")
	}
	enginetest.RoleExecute(t, e, admin, "DROP ROLE pending")
	enginetest.RoleExecute(t, e, admin, "CREATE USER pending")
	recreated, ok := e.Roles().Lookup("pending")
	if !ok || recreated.Ref == role.Ref {
		t.Fatal("drop/recreate did not replace the authenticated principal", recreated)
	}
	close(gate.release)
	released = true
	select {
	case got := <-result:
		finished = true
		if got.session != nil || got.err == nil {
			t.Fatal("stale initialization published", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("initialization did not finish")
	}
	core.EngineSessionMuRef(e).Lock()
	defer core.EngineSessionMuRef(e).Unlock()
	if len(core.EngineSessions(e)) != 1 {
		t.Fatal("failed authorization initialization entered live registry", len(core.EngineSessions(e)))
	}
}

func TestAuthorizationDumpPreservesChangedNamespaceState(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE TABLE sentinel(id INT)")
	namespaceOwner := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("pg_database_owner")
	namespace := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID})
	original := e.Catalog.State.MustNamespace(namespace.Address).ACL
	// Same grantors/grantees/effective rights, split and reordered entries.
	equivalent := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
		{Grantor: namespaceOwner, Grantee: authorization.ACLGrantee{Role: namespaceOwner}, Privileges: authorization.CreatePrivilege},
		{Grantor: namespaceOwner, Grantee: authorization.ACLGrantee{Role: namespaceOwner}, Privileges: authorization.UsagePrivilege},
		{Grantor: namespaceOwner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege},
	}}
	if err := enginetest.SetObjectACL(e, context.Background(), namespace, equivalent); err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err := engine.Dump(context.Background(), e, &dump); err != nil {
		t.Fatal("semantic bootstrap ACL rejected", err)
	}
	restored := engine.New()
	if err := engine.Restore(context.Background(), restored, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal("restore semantic bootstrap ACL dump", err)
	}
	read, readDone := enginetest.RoleOperation(t, restored, "postgres")
	defer readDone()
	enginetest.RoleExecute(t, restored, read, "INSERT INTO sentinel VALUES(7)")
	if got := enginetest.Tuples(enginetest.RoleExecute(t, restored, read, "SELECT * FROM sentinel")); !reflect.DeepEqual(got, [][]driver.Value{{int64(7)}}) {
		t.Fatal("restored sentinel behavior", got)
	}
	ref := roleTableRef(t, restored, "sentinel")
	if restored.Catalog.State.MustRelation(ref.Address).Owner != restored.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres") {
		t.Fatal("dump did not preserve sentinel owner")
	}
	changed := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
		{Grantor: namespaceOwner, Grantee: authorization.ACLGrantee{Role: namespaceOwner}, Privileges: authorization.UsagePrivilege | authorization.CreatePrivilege, GrantOptions: authorization.UsagePrivilege | authorization.CreatePrivilege},
		{Grantor: namespaceOwner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege},
	}}
	if err := enginetest.SetObjectACL(e, context.Background(), namespace, changed); err != nil {
		t.Fatal(err)
	}
	before := e.Catalog.State
	dump.Reset()
	if err := engine.Dump(context.Background(), e, &dump); err != nil || dump.Len() == 0 {
		t.Fatal("changed namespace ACL not serialized", err, dump.String())
	}
	if e.Catalog.State != before {
		t.Fatal("dump mutated namespace")
	}
	if err := engine.Restore(context.Background(), e, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if got := e.Catalog.State.MustNamespace(namespace.Address).ACL; !reflect.DeepEqual(got, changed) {
		t.Fatal("changed namespace ACL lost", got, changed)
	}
	namespace = e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), namespace.Address)
	if err := enginetest.SetObjectACL(e, context.Background(), namespace, original); err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, e, op, "CREATE ROLE namespace_owner")
	// Independent owner/grantor identities are representable without rewriting
	// the authentic raw ACL to the new owner's default.
	owner, _ := e.Roles().Lookup("namespace_owner")
	replacement := catalog.CloneState(e.Catalog.State)
	ns := *replacement.MustNamespace(namespace.Address)
	ns.Owner = owner.Ref
	replacement.SetDescriptor(namespace.Address, &ns)
	if _, err := e.ReplaceCatalog(context.Background(), e, e.Catalog.Read(replacement)); err != nil {
		t.Fatal(err)
	}
	dump.Reset()
	if err := engine.Dump(context.Background(), e, &dump); err != nil || dump.Len() == 0 {
		t.Fatal("independent namespace owner/ACL not serialized", err, dump.String())
	}
	if err := engine.Restore(context.Background(), e, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	got := e.Catalog.State.MustNamespace(namespace.Address)
	if got.Owner != owner.Ref || !reflect.DeepEqual(got.ACL, original) {
		t.Fatal("independent owner/grantor lost", got)
	}
}

func TestAuthorizationOwnerDiagnosticAndInvalidACLs(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE USER owner")
	enginetest.RoleExecute(t, e, op, "CREATE USER reader")
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION owner")
	withPublicSchemaUnitOwner(t, e, "owner", func() { enginetest.RoleExecute(t, e, op, "CREATE TABLE secret(id INT)") })
	ref := roleTableRef(t, e, "secret")
	owner := op.RoleState().CurrentUser
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
	view, err := core.OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	err = view.Check(access.Requirement{Object: ref, OwnerOnly: true})
	enginetest.State(t, err, "42501")
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) {
		t.Fatal(err)
	}
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "must be owner of table secret"}
	if !reflect.DeepEqual(diagnostic, want) {
		t.Fatalf("owner diagnostic %#v want %#v", diagnostic, want)
	}
	valid := authorization.ACLEntry{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege}
	for _, invalid := range []authorization.ACLEntry{
		{Grantor: authorization.PrincipalRef{}, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege},
		{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true, Role: owner}, Privileges: authorization.SelectPrivilege},
		{Grantor: owner, Grantee: authorization.ACLGrantee{Role: engine.New().RoleStore.Domain.Principal(10)}, Privileges: authorization.SelectPrivilege},
		{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege},
		{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege, GrantOptions: authorization.InsertPrivilege},
	} {
		before := e.Catalog.State
		if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{invalid}}); err == nil {
			t.Fatal("invalid ACL published", invalid)
		}
		if e.Catalog.State != before {
			t.Fatal("invalid ACL changed catalog")
		}
	}
	before := e.Catalog.State
	if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Entries: []authorization.ACLEntry{valid}}); err == nil {
		t.Fatal("absent ACL carried entries")
	}
	if e.Catalog.State != before {
		t.Fatal("absent ACL changed catalog")
	}
	enginetest.RoleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	beforeRoles := e.RoleStore.Published
	for _, name := range []string{"public", "none"} {
		_, err := e.Execute(op, &parser.CreateRole{Name: parser.Identifier{Text: name}}, 0, nil)
		enginetest.State(t, err, "42939")
		if e.RoleStore.Published != beforeRoles {
			t.Fatal("reserved implicit role became principal")
		}
	}
}

func TestAuthorizationLargeColumnManifestAndRepeatedUses(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE ROLE reader")
	var columns strings.Builder
	for i := range 65 {
		if i != 0 {
			columns.WriteByte(',')
		}
		fmt.Fprintf(&columns, "c%d INT", i)
	}
	enginetest.RoleExecute(t, e, op, "CREATE TABLE wide("+columns.String()+")")
	enginetest.RoleExecute(t, e, op, "INSERT INTO wide(c64) VALUES(7)")
	ref := roleTableRef(t, e, "wide")
	column := ref
	column.Address.SubID = 65
	if err := enginetest.SetObjectACL(e, context.Background(), column, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
		{Grantor: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege},
	}}); err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
	result := enginetest.RoleExecute(t, e, op, "SELECT c64,c64 FROM wide WHERE c64=7 ORDER BY c64")
	if result.Count != 1 {
		t.Fatal("wide row count", result.Count)
	}
	values := enginetest.Tuples(result)
	if !reflect.DeepEqual(values, [][]driver.Value{{int64(7), int64(7)}}) {
		t.Fatal(values)
	}
	enginetest.RoleError(t, e, op, "SELECT c0,c64 FROM wide", "42501", "permission denied for table wide", "")
	enginetest.RoleError(t, e, op, "INSERT INTO wide(c64) VALUES(9)", "42501", "permission denied for table wide", "")
	before := e.Catalog.State
	for _, subID := range []int32{-1, 66} {
		invalid := column
		invalid.Address.SubID = subID
		if err := enginetest.SetObjectACL(e, context.Background(), invalid, authorization.ACLValue{}); err == nil {
			t.Fatal("invalid absent column ACL", subID)
		}
	}
	if err := enginetest.SetObjectACL(e, context.Background(), column, authorization.ACLValue{Entries: []authorization.ACLEntry{{Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege}}}); err == nil {
		t.Fatal("entries in absent column ACL")
	}
	if e.Catalog.State != before {
		t.Fatal("invalid ACL publication")
	}
	if err := enginetest.SetObjectACL(e, context.Background(), column, authorization.ACLValue{}); err != nil {
		t.Fatal(err)
	}
	enginetest.RoleError(t, e, op, "SELECT c64 FROM wide", "42501", "permission denied for table wide", "")
	var dump bytes.Buffer
	if err := engine.Dump(context.Background(), e, &dump); err != nil || dump.Len() == 0 {
		t.Fatal("absent column ACL dump", err, dump.String())
	}
}

func TestAuthorizationStampRetainsRolePublication(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE TABLE stamp_retained(id INT)")
	held, err := core.OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	stamp := held.Stamp()
	oldPublication := e.RoleStore.Published
	oldEdges := held.Roles.Memberships()
	enginetest.RoleExecute(t, e, op, "CREATE ROLE stamp_peer")
	fresh, err := core.OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	if held.Stamp() != stamp || fresh.Stamp().Roles != stamp.Roles+1 || fresh.RoleRecord == oldPublication || !reflect.DeepEqual(held.Roles.Memberships(), oldEdges) {
		t.Fatal("role-only publication changed held stamp/root")
	}
	ref := roleTableRef(t, e, "stamp_retained")
	if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	newest, err := core.OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	if held.Stamp() != stamp || fresh.Stamp().Roles != stamp.Roles+1 || newest.Stamp().Roles != stamp.Roles+2 || newest.RoleRecord == fresh.RoleRecord || !reflect.DeepEqual(held.Roles.Memberships(), oldEdges) {
		t.Fatal("ACL-only publication changed held stamp/root")
	}
}

// descriptorReferences lists every principal a descriptor references: owner,
// policy roles, ACL grantors and grantees, including column ACLs.
func descriptorReferences(d catalog.Descriptor) []authorization.PrincipalRef {
	switch t := d.(type) {
	case *catalog.ForeignObject:
		if t.Address().ClassID == ids.ForeignMappingClassID {
			if t.MappedRole == (authorization.PrincipalRef{}) {
				return nil
			}
			return []authorization.PrincipalRef{t.MappedRole}
		}
	case *catalog.TSTemplate, *catalog.TSParser:
		return nil
	}
	owner, acl, cols := access.DescriptorACL(d)
	refs := []authorization.PrincipalRef{owner}
	if relation, ok := d.(*catalog.Relation); ok {
		for _, policy := range relation.Security.Policies {
			for _, role := range policy.Roles {
				if !role.Public {
					refs = append(refs, role.Ref)
				}
			}
		}
	}
	add := func(a authorization.ACLValue) {
		for _, x := range a.Entries {
			refs = append(refs, x.Grantor)
			if !x.Grantee.Public {
				refs = append(refs, x.Grantee.Role)
			}
		}
	}
	add(acl)
	for _, a := range cols {
		add(a)
	}
	return refs
}
