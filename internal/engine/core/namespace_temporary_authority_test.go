package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
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
	check(superuser, true) // Captured authority does not follow live SET ROLE.
	enginetest.RoleExecute(t, e, peer, "RESET ROLE")
	check(ordinary, false) // Captured ordinary actor does not follow live reset.
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
