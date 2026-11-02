package schemacmds_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/parser"
)

func TestTablespaceLifecycleImmutableRename(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	address := ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: catalog.DefaultTablespaceOID}
	base := e.CatalogRead()
	old := base.Get(address).(*catalog.Tablespace)
	stmt := &parser.AlterTablespaceRename{Name: parser.Identifier{Text: old.Name()}, NewName: parser.Identifier{Text: "unit renamed"}}
	if _, err := e.Execute(op, stmt, 0, nil); err != nil {
		t.Fatal(err)
	}
	next := e.CatalogRead().Get(address).(*catalog.Tablespace)
	if next == old || next.Name() != stmt.NewName.Text || next.Owner != old.Owner || !reflect.DeepEqual(next.ACL, old.ACL) || next.Revision() == old.Revision() {
		t.Fatal("rename did not preserve immutable identity/authority", old, next)
	}
	if base.Get(address) != old || base.NameAddress(catalog.DescriptorNameKey(old)) != address || e.CatalogRead().NameAddress(catalog.DescriptorNameKey(old)) != (ids.ObjectAddress{}) || e.CatalogRead().NameAddress(catalog.DescriptorNameKey(next)) != address {
		t.Fatal("name rebinding changed retained read")
	}
	if err := (catalog.TablespaceProvider{}).Validate(e.CatalogRead(), next); err != nil {
		t.Fatal(err)
	}
	state, frontier := e.Catalog.State, e.Catalog.NextRevision()
	ctx, cancel := context.WithCancel(op.Ctx)
	cancel()
	original := op.Ctx
	op.Ctx = ctx
	_, err := e.Execute(op, &parser.AlterTablespaceRename{Name: stmt.NewName, NewName: parser.Identifier{Text: "never published"}}, 0, nil)
	op.Ctx = original
	if !errors.Is(err, context.Canceled) || e.Catalog.State != state || e.Catalog.NextRevision() != frontier {
		t.Fatal("canceled real rename published", err)
	}
}

func TestTablespaceLifecycleOwnershipStageAndNoOp(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	if _, err := e.Execute(op, &parser.CreateRole{Name: parser.Identifier{Text: "tablespace_unrelated"}, DefaultLogin: true}, 0, nil); err != nil {
		t.Fatal(err)
	}
	address := ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: catalog.DefaultTablespaceOID}
	base := e.CatalogRead()
	old := base.Get(address).(*catalog.Tablespace)
	// Seed the actual owner stage with a legitimate typed raw ACL, not a
	// synthetic ordinary tablespace or a public-SQL setup shortcut.
	withACL := *old
	withACL.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: old.Owner, Grantee: authorization.ACLGrantee{Role: old.Owner}, Privileges: authorization.CreatePrivilege, GrantOptions: authorization.CreatePrivilege}}}
	seed := e.BeginCatalogStage(base)
	var err error
	withACL.ObjectRevision, err = seed.CascadeRevision(old.Revision())
	if err == nil {
		err = seed.Replace(&withACL)
	}
	if err != nil {
		t.Fatal(err)
	}
	collationUnitPublish(t, e, seed)
	e.DiscardCatalogStage(seed)
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	stmt := &parser.AlterOwner{Kind: "tablespace", Name: parser.ObjectName{Parts: []parser.Identifier{{Text: old.Name()}}}, Target: parser.RoleSpec{Name: "pg_monitor"}}
	change, err := role.PlanTablespaceOwnership(auth.View, auth, stmt, "pg_monitor", false)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	stage := core.OwnershipStage(e, auth, op)
	err = role.StageOwnership(stage, change.Object, change.Target)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	staged := stage.Get(address).(*catalog.Tablespace)
	if staged.Owner != change.Target || staged.ACL.Entries[0].Grantor != change.Target || staged.ACL.Entries[0].Grantee.Role != change.Target || staged.ACL.Entries[0].GrantOptions != authorization.CreatePrivilege || withACL.ACL.Entries[0].Grantor != old.Owner {
		t.Fatal("owner stage lost ACL rebasing/retained backing", staged)
	}
	collationUnitPublish(t, e, stage)
	e.DiscardCatalogStage(stage)
	// Authenticate a real unrelated login, not a predefined NOLOGIN role.
	unrelated, finish := enginetest.RoleOperation(t, e, "tablespace_unrelated")
	defer finish()
	actor, ok := e.Roles().Lookup("tablespace_unrelated")
	if !ok || !actor.Login || actor.Superuser {
		t.Fatal("no-op caller is not an ordinary login")
	}
	if inherits, err := e.Roles().InheritsRole(actor.Ref, staged.Owner); err != nil || inherits {
		t.Fatal("no-op caller unexpectedly has owner authority", err)
	}
	state, frontier := e.Catalog.State, e.Catalog.NextRevision()
	if _, err := e.Execute(unrelated, stmt, 0, nil); err != nil {
		t.Fatal(err)
	}
	if e.Catalog.State != state || e.Catalog.NextRevision() != frontier || e.CatalogRead().Get(address) != staged {
		t.Fatal("same-owner command staged a replacement or advanced frontier")
	}
	cancelled := e.BeginCatalogStage(e.CatalogRead())
	if err := role.StageOwnership(cancelled, staged, old.Owner); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prepared, err := e.ValidateCatalogStage(ctx, cancelled)
	if err != nil {
		t.Fatal(err)
	}
	cancel() // Real validated ownership candidate, before publication.
	if _, err := enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: cancelled.Base.Epoch()}); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled ownership publication", err)
	}
	e.DiscardCatalogStage(cancelled)
	if e.Catalog.State != state || e.Catalog.NextRevision() != frontier {
		t.Fatal("canceled ownership stage published")
	}
}

type tablespaceLifecycleWriter struct{ writes int }

func (w *tablespaceLifecycleWriter) Write(p []byte) (int, error) {
	w.writes++
	return len(p), nil
}

func TestTablespaceLifecycleControlPreflight(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	name := "unit \"tablespace"
	if _, err := e.Execute(op, &parser.AlterTablespaceRename{Name: parser.Identifier{Text: "pg_default"}, NewName: parser.Identifier{Text: name}}, 0, nil); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := engine.Dump(context.Background(), e, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "ALTER TABLESPACE \"pg_default\" RENAME TO \"unit \"\"tablespace\";\n") || strings.Contains(out.String(), "CREATE TABLESPACE") {
		t.Fatal("capture failed to render forward quoted builtin rename", out.String())
	}
	// Corrupt a private catalog image to exercise capture validation, not to
	// register ordinary native state. Failure must precede the first byte.
	root := catalog.CloneState(e.Catalog.State)
	address := ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: catalog.GlobalTablespaceOID}
	invalid := *root.Get(address).(*catalog.Tablespace)
	invalid.ObjectRevision = 0
	root.SetDescriptor(address, &invalid)
	e.Catalog.PublishLocked(root)
	writer := &tablespaceLifecycleWriter{}
	if err := engine.Dump(context.Background(), e, writer); err == nil || writer.writes != 0 {
		t.Fatal("invalid capture reached output I/O", writer.writes, err)
	}
}
