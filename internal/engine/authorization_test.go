package engine

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

// Host catalog fixtures resolve as the real bootstrap principal, not as a
// zero/default-allow authorization view. Caller holds e.mu.
func catalogAuthorization(e *Engine, view catalog.Read) AuthorizationView {
	return AuthorizationView{accessView: accessView{Owner: e.catalog.owner(), PairedCatalog: view, RoleRecord: e.roles.published, Catalog: view, Roles: e.roles.read(e.catalog.owner(), view, e.roles.published), State: authorization.RoleState{CurrentUser: e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")}}, engine: e}
}

func roleTableRef(t *testing.T, e *Engine, name string) ids.ObjectRef {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	ref, err := e.resolveReference(catalogAuthorization(e, e.catalog.read(e.catalog.state)), statement(t, "SELECT * FROM "+name).(*parser.Select).From.(*parser.TableReference).Name)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func TestAuthorizationACLAndExactColumnUses(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER owner")
	roleExecute(t, e, op, "CREATE USER reader")
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION owner")
	withPublicSchemaUnitOwner(t, e, "owner", func() { roleExecute(t, e, op, "CREATE TABLE secret(a INT,b INT,c INT)") })
	roleExecute(t, e, op, "INSERT INTO secret VALUES(7,8,9)")
	ref := roleTableRef(t, e, "secret")
	owner := op.RoleState().CurrentUser
	retained, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	view, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	if err := retained.Check(AccessRequirement{Object: ref, Privileges: authorization.SelectPrivilege | authorization.InsertPrivilege}); err != nil {
		t.Fatal("old view changed", err)
	}
	if err := view.Check(AccessRequirement{Object: ref, Privileges: authorization.SelectPrivilege}); err == nil {
		t.Fatal("explicit empty owner rights")
	}
	if err := view.Check(AccessRequirement{Object: ref, OwnerOnly: true}); err != nil {
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
	if err := e.SetObjectACL(context.Background(), column, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{entry}}); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
	roleExecute(t, e, op, "SELECT a FROM secret")
	for _, sql := range []string{"SELECT * FROM secret", "SELECT a FROM secret WHERE b=8", "SELECT a FROM secret ORDER BY c", "INSERT INTO secret(a) VALUES(10)"} {
		if _, err := e.Describe(op, statement(t, sql)); err != nil {
			t.Fatal("eager prepare denial", sql, err)
		}
		roleError(t, e, op, sql, "42501", "permission denied for table secret", "")
	}
	view, err = op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	if err := view.Check(AccessRequirement{Object: column, Privileges: authorization.SelectPrivilege | authorization.InsertPrivilege}); err == nil {
		t.Fatal("any-bit check")
	}
	if err := view.Check(AccessRequirement{Object: ref, OwnerOnly: true}); err == nil {
		t.Fatal("nonowner authority")
	}
	for _, req := range []AccessRequirement{{Object: ref}, {Object: ref, Privileges: authorization.UsagePrivilege}, {Object: New().catalog.Domain.Ref(ref.Epoch, ref.Address), Privileges: authorization.SelectPrivilege}} {
		if err := view.Check(req); err == nil {
			t.Fatal("invalid requirement", req)
		}
	}
	entry.Grantee = authorization.ACLGrantee{Public: true}
	entry.Privileges = authorization.SelectPrivilege | authorization.InsertPrivilege
	if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{entry}}); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "SELECT a,b,c FROM secret")
	roleExecute(t, e, op, "INSERT INTO secret(a) VALUES(11)")
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	roleExecute(t, e, op, "CREATE USER future")
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION future")
	roleExecute(t, e, op, "SELECT a FROM secret")
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	var dump bytes.Buffer
	if err := e.Dump(context.Background(), &dump); err == nil || dump.Len() != 0 {
		t.Fatal("explicit ACL dump loss", err, dump.String())
	}
	namespace := e.catalog.Domain.Ref(e.catalog.state.Epoch(), ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID})
	entry.Privileges = authorization.UsagePrivilege
	entry.Grantor = e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	if err := e.SetObjectACL(context.Background(), namespace, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{entry}}); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION future")
	roleErrorPosition(t, e, op, "CREATE TABLE forbidden(id INT)", "42501", "permission denied for schema public", "", "14")
}

