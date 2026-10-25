package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
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
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	names := func(path []ids.ObjectAddress) []string {
		result := make([]string, len(path))
		for i, a := range path {
			result[i] = auth.Catalog.Get(a).Name()
		}
		return result
	}
	temp := auth.Catalog.Get(temporaryNamespace(auth)).Name()
	for _, tc := range []struct {
		kind analyze.LookupKind
		want []string
	}{{analyze.RelationLookup, []string{temp, "pg_catalog", "b", "a"}}, {analyze.TypeLookup, []string{temp, "pg_catalog", "b", "a"}}, {analyze.FunctionLookup, []string{"pg_catalog", "b", "a"}}, {analyze.OperatorLookup, []string{"pg_catalog", "b", "a"}}} {
		path, err := LookupPath(auth, tc.kind)
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
		roleExecute(t, e, op, q)
	}
	e.Mu.Lock()
	reader := e.RoleStore.Domain.Principal(e.RoleStore.NextPrincipal())
	e.RoleStore.InstallPrincipalFrontierLocked(e.RoleStore.NextPrincipal() + 1)
	roles := cloneRoleFixture(e.RoleStore.Published)

	putFixtureRole(roles, authorization.RoleDescriptor{Ref: reader, Name: "reader", Login: true, Inherit: true, ConnectionLimit: -1})
	e.RoleStore.PublishLocked(roles)
	e.Mu.Unlock()
	if err := e.SetObjectACL(context.Background(), schemaRef(t, e, "visible"), authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Role: reader}, Privileges: authorization.UsagePrivilege}}}); err != nil {
		t.Fatal(err)
	}
	table := domainUnitTable(t, e, op, "CREATE TABLE public.bounded(id pg_catalog._name)")
	setting, err := e.Settings.Definitions()["search_path"].Parse("hidden,missing,visible,visible,public")
	if err != nil {
		t.Fatal(err)
	}
	readerOp, readerDone := roleOperation(t, e, "reader")
	defer readerDone()
	readerOp.Session.Settings.Mu.Lock()
	readerOp.Session.Settings.Typed["search_path"] = setting
	readerOp.Session.Settings.Mu.Unlock()
	scope, err := e.StartStatement(readerOp)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := scope.Finish(nil); err != nil {
			t.Error(err)
		}
	}()
	if err = scope.Gate(statement(t, "SELECT current_schemas(false)")); err != nil {
		t.Fatal(err)
	}
	query := domainUnitBindSelect(t, e, readerOp, "SELECT current_schemas(false)")
	if len(query.Root.Schema()) != 1 {
		t.Fatal(query.Root.Schema())
	}
	c := query.Root.Schema()[0]
	if c.Usage.Type.ID() != sqltype.NameArray || c.Usage.Type.Name() != "_name" || c.Usage.Type.DatabaseName() != "_NAME" || c.Usage.Typmod != -1 || c.Usage.Collation.ID != 950 || c.OriginTable != 0 || c.OriginAttribute != 0 || table.Columns[0].Usage != c.Usage {
		t.Fatal(c, table.Columns)
	}
	value, err := query.Root.(*plan.ProjectNode).Expressions[0].Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: HostOf(scope)}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := sqltype.PlanCoercion(c.Usage, table.Columns[0].Usage, sqltype.Assignment)
	if err != nil {
		t.Fatal(err)
	}
	value, err = plan.Apply(context.Background(), value, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	if err != nil || value.Type() != table.Columns[0].Usage.Type {
		t.Fatal(value, err)
	}
	text, err := value.Type().Output(context.Background(), value)
	if err != nil || text != "{visible,public}" {
		t.Fatal(text, err)
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(readerOp)
	if err != nil {
		t.Fatal(err)
	}
	path, _, err := ExplicitPath(auth)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range path {
		names = append(names, auth.Catalog.Get(a).Name())
	}
	if !reflect.DeepEqual(names, []string{"visible", "public"}) {
		t.Fatal(names)
	}
	target, err := e.CreationNamespace(auth, parser.ObjectName{Parts: []parser.Identifier{{Text: "no_fallback"}}})
	if err != nil || target.Address != path[0] {
		t.Fatal("creation fell back from first usable namespace", target, err)
	}
	domainUnitDiagnostic(t, auth.CheckCreate(target), "42501", "permission denied for schema visible")
	_, err = e.lookupNamespaces(auth, parser.ObjectName{Parts: []parser.Identifier{{Text: "hidden"}, {Text: "int4"}}}, analyze.TypeLookup)
	state(t, err, "42501")
}

func TestSearchPathMissingQualifiedNamespace(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	name := parser.ObjectName{Parts: []parser.Identifier{{Text: "missing_path_ns", Pos: parser.Position{Character: 8}}, {Text: "int4"}}}
	for _, kind := range []analyze.LookupKind{analyze.TypeLookup, analyze.FunctionLookup, analyze.OperatorLookup} {
		_, err := e.lookupNamespaces(auth, name, kind)
		state(t, err, "3F000")
	}
	path, err := e.lookupNamespaces(auth, name, analyze.RelationLookup)
	if err != nil || path.Count() != 0 {
		t.Fatal(path, err)
	}
}

// Scalar unit binders use real catalog/session authorization like public SQL.
func newTestExpressionBinder(t *testing.T, ctx context.Context, e *Engine, input sqltype.InputEnvironment, expected *StatementDescription) *analyze.Binder {
	t.Helper()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	return NewExpressionBinder(ctx, e, input, op, expected)
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
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	other, err := e.AuthorizationLocked(peer)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	temp := auth.Catalog.Get(temporaryNamespace(auth)).Name()
	foreign := other.Catalog.Get(temporaryNamespace(other)).Name()
	e.Mu.Unlock()
	roleExecute(t, e, op, "SET search_path TO b,pg_catalog,pg_temp,"+temp+","+foreign+",a")
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err = e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		kind analyze.LookupKind
		want []string
	}{
		{analyze.RelationLookup, []string{"b", "pg_catalog", temp, "a"}},
		{analyze.TypeLookup, []string{"b", "pg_catalog", temp, "a"}},
		{analyze.FunctionLookup, []string{"b", "pg_catalog", "a"}},
		{analyze.OperatorLookup, []string{"b", "pg_catalog", "a"}},
	} {
		path, err := LookupPath(auth, tc.kind)
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, len(path))
		for i, a := range path {
			got[i] = auth.Catalog.Get(a).Name()
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatal(tc.kind, got, tc.want)
		}
	}
}
