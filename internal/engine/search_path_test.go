package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestSearchPathLookupKinds(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE TEMP SEQUENCE temp_seq", "SET search_path TO missing,b,b,a"} {
		roleExecute(t, e, op, q)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	names := func(path []ObjectAddress) []string {
		result := make([]string, len(path))
		for i, a := range path {
			result[i] = auth.catalog.state.descriptors[a].Name()
		}
		return result
	}
	temp := auth.catalog.state.descriptors[temporaryNamespace(auth)].Name()
	for _, tc := range []struct {
		kind lookupKind
		want []string
	}{{relationLookup, []string{temp, "pg_catalog", "b", "a"}}, {typeLookup, []string{temp, "pg_catalog", "b", "a"}}, {functionLookup, []string{"pg_catalog", "b", "a"}}, {operatorLookup, []string{"pg_catalog", "b", "a"}}} {
		path, err := e.lookupPath(auth, tc.kind)
		if err != nil || !reflect.DeepEqual(names(path), tc.want) {
			t.Fatal(tc.kind, names(path), tc.want, err)
		}
	}
}

func TestSearchPathCurrentSchemasDescriptorAndACL(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER reader", "CREATE SCHEMA hidden", "CREATE SCHEMA visible"} {
		roleExecute(t, e, op, q)
	}
	reader, _ := e.Roles().Lookup("reader")
	if err := e.SetObjectACL(context.Background(), schemaRef(t, e, "visible"), ACLValue{Explicit: true, Entries: []ACLEntry{{Grantor: e.roles.names["postgres"], Grantee: ACLGrantee{Role: reader.Ref}, Privileges: UsagePrivilege}}}); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
	roleExecute(t, e, op, "SET search_path TO hidden,missing,visible,visible,public")
	got := tuples(roleExecute(t, e, op, "SELECT current_schemas(false)"))
	if len(got) != 1 || len(got[0]) != 1 || got[0][0] != "{visible,public}" {
		t.Fatal(got)
	}
	d, err := e.Describe(op, statement(t, "SELECT current_schemas(false)"))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Results) != 1 {
		t.Fatal(d)
	}
	c := d.Results[0]
	if c.Usage.Type.ID() != sqltype.NameArray || c.Usage.Type.Name() != "_name" || c.Usage.Type.DatabaseName() != "_NAME" || c.Usage.Typmod != -1 || c.Usage.Collation.ID != 950 || c.OriginTable != 0 || c.OriginAttribute != 0 {
		t.Fatal(c)
	}
	roleErrorPosition(t, e, op, "CREATE TABLE no_fallback(id INT)", "42501", "permission denied for schema visible", "", "14")
	schemaFail(t, e, op, "CREATE TABLE public.bad(id pg_catalog._name)", "0A000")
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	path, _, err := e.explicitPath(auth)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, a := range path {
		got = append(got, auth.catalog.state.descriptors[a].Name())
	}
	if !reflect.DeepEqual(got, []string{"visible", "public"}) {
		t.Fatal(got)
	}
	_, err = e.lookupNamespaces(auth, parser.ObjectName{Parts: []parser.Identifier{{Text: "hidden"}, {Text: "int4"}}}, typeLookup)
	state(t, err, "42501")
}

func TestSearchPathMissingQualifiedNamespace(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	name := parser.ObjectName{Parts: []parser.Identifier{{Text: "missing_path_ns", Pos: parser.Position{Character: 8}}, {Text: "int4"}}}
	for _, kind := range []lookupKind{typeLookup, functionLookup, operatorLookup} {
		_, err := e.lookupNamespaces(auth, name, kind)
		state(t, err, "3F000")
	}
	path, err := e.lookupNamespaces(auth, name, relationLookup)
	if err != nil || path.count() != 0 {
		t.Fatal(path, err)
	}
}

// Scalar unit binders use real catalog/session authorization like public SQL.
func newTestExpressionBinder(t *testing.T, ctx context.Context, e *Engine, input sqltype.InputEnvironment, expected *StatementDescription) *expressionBinder {
	t.Helper()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	return newExpressionBinder(ctx, e, input, op, expected)
}

func TestSearchPathExplicitTempPlacementAndForeignFiltering(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	peer, peerDone := roleOperation(t, e, "postgres")
	defer peerDone()
	for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE TEMP SEQUENCE temp_seq"} {
		roleExecute(t, e, op, q)
	}
	roleExecute(t, e, peer, "CREATE TEMP SEQUENCE peer_seq")
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	other, err := e.authorizationLocked(peer)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	temp := auth.catalog.state.descriptors[temporaryNamespace(auth)].Name()
	foreign := other.catalog.state.descriptors[temporaryNamespace(other)].Name()
	e.mu.Unlock()
	roleExecute(t, e, op, "SET search_path TO b,pg_catalog,pg_temp,"+temp+","+foreign+",a")
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err = e.authorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		kind lookupKind
		want []string
	}{
		{relationLookup, []string{"b", "pg_catalog", temp, "a"}},
		{typeLookup, []string{"b", "pg_catalog", temp, "a"}},
		{functionLookup, []string{"b", "pg_catalog", "a"}},
		{operatorLookup, []string{"b", "pg_catalog", "a"}},
	} {
		path, err := e.lookupPath(auth, tc.kind)
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, len(path))
		for i, a := range path {
			got[i] = auth.catalog.state.descriptors[a].Name()
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatal(tc.kind, got, tc.want)
		}
	}
}
