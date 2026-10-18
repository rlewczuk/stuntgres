package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"testing"
)

func TestPGNamespaceTemporarySuperuserAuthority(t *testing.T) {
	e := New()
	creator, closeCreator := roleOperation(t, e, "postgres")
	t.Cleanup(closeCreator)
	roleExecute(t, e, creator, "CREATE ROLE namespace_usage_reader")
	roleExecute(t, e, creator, "CREATE TEMP TABLE namespace_usage_object(i INT)")
	namespace, token := creator.session.temporaryNamespace, creator.session.token
	toast := assertTemporaryNamespacePair(t, e, token, namespace)
	name := e.state.descriptors[namespace].Name()
	qualified := parser.ObjectName{Parts: []parser.Identifier{{Text: name}, {Text: "namespace_usage_object"}}}
	peer, closePeer := roleOperation(t, e, "postgres")
	defer closePeer()
	superuser, err := peer.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	check := func(auth AuthorizationView, allowed bool) {
		t.Helper()
		for _, kind := range []lookupKind{relationLookup, typeLookup, functionLookup, operatorLookup, collationLookup, conversionLookup} {
			selected, err := e.lookupNamespacesSnapshot(auth, qualified, kind)
			if allowed {
				if err != nil || selected.qualified != namespace {
					t.Fatal("captured superuser namespace usage", kind, selected, err)
				}
			} else {
				defaultUnitDiagnostic(t, err, "42501", "permission denied for schema "+name)
			}
		}
		if temporaryNamespace(auth) != (ids.ObjectAddress{}) || ownTemporaryNamespace(auth, auth.catalog.state.descriptors[namespace].(*namespaceDescriptor)) {
			t.Fatal("qualified usage granted physical namespace adoption")
		}
	}
	check(superuser, true)
	roleExecute(t, e, peer, "SET ROLE namespace_usage_reader")
	ordinary, err := peer.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	if ordinary.state.Authenticated != superuser.state.Authenticated || ordinary.state.CurrentUser == superuser.state.CurrentUser {
		t.Fatal("SET ROLE did not preserve bootstrap authenticator and change actor")
	}
	check(ordinary, false)
	check(superuser, true) // Captured authority does not follow live SET ROLE.
	roleExecute(t, e, peer, "RESET ROLE")
	check(ordinary, false) // Captured ordinary actor does not follow live reset.
	roleErrorPosition(t, e, peer, "CREATE TABLE "+name+".foreign_create(i INT)", "42P16", "cannot create relations in temporary schemas of other sessions", "", "14")
	closeCreator()
	assertTemporaryNamespacePair(t, e, token, namespace)
	retired, err := peer.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	check(retired, true)
	if retired.catalog.state.descriptors[toast] != superuser.catalog.state.descriptors[toast] {
		t.Fatal("retirement changed captured toast identity")
	}
	roleErrorPosition(t, e, peer, "SELECT i FROM "+name+".namespace_usage_object", "42P01", `relation "`+name+`.namespace_usage_object" does not exist`, "", "15")
	roleErrorPosition(t, e, peer, "CREATE TABLE "+name+".retired_create(i INT)", "42P16", "cannot create relations in temporary schemas of other sessions", "", "14")
	roleExecute(t, e, peer, "SET ROLE namespace_usage_reader")
	denied, err := peer.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	check(denied, false)
	roleExecute(t, e, peer, "RESET ROLE")
}