func TestAuthorizationImportRemapsACLAndDependencies(t *testing.T) {
	source, target := New(), New()
	a, doneA := roleOperation(t, source, "postgres")
	defer doneA()
	b, doneB := roleOperation(t, target, "postgres")
	defer doneB()
	for _, pair := range []struct {
		e  *Engine
		op *Operation
	}{{source, a}, {target, b}} {
		roleExecute(t, pair.e, pair.op, "CREATE USER owner")
		roleExecute(t, pair.e, pair.op, "CREATE USER reader")
	}
	roleExecute(t, source, a, "SET SESSION AUTHORIZATION owner")
	withPublicSchemaUnitOwner(t, source, "owner", func() { roleExecute(t, source, a, "CREATE TABLE secret(id INT)") })
	roleExecute(t, source, a, "INSERT INTO secret VALUES(7)")
	ref := roleTableRef(t, source, "secret")
	owner := a.RoleState().CurrentUser
	reader, _ := source.Roles().Lookup("reader")
	if err := source.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := target.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	targetRef := roleTableRef(t, target, "secret")
	target.mu.Lock()
	d := target.catalog.state.MustRelation(targetRef.Address)
	target.mu.Unlock()
	for _, ref := range descriptorReferences(d) {
		if target.Roles().ValidatePrincipal(ref) != nil {
			t.Fatal("foreign principal survived import")
		}
	}
	roleExecute(t, target, b, "SET SESSION AUTHORIZATION reader")
	r := roleExecute(t, target, b, "SELECT id FROM secret")
	values := tuples(r)
	if !reflect.DeepEqual(values, [][]driver.Value{{int64(7)}}) {
		t.Fatal(values)
	}
	roleExecute(t, target, b, "RESET SESSION AUTHORIZATION")
	roleError(t, target, b, "DROP ROLE reader", "2BP01", `role "reader" cannot be dropped because some objects depend on it`, "privileges for table secret")
	unknown := New()
	before := unknown.catalog.state
	if _, err := unknown.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err == nil {
		t.Fatal("unknown owner import")
	}
	if unknown.catalog.state != before {
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
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER owner")
	roleExecute(t, e, op, "CREATE TABLE sentinel(id INT)")
	roleExecute(t, e, op, "INSERT INTO sentinel VALUES(7)")
	before := e.catalog.state
	gate := &roleBlockingReader{entered: make(chan struct{}), release: make(chan struct{}),
		text: "SET SESSION AUTHORIZATION 'owner'; CREATE TABLE imported(id INT);"}
	result := make(chan error, 1)
	go func() { result <- e.Restore(context.Background(), &roleGateReader{gate: gate}) }()
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
	roleExecute(t, e, op, "DROP ROLE owner")
	roleExecute(t, e, op, "CREATE USER owner")
	roleExecute(t, e, op, "SELECT id FROM sentinel")
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
	if e.catalog.state != before {
		t.Fatal("restore failure changed catalog")
	}
}
func TestAuthorizationExhaustionAndCancellationDoNotPublish(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	before := e.roles.published
	e.roles.nextPrincipal = ^uint64(0)
	if _, err := e.Execute(op, statement(t, "CREATE ROLE exhausted"), 0, nil); err == nil {
		t.Fatal("principal exhaustion")
	}
	if e.roles.published != before {
		t.Fatal("exhaustion publication")
	}
	e.roles.nextPrincipal = authorization.LastPredefinedRoleID + 1
	ctx, cancel := context.WithCancel(context.Background())
	s, err := e.StartSession(ctx, SessionStart{ID: ids.SessionID(testSessionID.Add(1))})
	if err != nil {
		t.Fatal(err)
	}
	canceled, err := s.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	for _, sql := range []string{"CREATE ROLE canceled", "DROP ROLE absent", "SET SESSION AUTHORIZATION DEFAULT"} {
		if _, err := e.Execute(canceled, statement(t, sql), 0, nil); !errors.Is(err, context.Canceled) {
			t.Fatal(sql, err)
		}
	}
	canceled.Done()
	s.RequestEnd(CleanClose)
	_ = s.WaitEnd(context.Background())
	if e.roles.published != before {
		t.Fatal("canceled role publication")
	}
	roleExecute(t, e, op, "CREATE ROLE retained")
	before = e.roles.published
	saturated := *e.roles.published
	saturated.Revision = ^uint64(0)
	e.roles.published = &saturated
	before = e.roles.published
	if _, err := e.Execute(op, statement(t, "DROP ROLE retained"), 0, nil); err == nil {
		t.Fatal("role revision exhaustion")
	}
	if e.roles.published != before {
		t.Fatal("drop exhaustion publication")
	}
	op.session.authorization.revision = ^uint64(0)
	state := op.RoleState()
	if _, err := e.Execute(op, statement(t, "SET SESSION AUTHORIZATION retained"), 0, nil); err == nil {
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
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			roleExecute(t, e, op, "CREATE ROLE retained")
			beforeRoles, beforeCatalog, beforePrincipal := e.roles.published, e.catalog.state, e.roles.nextPrincipal
			beforeIdentity := op.RoleState()
			beforeRevision := op.session.authorization.revision
			original := op.ctx
			ctx := &roleCommitContext{Context: original}
			op.ctx = ctx
			_, err := e.Execute(op, statement(t, query), 0, nil)
			op.ctx = original
			if !errors.Is(err, context.Canceled) || ctx.checks < 3 {
				t.Fatal("final cancellation checkpoint", ctx.checks, err)
			}
			if e.roles.published != beforeRoles || e.catalog.state != beforeCatalog || e.roles.nextPrincipal != beforePrincipal ||
				op.RoleState() != beforeIdentity || op.session.authorization.revision != beforeRevision {
				t.Fatal("canceled install published a partial change")
			}
		})
	}
}

