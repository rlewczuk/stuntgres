package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func roleAttributeCreate(t *testing.T, e *Engine, op *session.Operation, name string, login bool, o parser.RoleOptions) authorization.RoleDescriptor {
	t.Helper()
	roleAttributeApply(t, e, op, &parser.CreateRole{Name: parser.Identifier{Text: name}, DefaultLogin: login, Options: o})
	d, ok := e.Roles().Lookup(name)
	if !ok {
		t.Fatal("missing role", name)
	}
	return d
}
func roleAttributeApply(t *testing.T, e *Engine, op *session.Operation, s parser.Statement) {
	t.Helper()
	r, err := e.Execute(op, s, 0, nil)
	if err != nil || r.Count != 0 || r.Rows != nil {
		t.Fatalf("attribute utility: result=%#v error=%v", r, err)
	}
}
func roleAttributeAlter(name string, o parser.RoleOptions) *parser.AlterRole {
	return &parser.AlterRole{Name: parser.Identifier{Text: name}, Options: o}
}
func roleAttributeFailure(t *testing.T, e *Engine, op *session.Operation, s parser.Statement, code string) {
	t.Helper()
	beforePublication := e.RoleStore.Published
	next := e.RoleStore.NextPrincipal()
	revision := e.RoleStore.NextRevision()
	r, err := e.Execute(op, s, 0, nil)
	var d *sqlstate.Error
	if err == nil || code != "" && (!errors.As(err, &d) || d.Code != code) || r.Rows != nil {
		t.Fatalf("want %s: %#v %v", code, r, err)
	}
	if e.RoleStore.Published != beforePublication || e.RoleStore.NextPrincipal() != next || e.RoleStore.NextRevision() != revision {
		t.Fatal("failed mutation published role root or allocator")
	}
}
func allRoleOptions() parser.RoleOptions {
	return parser.RoleOptions{Present: parser.RoleSuperuser | parser.RoleCreateDB | parser.RoleCreateRole | parser.RoleLogin | parser.RoleInherit | parser.RoleReplication | parser.RoleBypassRLS | parser.RoleConnectionLimit | parser.RolePassword | parser.RoleValidUntil, Superuser: true, CreateDB: true, CreateRole: true, Login: true, Inherit: false, Replication: true, BypassRLS: true, ConnectionLimit: 3, Password: "secret", ValidUntil: "infinity"}
}
func TestRoleAttributesDefaultsAndRetention(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	boot, _ := e.Roles().Lookup("postgres")
	if boot.AuthIDVersion != 1 || !boot.Login || !boot.Superuser || !boot.CreateDB || !boot.CreateRole || !boot.Inherit || !boot.Replication || !boot.BypassRLS || boot.ConnectionLimit != -1 || boot.Password != "" || boot.HasValidUntil {
		t.Fatal(boot)
	}
	for _, login := range []bool{false, true} {
		name := "defaults_role"
		if login {
			name = "defaults_user"
		}
		version := e.RoleStore.NextRevision()
		d := roleAttributeCreate(t, e, op, name, login, parser.RoleOptions{})
		want := authorization.RoleDescriptor{Ref: d.Ref, Name: name, AuthIDVersion: version, Login: login, Inherit: true, ConnectionLimit: -1}
		if d != want {
			t.Fatalf("defaults %#v want %#v", d, want)
		}
	}
	d := roleAttributeCreate(t, e, op, "attrs", false, allRoleOptions())
	if !d.Superuser || !d.CreateDB || !d.CreateRole || !d.Login || d.Inherit || !d.Replication || !d.BypassRLS || d.ConnectionLimit != 3 || !d.HasValidUntil || d.ValidUntil != sqltype.TimestampPositiveInfinity || !strings.HasPrefix(d.Password, "SCRAM-SHA-256$4096:") {
		t.Fatal(d)
	}
	old := e.Roles()
	version := e.RoleStore.NextRevision()
	roleAttributeApply(t, e, op, roleAttributeAlter("attrs", parser.RoleOptions{}))
	retained, _ := e.Roles().Lookup("attrs")
	wantRetained := d
	wantRetained.AuthIDVersion = version
	if retained != wantRetained || retained.AuthIDVersion <= d.AuthIDVersion {
		t.Fatal("empty ALTER changed attributes or failed to replace the authid tuple")
	}
	o := parser.RoleOptions{Present: parser.RoleLogin | parser.RoleInherit, Inherit: true}
	version = e.RoleStore.NextRevision()
	roleAttributeApply(t, e, op, roleAttributeAlter("attrs", o))
	changed, _ := e.Roles().Lookup("attrs")
	want := d
	want.AuthIDVersion = version
	want.Login = false
	want.Inherit = true
	if changed != want {
		t.Fatalf("omission lost fields %#v want %#v", changed, want)
	}
	oldD, _ := old.Lookup("attrs")
	if oldD != d {
		t.Fatal("retained descriptor mutated")
	}
	roleAttributeApply(t, e, op, &parser.Comment{Kind: parser.CommentRole, Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "attrs"}}}, Text: "retained comment"})
	commented, _ := e.Roles().Lookup("attrs")
	want.Comment = "retained comment"
	if commented != want {
		t.Fatal("COMMENT discarded attributes", commented)
	}
	off := parser.RoleOptions{Present: parser.RoleSuperuser | parser.RoleCreateDB | parser.RoleCreateRole | parser.RoleLogin | parser.RoleInherit | parser.RoleReplication | parser.RoleBypassRLS, Inherit: true}
	version = e.RoleStore.NextRevision()
	roleAttributeApply(t, e, op, roleAttributeAlter("attrs", off))
	got, _ := e.Roles().Lookup("attrs")
	want.Superuser = false
	want.CreateDB = false
	want.CreateRole = false
	want.Replication = false
	want.BypassRLS = false
	want.AuthIDVersion = version
	if got != want {
		t.Fatal("negative toggles lost metadata", got, want)
	}
}

