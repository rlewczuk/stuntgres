package core_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func extensionUnitGrant(name string, role string) *parser.ObjectGrant {
	return &parser.ObjectGrant{Kind: "database", Privileges: []parser.GrantPrivilege{{Name: "create"}}, Relations: []parser.ObjectName{{Parts: []parser.Identifier{{Text: name}}}}, Grantees: []parser.GrantGrantee{{Role: parser.RoleSpec{Name: role}}}}
}

func TestExtensionSchemaACL(t *testing.T) {
	e := engine.NewWithSettings(nil, "ext_acl_db", false)
	admin, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	actor := roleAttributeCreate(t, e, admin, "ext_acl_actor", true, parser.RoleOptions{})
	ns := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 60000}, ObjectRevision: 1, ObjectName: "ext_acl", Owner: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), ACL: authorization.ACLValue{Explicit: true}}
	stage := e.BeginCatalogStage(e.CatalogRead())
	if err := stage.Create(ns); err != nil {
		t.Fatal(err)
	}
	enginetest.PublishOperationStage(t, e, admin, stage)
	if err := enginetest.ExtensionUnitExecute(t, e, admin, extensionUnitGrant("ext_acl_db", "ext_acl_actor")); err != nil {
		t.Fatal(err)
	}
	op, actorDone := enginetest.RoleOperation(t, e, "ext_acl_actor")
	defer actorDone()
	install := &parser.CreateExtension{Name: parser.Identifier{Text: "unaccent"}, Options: []parser.ExtensionOption{{Name: "schema", Value: parser.Identifier{Text: "ext_acl"}}}}
	if err := enginetest.ExtensionUnitExecute(t, e, op, install); err != nil {
		t.Fatal("trusted explicit schema requires neither USAGE nor CREATE", err)
	}
	ext := e.Catalog.State.MustExtension(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}))
	if ext.Owner != actor.Ref {
		t.Fatal("extension owner")
	}
	for _, a := range ext.Members {
		if a.ClassID != ids.TSTemplateClassID {
			owner, _, _ := access.DescriptorACL(e.Catalog.State.Get(a))
			if owner != e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres") {
				t.Fatal("trusted member owner")
			}
		}
	}
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "ext_acl"}, {Text: "unaccent"}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "Hôtel"}}}}
	binder := core.NewExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, op, nil)
	_, err := binder.BindFunctionProvider(raw)
	enginetest.State(t, err, "42501")
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	path, err := e.Settings.Definitions()["search_path"].Parse("ext_acl")
	if err != nil {
		t.Fatal(err)
	}
	core.AuthorizationSettings(&auth).Mu.Lock()
	core.AuthorizationSettings(&auth).Typed["search_path"] = path
	core.AuthorizationSettings(&auth).Values["search_path"] = "ext_acl"
	core.AuthorizationSettings(&auth).Mu.Unlock()
	binder = core.NewExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, op, nil)
	raw.Name.Parts = raw.Name.Parts[1:]
	_, err = binder.BindFunctionProvider(raw)
	enginetest.State(t, err, "42883")
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: ns.Owner, Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: authorization.UsagePrivilege}}}
	if err := enginetest.SetObjectACL(e, context.Background(), e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), ns.Address()), acl); err != nil {
		t.Fatal(err)
	}
	binder = core.NewExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, op, nil)
	node, err := binder.BindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(context.Canceled)
	if err := scope.Gate(&parser.Select{}); err != nil {
		_ = scope.Finish(err)
		t.Fatal(err)
	}
	value, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{
		Context: op.Context(), Host: core.HostOf(scope), Catalog: scope.ResolutionCatalog(),
		Input: scope.Input, Output: scope.Output,
	}})
	err = scope.Finish(err)
	text, _ := value.Text()
	if err != nil || text != "Hotel" {
		t.Fatal(text, err)
	}
}
func TestExtensionDatabaseCreateAuthority(t *testing.T) {
	e := engine.NewWithSettings(nil, "ext_authority", false)
	admin, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	actor := roleAttributeCreate(t, e, admin, "ext_db_actor", true, parser.RoleOptions{Present: parser.RoleCreateDB, CreateDB: true})
	op, actorDone := enginetest.RoleOperation(t, e, "ext_db_actor")
	defer actorDone()
	install := &parser.CreateExtension{Name: parser.Identifier{Text: "unaccent"}}
	enginetest.State(t, enginetest.ExtensionUnitExecute(t, e, op, install), "42501")
	grant := extensionUnitGrant("ext_authority", "ext_db_actor")
	grant.GrantOption = true
	if err := enginetest.ExtensionUnitExecute(t, e, admin, grant); err != nil {
		t.Fatal(err)
	}
	db := e.Catalog.State.MustDatabase(catalog.CurrentDatabaseAddress)
	if authorization.ACLRights(db.ACL, db.Owner, []authorization.PrincipalRef{actor.Ref}, authorization.CreatePrivilege, true) != authorization.CreatePrivilege {
		t.Fatal("grant option missing")
	}
	delegated := extensionUnitGrant("ext_authority", "")
	delegated.Grantees = []parser.GrantGrantee{{Public: true}}
	if err := enginetest.ExtensionUnitExecute(t, e, op, delegated); err != nil {
		t.Fatal(err)
	}
	db = e.Catalog.State.MustDatabase(catalog.CurrentDatabaseAddress)
	found := false
	for _, entry := range db.ACL.Entries {
		if entry.Grantee.Public && entry.Grantor == actor.Ref && entry.Privileges&authorization.CreatePrivilege != 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("grantor provenance lost")
	}
	enginetest.State(t, enginetest.ExtensionUnitExecute(t, e, admin, &parser.DropRole{Names: []string{actor.Name}}), "2BP01")
	references := descriptorReferences(db)
	found = false
	for _, ref := range references {
		found = found || ref == actor.Ref
	}
	if !found {
		t.Fatal("database ACL DROP dependency missing")
	}
	if err := enginetest.ExtensionUnitExecute(t, e, op, install); err != nil {
		t.Fatal(err)
	}
	replacement := engine.NewWithSettings(nil, "different_attachment", false)
	if _, err := e.ReplaceCatalog(context.Background(), replacement, replacement.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	retained := e.Catalog.State.MustDatabase(catalog.CurrentDatabaseAddress)
	if retained.Address() != db.Address() || retained.Name() != "ext_authority" || retained.Owner != db.Owner || !reflect.DeepEqual(retained.ACL, db.ACL) {
		t.Fatal("replacement overwrote destination database")
	}
	revoke := extensionUnitGrant("ext_authority", "ext_db_actor")
	revoke.Revoke = true
	enginetest.State(t, enginetest.ExtensionUnitExecute(t, e, admin, revoke), "2BP01")
	revoke.Cascade = true
	if err := enginetest.ExtensionUnitExecute(t, e, admin, revoke); err != nil {
		t.Fatal(err)
	}
	enginetest.State(t, enginetest.ExtensionUnitExecute(t, e, op, install), "42501")
}