type roleInitGate struct {
	entered, release chan struct{}
}

func (g *roleInitGate) SessionDefault(ctx context.Context, d SettingDefinition) (string, bool, error) {
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
	e := New()
	gate := &roleInitGate{entered: make(chan struct{}), release: make(chan struct{})}
	close(gate.release)
	s := sessionStart(t, e, gate)
	sessionValue(t, s, "initial")
	sessionSQL(t, s, "SET application_name TO changed")
	sessionSQL(t, s, "SET application_name TO DEFAULT")
	sessionValue(t, s, "initial")
	for _, query := range []string{"SHOW client_min_messages", "SET client_min_messages TO warning", "SET client_min_messages TO DEFAULT", "SHOW client_min_messages"} {
		r := sessionSQL(t, s, query)
		if strings.HasPrefix(query, "SHOW") && !reflect.DeepEqual(tuples(r), [][]driver.Value{{"notice"}}) {
			t.Fatal("integrated boot default was not captured", r, tuples(r))
		}
	}
}
func TestAuthorizationInitializationFencesDeletedPrincipal(t *testing.T) {
	e := New()
	admin, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, admin, "CREATE USER pending")
	role, _ := e.Roles().Lookup("pending")
	gate := &roleInitGate{entered: make(chan struct{}), release: make(chan struct{})}
	type outcome struct {
		session *Session
		err     error
	}
	result := make(chan outcome, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		s, err := e.StartSession(ctx, SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Authenticated: role.Ref, Defaults: gate})
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
	roleExecute(t, e, admin, "DROP ROLE pending")
	roleExecute(t, e, admin, "CREATE USER pending")
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
	e.sessionMu.Lock()
	defer e.sessionMu.Unlock()
	if len(e.sessions) != 1 {
		t.Fatal("failed authorization initialization entered live registry", len(e.sessions))
	}
}