func TestRoleAttributesAuthority(t *testing.T) {
	e := New()
	admin, done := roleOperation(t, e, "postgres")
	defer done()
	actor := roleAttributeCreate(t, e, admin, "creator", true, parser.RoleOptions{Present: parser.RoleCreateRole, CreateRole: true})
	roleAttributeCreate(t, e, admin, "unrelated", true, parser.RoleOptions{})
	op, closeActor := roleOperation(t, e, "creator")
	defer closeActor()
	before := e.Roles()
	target := roleAttributeCreate(t, e, op, "owned", false, parser.RoleOptions{})
	edges := e.Roles().Memberships()
	wantEdge := authorization.Membership{Role: target.Ref, Member: actor.Ref, Grantor: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Admin: true}
	if len(edges) != 4 || edges[3] != wantEdge || len(before.Memberships()) != 3 {
		t.Fatal("creator ADMIN not atomic", edges)
	}
	held := roleAttributeAlter("owned", parser.RoleOptions{Present: parser.RoleLogin, Login: true})
	if _, err := e.Describe(op, held); err != nil {
		t.Fatal(err)
	}
	roleAttributeApply(t, e, op, held)
	roleAttributeFailure(t, e, op, roleAttributeAlter("unrelated", parser.RoleOptions{}), "42501")
	roleAttributeFailure(t, e, op, roleAttributeAlter("creator", parser.RoleOptions{Present: parser.RoleLogin}), "42501")
	roleAttributeApply(t, e, op, roleAttributeAlter("creator", parser.RoleOptions{Present: parser.RolePassword, PasswordNull: true}))
	roleAttributeFailure(t, e, op, roleAttributeAlter("creator", parser.RoleOptions{Present: parser.RolePassword | parser.RoleValidUntil, PasswordNull: true, ValidUntil: "infinity"}), "42501")
	for _, special := range []struct {
		field    parser.RoleField
		positive parser.RoleOptions
	}{
		{parser.RoleCreateDB, parser.RoleOptions{CreateDB: true}}, {parser.RoleReplication, parser.RoleOptions{Replication: true}}, {parser.RoleBypassRLS, parser.RoleOptions{BypassRLS: true}},
	} {
		pos := special.positive
		pos.Present = special.field
		roleAttributeFailure(t, e, op, &parser.CreateRole{Name: parser.Identifier{Text: "denied"}, Options: pos}, "42501")
		roleAttributeCreate(t, e, op, "negative", false, parser.RoleOptions{Present: special.field})
		roleAttributeApply(t, e, admin, &parser.DropRole{Names: []string{"negative"}})
		roleAttributeFailure(t, e, op, roleAttributeAlter("owned", pos), "42501")
		roleAttributeFailure(t, e, op, roleAttributeAlter("owned", parser.RoleOptions{Present: special.field}), "42501")
		roleAttributeApply(t, e, admin, roleAttributeAlter("creator", pos))
		roleAttributeApply(t, e, op, roleAttributeAlter("owned", pos))
		roleAttributeApply(t, e, op, roleAttributeAlter("owned", parser.RoleOptions{Present: special.field}))
	}
	roleAttributeFailure(t, e, op, roleAttributeAlter("postgres", parser.RoleOptions{}), "42501")
	for _, value := range []bool{false, true} {
		roleAttributeFailure(t, e, op, roleAttributeAlter("owned", parser.RoleOptions{Present: parser.RoleSuperuser, Superuser: value}), "42501")
	}
	roleAttributeFailure(t, e, admin, roleAttributeAlter("postgres", parser.RoleOptions{Present: parser.RoleSuperuser}), "0A000")
	roleAttributeApply(t, e, admin, roleAttributeAlter("creator", parser.RoleOptions{Present: parser.RoleCreateRole}))
	roleAttributeFailure(t, e, op, held, "42501")
	roleAttributeApply(t, e, admin, roleAttributeAlter("creator", parser.RoleOptions{Present: parser.RoleSuperuser, Superuser: true}))
	roleAttributeApply(t, e, op, roleAttributeAlter("unrelated", parser.RoleOptions{Present: parser.RoleLogin}))
	// Effective self, not authenticated identity, controls PASSWORD-only.
	roleAttributeApply(t, e, op, &parser.RoleSelection{Name: "unrelated"})
	roleAttributeApply(t, e, op, &parser.AlterRole{Identity: "current_user", Options: parser.RoleOptions{Present: parser.RolePassword, PasswordNull: true}})
	roleAttributeFailure(t, e, op, roleAttributeAlter("creator", parser.RoleOptions{Present: parser.RolePassword, PasswordNull: true}), "42501")
}

