package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestInsertConflictACLCurrentManifest(t *testing.T) {
	f := insertConflictUniqueFixture(t)
	admin, done := enginetest.RoleOperation(t, f.e, "postgres")
	defer done()
	if _, err := f.e.Execute(admin, &parser.CreateRole{Name: parser.Identifier{Text: "conflict_reader"}, Options: parser.RoleOptions{Present: parser.RoleLogin, Login: true}}, 0, nil); err != nil {
		t.Fatal(err)
	}
	reader, doneReader := enginetest.RoleOperation(t, f.e, "conflict_reader")
	defer doneReader()
	role, _ := f.e.Roles().Lookup("conflict_reader")
	owner, _ := f.e.Roles().Lookup("postgres")
	f.parent.Owner = owner.Ref
	f.index.Owner = owner.Ref
	ref := f.e.Catalog.Domain.Ref(f.e.Catalog.State.Epoch(), f.parent.Address())
	acl := func(privileges authorization.Privileges) authorization.ACLValue {
		return authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner.Ref, Grantee: authorization.ACLGrantee{Role: role.Ref}, Privileges: privileges}}}
	}
	if err := enginetest.SetObjectACL(f.e, context.Background(), ref, acl(authorization.InsertPrivilege)); err != nil {
		t.Fatal(err)
	}
	b := dml.BindingForConflict(ref, f.parent, dml.NewInsertConflictState(&analyze.InsertConflict{Targets: []int{0, 1}, Reads: []int{0, 1}}))
	check := func(want string) {
		t.Helper()
		f.e.Mu.Lock()
		auth, err := f.e.AuthorizationLocked(reader)
		f.e.Mu.Unlock()
		if err == nil {
			err = analyze.CheckBinding(auth, b, &parser.Insert{})
		}
		if want == "" {
			if err != nil {
				t.Fatal(err)
			}
			return
		}
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != want {
			t.Fatal(want, err)
		}
	}
	check("42501")
	column := ref
	column.Address.SubID = 10
	if err := enginetest.SetObjectACL(f.e, context.Background(), column, acl(authorization.SelectPrivilege)); err != nil {
		t.Fatal(err)
	}
	check("42501")
	column.Address.SubID = 20
	if err := enginetest.SetObjectACL(f.e, context.Background(), column, acl(authorization.SelectPrivilege)); err != nil {
		t.Fatal(err)
	}
	check("")
	if err := enginetest.SetObjectACL(f.e, context.Background(), column, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	check("42501")
	// Targetless DO NOTHING has no hidden selected-index SELECT manifest.
	b.Insert.Conflict.Reads = nil
	check("")
}
