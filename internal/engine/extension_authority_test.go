package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func extensionUnitExecute(t *testing.T, e *Engine, op *Operation, stmt parser.Statement) error {
	t.Helper()
	scope, err := e.StartStatement(op)
	if err != nil {
		return err
	}
	_, err = scope.Execute(stmt, 0, nil)
	return scope.Finish(err)
}
func extensionUnitGrant(name string, role string) *parser.ObjectGrant {
	return &parser.ObjectGrant{Kind: "database", Privileges: []parser.GrantPrivilege{{Name: "create"}}, Relations: []parser.ObjectName{{Parts: []parser.Identifier{{Text: name}}}}, Grantees: []parser.GrantGrantee{{Role: parser.RoleSpec{Name: role}}}}
}
func extensionUnitError(t *testing.T, err error, code string) {
	t.Helper()
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != code {
		t.Fatalf("got %v, want SQLSTATE %s", err, code)
	}
}

func TestExtensionSchemaACL(t *testing.T) {
	e := NewWithSettings(nil, "ext_acl_db", false)
	admin, done := roleOperation(t, e, "postgres")
	defer done()
	actor := roleAttributeCreate(t, e, admin, "ext_acl_actor", true, parser.RoleOptions{})
	ns := &namespaceDescriptor{address: ObjectAddress{ClassID: NamespaceClassID, ObjectID: 60000}, revision: 1, name: "ext_acl", owner: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), acl: authorization.ACLValue{Explicit: true}}
	stage := e.BeginCatalogStage(e.CatalogRead())
	if err := stage.Create(ns); err != nil {
		t.Fatal(err)
	}
	extensionUnitPublish(t, e, admin, stage)
	if err := extensionUnitExecute(t, e, admin, extensionUnitGrant("ext_acl_db", "ext_acl_actor")); err != nil {
		t.Fatal(err)
	}
	op, actorDone := roleOperation(t, e, "ext_acl_actor")
	defer actorDone()
	install := &parser.CreateExtension{Name: parser.Identifier{Text: "unaccent"}, Options: []parser.ExtensionOption{{Name: "schema", Value: parser.Identifier{Text: "ext_acl"}}}}
	if err := extensionUnitExecute(t, e, op, install); err != nil {
		t.Fatal("trusted explicit schema requires neither USAGE nor CREATE", err)
	}
	ext := e.state.descriptors[e.state.names[NameKey{ClassID: ExtensionClassID, Name: "unaccent"}]].(*extensionDescriptor)
	if ext.owner != actor.Ref {
		t.Fatal("extension owner")
	}
	for _, a := range ext.members {
		if a.ClassID != TSTemplateClassID {
			owner, _, _ := descriptorACL(e.state.descriptors[a])
			if owner != e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres") {
				t.Fatal("trusted member owner")
			}
		}
	}
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "ext_acl"}, {Text: "unaccent"}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "Hôtel"}}}}
	binder := newExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, op, nil)
	_, err := binder.bindInstalledFunction(raw)
	extensionUnitError(t, err, "42501")
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	path, err := e.settingsDefinitions["search_path"].parse("ext_acl")
	if err != nil {
		t.Fatal(err)
	}
	auth.settings.mu.Lock()
	auth.settings.typed["search_path"] = path
	auth.settings.values["search_path"] = "ext_acl"
	auth.settings.mu.Unlock()
	binder = newExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, op, nil)
	raw.Name.Parts = raw.Name.Parts[1:]
	_, err = binder.bindInstalledFunction(raw)
	extensionUnitError(t, err, "42883")
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: ns.owner, Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: authorization.UsagePrivilege}}}
	if err := e.SetObjectACL(context.Background(), e.domain.Ref(e.state.epoch, ns.address), acl); err != nil {
		t.Fatal(err)
	}
	binder = newExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, op, nil)
	node, err := binder.bindInstalledFunction(raw)
	if err != nil {
		t.Fatal(err)
	}
	value, err := node.Eval(&EvalContext{Context: context.Background()})
	text, _ := value.Text()
	if err != nil || text != "Hotel" {
		t.Fatal(text, err)
	}
}
func TestExtensionDatabaseCreateAuthority(t *testing.T) {
	e := NewWithSettings(nil, "ext_authority", false)
	admin, done := roleOperation(t, e, "postgres")
	defer done()
	actor := roleAttributeCreate(t, e, admin, "ext_db_actor", true, parser.RoleOptions{Present: parser.RoleCreateDB, CreateDB: true})
	op, actorDone := roleOperation(t, e, "ext_db_actor")
	defer actorDone()
	install := &parser.CreateExtension{Name: parser.Identifier{Text: "unaccent"}}
	extensionUnitError(t, extensionUnitExecute(t, e, op, install), "42501")
	grant := extensionUnitGrant("ext_authority", "ext_db_actor")
	grant.GrantOption = true
	if err := extensionUnitExecute(t, e, admin, grant); err != nil {
		t.Fatal(err)
	}
	db := e.state.descriptors[currentDatabaseAddress].(*databaseDescriptor)
	if authorization.ACLRights(db.acl, db.owner, []authorization.PrincipalRef{actor.Ref}, authorization.CreatePrivilege, true) != authorization.CreatePrivilege {
		t.Fatal("grant option missing")
	}
	delegated := extensionUnitGrant("ext_authority", "")
	delegated.Grantees = []parser.GrantGrantee{{Public: true}}
	if err := extensionUnitExecute(t, e, op, delegated); err != nil {
		t.Fatal(err)
	}
	db = e.state.descriptors[currentDatabaseAddress].(*databaseDescriptor)
	found := false
	for _, entry := range db.acl.Entries {
		if entry.Grantee.Public && entry.Grantor == actor.Ref && entry.Privileges&authorization.CreatePrivilege != 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("grantor provenance lost")
	}
	extensionUnitError(t, extensionUnitExecute(t, e, admin, &parser.DropRole{Names: []string{actor.Name}}), "2BP01")
	references := descriptorReferences(db)
	found = false
	for _, ref := range references {
		found = found || ref == actor.Ref
	}
	if !found {
		t.Fatal("database ACL DROP dependency missing")
	}
	if err := extensionUnitExecute(t, e, op, install); err != nil {
		t.Fatal(err)
	}
	replacement := NewWithSettings(nil, "different_attachment", false)
	if _, err := e.replaceCatalog(context.Background(), replacement.CatalogRead(), admin); err != nil {
		t.Fatal(err)
	}
	retained := e.state.descriptors[currentDatabaseAddress].(*databaseDescriptor)
	if retained != db || retained.name != "ext_authority" {
		t.Fatal("replacement overwrote destination database")
	}
	revoke := extensionUnitGrant("ext_authority", "ext_db_actor")
	revoke.Revoke = true
	extensionUnitError(t, extensionUnitExecute(t, e, admin, revoke), "2BP01")
	revoke.Cascade = true
	if err := extensionUnitExecute(t, e, admin, revoke); err != nil {
		t.Fatal(err)
	}
	extensionUnitError(t, extensionUnitExecute(t, e, op, install), "42501")
}