func TestRoleAttributesMembershipNonInheritanceAndAdminTraversal(t *testing.T) {
	e := New()
	admin, done := roleOperation(t, e, "postgres")
	defer done()
	upstream := roleAttributeCreate(t, e, admin, "upstream", true, allRoleOptions())
	member := roleAttributeCreate(t, e, admin, "member", false, parser.RoleOptions{})
	middle := roleAttributeCreate(t, e, admin, "middle", false, parser.RoleOptions{})
	target := roleAttributeCreate(t, e, admin, "target", false, parser.RoleOptions{})
	for _, grant := range []*parser.RoleMembership{
		{Targets: []parser.RoleSpec{{Name: upstream.Name}}, Members: []parser.RoleSpec{{Name: member.Name}}},
		{Targets: []parser.RoleSpec{{Name: middle.Name}}, Members: []parser.RoleSpec{{Name: member.Name}}, Options: []parser.MembershipOption{{Name: "inherit"}, {Name: "set"}}},
		{Targets: []parser.RoleSpec{{Name: target.Name}}, Members: []parser.RoleSpec{{Name: middle.Name}}, Options: []parser.MembershipOption{{Name: "admin", Value: true}, {Name: "inherit"}, {Name: "set"}}},
	} {
		roleAttributeApply(t, e, admin, grant)
	}
	read := e.Roles()
	got, _ := read.Descriptor(member.Ref)
	if got != member {
		t.Fatal("attributes inherited", got)
	}
	if inherited, err := read.InheritsRole(member.Ref, upstream.Ref); err != nil || !inherited {
		t.Fatal("object inheritance control", err)
	}
	if allowed, err := read.AdminOfRole(member.Ref, target.Ref); err != nil || !allowed {
		t.Fatal("transitive ADMIN", err)
	}
	if allowed, err := read.AdminOfRole(member.Ref, member.Ref); err != nil || allowed {
		t.Fatal("self ADMIN", err)
	}
	if allowed, err := read.AdminOfRole(member.Ref, upstream.Ref); err != nil || allowed {
		t.Fatal("SET/INHERIT implies ADMIN", err)
	}
	if _, err := read.AdminOfRole(member.Ref, authorization.PrincipalRef{}); err == nil {
		t.Fatal("invalid ADMIN endpoint")
	}
	if _, err := e.ResolveLogin("member"); err == nil {
		t.Fatal("LOGIN inherited from membership")
	}
	roleAttributeApply(t, e, admin, roleAttributeAlter("member", parser.RoleOptions{Present: parser.RoleLogin, Login: true}))
	op, closeMember := roleOperation(t, e, "member")
	defer closeMember()
	roleAttributeFailure(t, e, op, &parser.CreateRole{Name: parser.Identifier{Text: "inherited_creation"}}, "42501")
	roleAttributeFailure(t, e, op, roleAttributeAlter("target", parser.RoleOptions{Present: parser.RoleLogin}), "42501")
	roleAttributeApply(t, e, admin, roleAttributeAlter("member", parser.RoleOptions{Present: parser.RoleCreateRole | parser.RoleInherit, CreateRole: true}))
	roleAttributeApply(t, e, op, roleAttributeAlter("target", parser.RoleOptions{Present: parser.RoleLogin, Login: true}))
	if !reflect.DeepEqual(read.Memberships(), e.Roles().Memberships()) {
		t.Fatal("INHERIT rewrote explicit membership bits")
	}
	if allowed, err := e.Roles().AdminOfRole(target.Ref, upstream.Ref); err != nil || allowed {
		t.Fatal("ADMIN traversal leaked authority", err)
	}
}