func TestAuthorizationDumpRejectsChangedNamespaceState(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE TABLE sentinel(id INT)")
	namespaceOwner := e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("pg_database_owner")
	namespace := e.catalog.Domain.Ref(e.catalog.state.Epoch(), ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID})
	original := e.catalog.state.MustNamespace(namespace.Address).ACL
	// Same grantors/grantees/effective rights, split and reordered entries.
	equivalent := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
		{Grantor: namespaceOwner, Grantee: authorization.ACLGrantee{Role: namespaceOwner}, Privileges: authorization.CreatePrivilege},
		{Grantor: namespaceOwner, Grantee: authorization.ACLGrantee{Role: namespaceOwner}, Privileges: authorization.UsagePrivilege},
		{Grantor: namespaceOwner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege},
	}}
	if err := e.SetObjectACL(context.Background(), namespace, equivalent); err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err := e.Dump(context.Background(), &dump); err != nil {
		t.Fatal("semantic bootstrap ACL rejected", err)
	}
	restored := New()
	if err := restored.Restore(context.Background(), bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal("restore semantic bootstrap ACL dump", err)
	}
	read, readDone := roleOperation(t, restored, "postgres")
	defer readDone()
	roleExecute(t, restored, read, "INSERT INTO sentinel VALUES(7)")
	if got := tuples(roleExecute(t, restored, read, "SELECT * FROM sentinel")); !reflect.DeepEqual(got, [][]driver.Value{{int64(7)}}) {
		t.Fatal("restored sentinel behavior", got)
	}
	ref := roleTableRef(t, restored, "sentinel")
	if restored.catalog.state.MustRelation(ref.Address).Owner != restored.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres") {
		t.Fatal("dump did not preserve sentinel owner")
	}
	changed := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
		{Grantor: namespaceOwner, Grantee: authorization.ACLGrantee{Role: namespaceOwner}, Privileges: authorization.UsagePrivilege | authorization.CreatePrivilege, GrantOptions: authorization.UsagePrivilege | authorization.CreatePrivilege},
		{Grantor: namespaceOwner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege},
	}}
	if err := e.SetObjectACL(context.Background(), namespace, changed); err != nil {
		t.Fatal(err)
	}
	before := e.catalog.state
	dump.Reset()
	if err := e.Dump(context.Background(), &dump); err == nil || dump.Len() != 0 {
		t.Fatal("changed namespace ACL silently lost", err, dump.String())
	}
	if e.catalog.state != before {
		t.Fatal("dump mutated namespace")
	}
	if err := e.SetObjectACL(context.Background(), namespace, original); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "CREATE ROLE namespace_owner")
	// A raw descriptor-owner rewrite without the matching ACL substitution is
	// genuinely unrepresentable. Normal ALTER OWNER's canonical transform is
	// accepted and roundtripped by TestSchemaPublicLifecycleControls.
	owner, _ := e.Roles().Lookup("namespace_owner")
	replacement := catalog.CloneState(e.catalog.state)
	ns := *replacement.MustNamespace(namespace.Address)
	ns.Owner = owner.Ref
	replacement.SetDescriptor(namespace.Address, &ns)
	if _, err := e.ReplaceCatalog(context.Background(), e, e.catalog.read(replacement)); err != nil {
		t.Fatal(err)
	}
	if err := e.Dump(context.Background(), &dump); err == nil || dump.Len() != 0 {
		t.Fatal("inconsistent namespace owner/ACL silently lost", err, dump.String())
	}
}

func TestAuthorizationOwnerDiagnosticAndInvalidACLs(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER owner")
	roleExecute(t, e, op, "CREATE USER reader")
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION owner")
	withPublicSchemaUnitOwner(t, e, "owner", func() { roleExecute(t, e, op, "CREATE TABLE secret(id INT)") })
	ref := roleTableRef(t, e, "secret")
	owner := op.RoleState().CurrentUser
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
	view, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	err = view.Check(AccessRequirement{Object: ref, OwnerOnly: true})
	state(t, err, "42501")
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
		{Grantor: owner, Grantee: authorization.ACLGrantee{Role: New().roles.domain.Principal(10)}, Privileges: authorization.SelectPrivilege},
		{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege},
		{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege, GrantOptions: authorization.InsertPrivilege},
	} {
		before := e.catalog.state
		if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{invalid}}); err == nil {
			t.Fatal("invalid ACL published", invalid)
		}
		if e.catalog.state != before {
			t.Fatal("invalid ACL changed catalog")
		}
	}
	before := e.catalog.state
	if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Entries: []authorization.ACLEntry{valid}}); err == nil {
		t.Fatal("absent ACL carried entries")
	}
	if e.catalog.state != before {
		t.Fatal("absent ACL changed catalog")
	}
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	beforeRoles := e.roles.published
	for _, name := range []string{"public", "none"} {
		_, err := e.Execute(op, &parser.CreateRole{Name: parser.Identifier{Text: name}}, 0, nil)
		state(t, err, "42939")
		if e.roles.published != beforeRoles {
			t.Fatal("reserved implicit role became principal")
		}
	}
}

