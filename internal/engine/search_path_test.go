package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
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
	names := func(path []ids.ObjectAddress) []string {
		result := make([]string, len(path))
		for i, a := range path {
			result[i] = auth.catalog.Get(a).Name()
		}
		return result
	}
	temp := auth.catalog.Get(temporaryNamespace(auth)).Name()
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
	for _, q := range []string{"CREATE SCHEMA hidden", "CREATE SCHEMA visible"} {
		domainUnitPublish(t, e, domainUnitStage(t, e, op, q))
	}
	e.mu.Lock()
	reader := e.authorizationDomain.Principal(e.nextPrincipal)
	e.nextPrincipal++
	roles := cloneRoleFixture(e.roles)

	putFixtureRole(roles, authorization.RoleDescriptor{Ref: reader, Name: "reader", Login: true, Inherit: true, ConnectionLimit: -1})
	e.roles = roles
	e.mu.Unlock()
	if err := e.SetObjectACL(context.Background(), schemaRef(t, e, "visible"), authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Role: reader}, Privileges: authorization.UsagePrivilege}}}); err != nil {
		t.Fatal(err)
	}
	table := domainUnitTable(t, e, op, "CREATE TABLE public.bounded(id pg_catalog._name)")
	setting, err := e.settingsDefinitions["search_path"].parse("hidden,missing,visible,visible,public")
	if err != nil {
		t.Fatal(err)
	}
	readerOp, readerDone := roleOperation(t, e, "reader")
	defer readerDone()
	readerOp.session.settings.mu.Lock()
	readerOp.session.settings.typed["search_path"] = setting
	readerOp.session.settings.mu.Unlock()
	scope, err := e.StartStatement(readerOp)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := scope.Finish(nil); err != nil {
			t.Error(err)
		}
	}()
	if err = scope.gate(statement(t, "SELECT current_schemas(false)")); err != nil {
		t.Fatal(err)
	}
	query := domainUnitBindSelect(t, e, readerOp, "SELECT current_schemas(false)")
	if len(query.Root.Schema()) != 1 {
		t.Fatal(query.Root.Schema())
	}
	c := query.Root.Schema()[0]
	if c.Usage.Type.ID() != sqltype.NameArray || c.Usage.Type.Name() != "_name" || c.Usage.Type.DatabaseName() != "_NAME" || c.Usage.Typmod != -1 || c.Usage.Collation.ID != 950 || c.OriginTable != 0 || c.OriginAttribute != 0 || table.columns[0].Usage != c.Usage {
		t.Fatal(c, table.columns)
	}
	value, err := query.Root.(*projectNode).expressions[0].Eval(&EvalContext{Context: context.Background(), statementScope: scope})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := sqltype.PlanCoercion(c.Usage, table.columns[0].Usage, sqltype.Assignment)
	if err != nil {
		t.Fatal(err)
	}
	value, err = plan.Apply(context.Background(), value, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	if err != nil || value.Type() != table.columns[0].Usage.Type {
		t.Fatal(value, err)
	}
	text, err := value.Type().Output(context.Background(), value)
	if err != nil || text != "{visible,public}" {
		t.Fatal(text, err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(readerOp)
	if err != nil {
		t.Fatal(err)
	}
	path, _, err := e.explicitPath(auth)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range path {
		names = append(names, auth.catalog.Get(a).Name())
	}
	if !reflect.DeepEqual(names, []string{"visible", "public"}) {
		t.Fatal(names)
	}
	target, err := e.creationNamespace(auth, parser.ObjectName{Parts: []parser.Identifier{{Text: "no_fallback"}}})
	if err != nil || target.Address != path[0] {
		t.Fatal("creation fell back from first usable namespace", target, err)
	}
	domainUnitDiagnostic(t, auth.CheckCreate(target), "42501", "permission denied for schema visible")
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
	temp := auth.catalog.Get(temporaryNamespace(auth)).Name()
	foreign := other.catalog.Get(temporaryNamespace(other)).Name()
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
			got[i] = auth.catalog.Get(a).Name()
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatal(tc.kind, got, tc.want)
		}
	}
}