func TestRoleAttributesPublicationAndSnapshots(t *testing.T) {
	e := New()
	admin, done := roleOperation(t, e, "postgres")
	defer done()
	roleAttributeCreate(t, e, admin, "snapshot", false, allRoleOptions())
	old := e.Roles()
	d, _ := old.Lookup("snapshot")
	edges := old.Memberships()
	roleAttributeFailure(t, e, admin, roleAttributeAlter("snapshot", parser.RoleOptions{Present: parser.RoleLogin | parser.RolePassword | parser.RoleValidUntil, Login: false, Password: "replacement", ValidUntil: "bad"}), "22007")
	roleAttributeFailure(t, e, admin, &parser.CreateRole{Name: parser.Identifier{Text: "bad_expiry"}, Options: parser.RoleOptions{Present: parser.RoleValidUntil, ValidUntil: "bad"}}, "22007")
	roleAttributeApply(t, e, admin, roleAttributeAlter("snapshot", parser.RoleOptions{Present: parser.RolePassword | parser.RoleValidUntil, PasswordNull: true, ValidUntil: "-infinity"}))
	fresh, _ := e.Roles().Lookup("snapshot")
	if fresh.Ref != d.Ref || fresh.Password != "" || !fresh.HasValidUntil || fresh.ValidUntil != sqltype.TimestampNegativeInfinity {
		t.Fatal(fresh)
	}
	retained, _ := old.Lookup("snapshot")
	if retained != d || !reflect.DeepEqual(edges, old.Memberships()) {
		t.Fatal("old root changed")
	}
	copyEdges := old.Memberships()
	if len(copyEdges) > 0 {
		copyEdges[0] = authorization.Membership{}
		if !reflect.DeepEqual(edges, old.Memberships()) {
			t.Fatal("mutable edge snapshot")
		}
	}
	savedNext := e.RoleStore.NextPrincipal()
	e.RoleStore.InstallPrincipalFrontierLocked(^uint64(0))
	roleAttributeFailure(t, e, admin, &parser.CreateRole{Name: parser.Identifier{Text: "exhausted"}}, "")
	e.RoleStore.InstallPrincipalFrontierLocked(savedNext)
	savedRoot := e.RoleStore.Published
	exhaustedRoot := *savedRoot
	exhaustedRoot.Revision = ^uint64(0)
	e.RoleStore.PublishLocked(&exhaustedRoot)
	roleAttributeFailure(t, e, admin, roleAttributeAlter("snapshot", parser.RoleOptions{Present: parser.RoleLogin}), "")
	e.RoleStore.PublishLocked(savedRoot)
	savedRevision := e.RoleStore.NextRevision()
	e.RoleStore.InstallRevisionFrontierLocked(^uint64(0))
	roleAttributeFailure(t, e, admin, roleAttributeAlter("snapshot", parser.RoleOptions{}), "")
	e.RoleStore.InstallRevisionFrontierLocked(savedRevision)
	for _, create := range []bool{false, true} {
		t.Run(fmt.Sprintf("canceled final publication/create%v", create), func(t *testing.T) {
			fixture := New()
			bootstrap, closeBootstrap := roleOperation(t, fixture, "postgres")
			defer closeBootstrap()
			roleAttributeCreate(t, fixture, bootstrap, "creator_cancel", true, parser.RoleOptions{Present: parser.RoleCreateRole, CreateRole: true})
			roleAttributeCreate(t, fixture, bootstrap, "cancel_target", false, parser.RoleOptions{})
			actor, closeActor := roleOperation(t, fixture, "creator_cancel")
			defer closeActor()
			roleAttributeApply(t, fixture, bootstrap, &parser.RoleMembership{
				Targets: []parser.RoleSpec{{Name: "cancel_target"}}, Members: []parser.RoleSpec{{Name: "creator_cancel"}},
				Options: []parser.MembershipOption{{Name: "admin", Value: true}, {Name: "inherit"}, {Name: "set"}},
			})
			beforePublication := fixture.RoleStore.Published
			next := fixture.RoleStore.NextPrincipal()
			revision := fixture.RoleStore.NextRevision()
			ctx, cancel := context.WithCancel(actor.Context())
			defer cancel()
			fence := &identityRolePublicationContext{Context: ctx, session: actor.Session, cancel: cancel}
			actor.Ctx = fence
			var stmt parser.Statement = roleAttributeAlter("cancel_target", parser.RoleOptions{Present: parser.RoleLogin, Login: true})
			if create {
				stmt = &parser.CreateRole{Name: parser.Identifier{Text: "cancel_created"}}
			}
			_, err := fixture.executeRoleAttributes(&StatementScope{Op: actor}, stmt, 0)
			if !fence.observed || !errors.Is(err, context.Canceled) || fixture.RoleStore.Published != beforePublication || fixture.RoleStore.NextPrincipal() != next || fixture.RoleStore.NextRevision() != revision {
				t.Fatal("final cancellation leaked catalog, edge, allocator", fence.observed, err)
			}
		})
	}
}

func TestRoleAttributesControlsRetention(t *testing.T) {
	e := New()
	admin, done := roleOperation(t, e, "postgres")
	defer done()
	roleAttributeCreate(t, e, admin, "controls", true, allRoleOptions())
	root := e.Roles()
	d, _ := root.Lookup("controls")
	var dump bytes.Buffer
	if err := e.Dump(context.Background(), &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), d.Password) || strings.Contains(dump.String(), "CREATE ROLE") || strings.Contains(dump.String(), "PASSWORD") {
		t.Fatal("Dump serialized shared password metadata")
	}
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := e.Restore(context.Background(), strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	got, _ := e.Roles().Lookup("controls")
	if got != d {
		t.Fatal("controls changed descriptor", got)
	}
	before := e.RoleStore.Published
	if err := e.Restore(context.Background(), strings.NewReader("CREATE ROLE forbidden")); err == nil {
		t.Fatal("Restore role whitelist widened")
	}
	if e.RoleStore.Published != before {
		t.Fatal("failed Restore changed roles")
	}
	// Real admission/authority continue to consume the retained root.
	op, closeRole := roleOperation(t, e, "controls")
	defer closeRole()
	roleAttributeCreate(t, e, op, "post_controls", false, parser.RoleOptions{})
}