func TestAuthorizationLargeColumnManifestAndRepeatedUses(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE ROLE reader")
	var columns strings.Builder
	for i := range 65 {
		if i != 0 {
			columns.WriteByte(',')
		}
		fmt.Fprintf(&columns, "c%d INT", i)
	}
	roleExecute(t, e, op, "CREATE TABLE wide("+columns.String()+")")
	roleExecute(t, e, op, "INSERT INTO wide(c64) VALUES(7)")
	ref := roleTableRef(t, e, "wide")
	column := ref
	column.Address.SubID = 65
	if err := e.SetObjectACL(context.Background(), column, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
		{Grantor: e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege},
	}}); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
	result := roleExecute(t, e, op, "SELECT c64,c64 FROM wide WHERE c64=7 ORDER BY c64")
	if result.Count != 1 {
		t.Fatal("wide row count", result.Count)
	}
	values := tuples(result)
	if !reflect.DeepEqual(values, [][]driver.Value{{int64(7), int64(7)}}) {
		t.Fatal(values)
	}
	roleError(t, e, op, "SELECT c0,c64 FROM wide", "42501", "permission denied for table wide", "")
	roleError(t, e, op, "INSERT INTO wide(c64) VALUES(9)", "42501", "permission denied for table wide", "")
	before := e.catalog.state
	for _, subID := range []int32{-1, 66} {
		invalid := column
		invalid.Address.SubID = subID
		if err := e.SetObjectACL(context.Background(), invalid, authorization.ACLValue{}); err == nil {
			t.Fatal("invalid absent column ACL", subID)
		}
	}
	if err := e.SetObjectACL(context.Background(), column, authorization.ACLValue{Entries: []authorization.ACLEntry{{Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege}}}); err == nil {
		t.Fatal("entries in absent column ACL")
	}
	if e.catalog.state != before {
		t.Fatal("invalid ACL publication")
	}
	if err := e.SetObjectACL(context.Background(), column, authorization.ACLValue{}); err != nil {
		t.Fatal(err)
	}
	roleError(t, e, op, "SELECT c64 FROM wide", "42501", "permission denied for table wide", "")
	var dump bytes.Buffer
	if err := e.Dump(context.Background(), &dump); err != nil || dump.Len() == 0 {
		t.Fatal("absent column ACL dump", err, dump.String())
	}
}

func TestAuthorizationStampRetainsRolePublication(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE TABLE stamp_retained(id INT)")
	held, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	stamp := held.Stamp()
	oldPublication := e.roles.published
	oldEdges := held.Roles.Memberships()
	if err := e.SetRoleReferences(context.Background(), "stamp fixture", []authorization.PrincipalRef{op.RoleState().CurrentUser}); err != nil {
		t.Fatal(err)
	}
	fresh, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	if held.Stamp() != stamp || fresh.Stamp().Roles != stamp.Roles+1 || fresh.RoleRecord == oldPublication || !reflect.DeepEqual(held.Roles.Memberships(), oldEdges) {
		t.Fatal("role-only publication changed held stamp/root")
	}
	ref := roleTableRef(t, e, "stamp_retained")
	if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	newest, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	if held.Stamp() != stamp || fresh.Stamp().Roles != stamp.Roles+1 || newest.Stamp().Roles != stamp.Roles+2 || newest.RoleRecord == fresh.RoleRecord || !reflect.DeepEqual(held.Roles.Memberships(), oldEdges) {
		t.Fatal("ACL-only publication changed held stamp/root")
	}
}
