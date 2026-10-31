package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/parser"
)

func TestPGNamespaceTemporarySuperuserAuthority(t *testing.T) {
	e := engine.New()
	creator, closeCreator := enginetest.RoleOperation(t, e, "postgres")
	t.Cleanup(closeCreator)
	enginetest.RoleExecute(t, e, creator, "CREATE ROLE namespace_usage_reader")
	enginetest.RoleExecute(t, e, creator, "CREATE TEMP TABLE namespace_usage_object(i INT)")
	namespace, token := creator.Session.TemporaryNamespace, creator.Session.SessionToken
	toast := assertTemporaryNamespacePair(t, e, token, namespace)
	name := e.Catalog.State.Get(namespace).Name()
	qualified := parser.ObjectName{Parts: []parser.Identifier{{Text: name}, {Text: "namespace_usage_object"}}}
	peer, closePeer := enginetest.RoleOperation(t, e, "postgres")
	defer closePeer()
	superuser, err := core.OperationAuthorization(peer)
	if err != nil {
		t.Fatal(err)
	}
	check := func(auth core.AuthorizationView, allowed bool) {
		t.Helper()
		for _, kind := range []analyze.LookupKind{analyze.RelationLookup, analyze.TypeLookup, analyze.FunctionLookup, analyze.OperatorLookup, analyze.CollationLookup, analyze.ConversionLookup} {
			selected, err := core.LookupNamespacesSnapshot(auth, qualified, kind)
			if allowed {
				if err != nil || selected.Qualified != namespace {
					t.Fatal("captured superuser namespace usage", kind, selected, err)
				}
			} else {
				defaultUnitDiagnostic(t, err, "42501", "permission denied for schema "+name)
			}
		}
		if core.TemporaryNamespace(auth) != (ids.ObjectAddress{}) || access.OwnTemporaryNamespace(core.TemporaryNamespace(auth), auth.Catalog.MustNamespace(namespace)) {
			t.Fatal("qualified usage granted physical namespace adoption")
		}
	}
	// Text relation targets and regclass input share the captured resolver.
	// Qualified foreign TEMP lookup uses the captured current role, never the
	// authenticated superuser after SET ROLE, and does not open TEMP storage.
	checkInput := func(auth core.AuthorizationView, allowed bool) {
		t.Helper()
		input := regobj.NewClassInput(regobj.InputCapture{
			Authority: regobj.Authority{View: auth.View, Environment: auth},
			Database: peer.Session.DatabaseName,
			Token: peer.Session.SessionToken,
			Temporary: core.TemporaryNamespace(auth),
		})
		for _, resolve := range []func(context.Context, string) (uint32, error){input.ResolveRegclass, input.ResolveRelationName} {
			got, err := resolve(context.Background(), name+".namespace_usage_object")
			if !allowed {
				defaultUnitDiagnostic(t, err, "42501", "permission denied for schema "+name)
				continue
			}
			want := auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: "namespace_usage_object"})
			if err != nil || ids.OID(got) != want.ObjectID {
				t.Fatal("captured superuser text target resolution", got, want, err)
			}
		}
	}
	checkInput(superuser, true)
	check(superuser, true)
	enginetest.RoleExecute(t, e, peer, "SET ROLE namespace_usage_reader")
	ordinary, err := core.OperationAuthorization(peer)
	if err != nil {
		t.Fatal(err)
	}
	if ordinary.State.Authenticated != superuser.State.Authenticated || ordinary.State.CurrentUser == superuser.State.CurrentUser {
		t.Fatal("SET ROLE did not preserve bootstrap authenticator and change actor")
	}
	check(ordinary, false)
	checkInput(ordinary, false)
	checkInput(superuser, true)
	check(superuser, true) // Captured authority does not follow live SET ROLE.
	enginetest.RoleExecute(t, e, peer, "RESET ROLE")
	check(ordinary, false) // Captured ordinary actor does not follow live reset.
	checkInput(ordinary, false)
	enginetest.RoleErrorPosition(t, e, peer, "CREATE TABLE "+name+".foreign_create(i INT)", "42P16", "cannot create relations in temporary schemas of other sessions", "", "14")
	closeCreator()
	assertTemporaryNamespacePair(t, e, token, namespace)
	retired, err := core.OperationAuthorization(peer)
	if err != nil {
		t.Fatal(err)
	}
	check(retired, true)
	if retired.Catalog.Get(toast) != superuser.Catalog.Get(toast) {
		t.Fatal("retirement changed captured toast identity")
	}
	enginetest.RoleErrorPosition(t, e, peer, "SELECT i FROM "+name+".namespace_usage_object", "42P01", `relation "`+name+`.namespace_usage_object" does not exist`, "", "15")
	enginetest.RoleErrorPosition(t, e, peer, "CREATE TABLE "+name+".retired_create(i INT)", "42P16", "cannot create relations in temporary schemas of other sessions", "", "14")
	enginetest.RoleExecute(t, e, peer, "SET ROLE namespace_usage_reader")
	denied, err := core.OperationAuthorization(peer)
	if err != nil {
		t.Fatal(err)
	}
	check(denied, false)
	enginetest.RoleExecute(t, e, peer, "RESET ROLE")
}
