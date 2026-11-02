package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func pkeyUnitExpression(t *testing.T, e *core.Engine) plan.InstalledDblinkGetPkeyExpr {
	t.Helper()
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	b.Clause.AggregateClause = "SELECT"
	node, err := b.BindFunctionProvider(enginetest.FunctionCall(functions.DblinkGetPkeyName, enginetest.StringLiteral("absent_relation")))
	if err != nil {
		t.Fatal(err)
	}
	n, ok := node.(plan.InstalledDblinkGetPkeyExpr)
	if !ok {
		t.Fatalf("not an installed nominal source: %T", node)
	}
	return n
}

func TestDblinkGetPkeyIdentity(t *testing.T) {
	e := engine.New()
	ext := dblinkUnitInstall(t, e)
	view := e.CatalogRead()
	if len(ext.Members) != 6 {
		t.Fatal("missing appended recipe", ext.Members)
	}
	for i, name := range []string{functions.DblinkDeleteName, functions.DblinkInsertName, functions.DblinkUpdateName} {
		p := view.MustProcedure(ext.Members[i])
		symbol, err := extension.ProcedureSymbol(view, p)
		if err != nil || symbol != name || p.Origin.Declaration != i || p.Result != 25 || p.Installed.ReturnsSet {
			t.Fatal("incumbent constructor changed", i, symbol, err)
		}
	}
	typ, array, proc := view.MustType(ext.Members[3]), view.MustType(ext.Members[4]), view.MustProcedure(ext.Members[5])
	backing := view.MustRelation(typ.OwningRelation)
	if proc.Result != typ.Address().ObjectID || !proc.Strict || proc.Volatility != expr.Volatile || !proc.Installed.ReturnsSet || proc.Installed.Rows != 1000 || proc.Installed.Parallel != 'r' || proc.Installed.ArgumentNames[0] != "" || typ.Array != array.SQLType.ID() || array.SQLType.Element() != typ.SQLType || backing.RowType != typ.SQLType.ID() || backing.RelationKind != catalog.CompositeRelation || array.OwningRelation != backing.Address() {
		t.Fatal("nominal declaration relationships lost")
	}
	roles, err := extension.InstalledTypes(view, ext)
	if err != nil || len(roles) != 1 || roles[0].Type != typ || roles[0].Name != "dblink_pkey_results" {
		t.Fatal("missing symbolic nominal type role", roles, err)
	}
	for _, pair := range []struct {
		typ  *catalog.Type
		role string
	}{{typ, "dblink_pkey_results"}, {array, "dblink_pkey_results[]"}} {
		role, err := extension.MemberTypeRole(view, ext, pair.typ)
		if err != nil || role != pair.role {
			t.Fatal("wrong replay type role", role, err)
		}
	}
	for _, mutate := range []func(*catalog.Procedure){
		func(p *catalog.Procedure) { p.Result = 2249 },
		func(p *catalog.Procedure) { p.Result = array.Address().ObjectID },
		func(p *catalog.Procedure) { p.Implementation = new(extension.DblinkProvider) },
		func(p *catalog.Procedure) { p.Strict = false },
		func(p *catalog.Procedure) { p.Installed.ReturnsSet = false },
		func(p *catalog.Procedure) { p.Origin = nil },
		func(p *catalog.Procedure) { origin := *p.Origin; origin.Declaration = 2; p.Origin = &origin },
	} {
		state := catalog.CloneState(view.State())
		bad := *proc
		mutate(&bad)
		state.SetDescriptor(bad.Address(), &bad)
		if extension.StockDblink.ValidateProcedure(e.Catalog.Read(state), &bad) == nil {
			t.Fatal("forged routine accepted", bad)
		}
	}
	for _, mutate := range []func(*catalog.Type){
		func(t *catalog.Type) { t.Array = 0 },
		func(t *catalog.Type) { t.Origin = nil },
		func(t *catalog.Type) { t.OwningRelation = ids.ObjectAddress{} },
		func(t *catalog.Type) { t.SQLType = array.SQLType },
	} {
		state := catalog.CloneState(view.State())
		bad := *typ
		mutate(&bad)
		state.SetDescriptor(bad.Address(), &bad)
		if extension.StockDblink.ValidateExtension(e.Catalog.Read(state), ext) == nil {
			t.Fatal("forged nominal type accepted", bad)
		}
	}
	for _, mutate := range []func(*catalog.Type){
		func(t *catalog.Type) { t.SQLType = typ.SQLType },
		func(t *catalog.Type) { t.Origin = nil },
		func(t *catalog.Type) { t.OwningRelation = ids.ObjectAddress{} },
		func(t *catalog.Type) { t.Owner = authorization.PrincipalRef{} },
	} {
		state := catalog.CloneState(view.State())
		bad := *array
		mutate(&bad)
		state.SetDescriptor(bad.Address(), &bad)
		if extension.StockDblink.ValidateExtension(e.Catalog.Read(state), ext) == nil {
			t.Fatal("forged nominal array accepted", bad)
		}
	}
	state := catalog.CloneState(view.State())
	badBacking := *backing
	badBacking.Columns = append([]catalog.Column(nil), backing.Columns...)
	badBacking.Columns[0].Nullable = false
	state.SetDescriptor(backing.Address(), &badBacking)
	if extension.StockDblink.ValidateExtension(e.Catalog.Read(state), ext) == nil {
		t.Fatal("forged result declaration nullability accepted")
	}
	n := pkeyUnitExpression(t, e)
	if err := extension.ValidateDblinkGetPkeyCall(view, proc, n.DblinkGetPkey); err != nil {
		t.Fatal(err)
	}
	lookalike, err := sqltype.Composite(typ.SQLType.ID(), typ.Name(), typ.SQLType.SQLName(), array.SQLType.ID(), []sqltype.CompositeMember{typ.SQLType.CompositeMember(0), typ.SQLType.CompositeMember(1)})
	if err != nil {
		t.Fatal(err)
	}
	badCall := n.DblinkGetPkey
	badCall.Usage = lookalike.Usage()
	if extension.ValidateDblinkGetPkeyCall(view, proc, badCall) == nil {
		t.Fatal("structurally equal but foreign nominal codec accepted")
	}
	for _, ref := range []ids.ObjectRef{e.Catalog.Domain.Ref(view.Epoch()+1, proc.Address()), engine.New().Catalog.Domain.Ref(view.Epoch(), proc.Address())} {
		if extension.ValidateDblinkIdentity(view, proc, ref) == nil {
			t.Fatal("stale or foreign routine reference accepted")
		}
	}
	for _, setup := range []string{
		"CREATE TYPE dblink_pkey_results AS (position integer,colname text)",
		"CREATE FUNCTION dblink_get_pkey(text) RETURNS text LANGUAGE SQL AS 'SELECT NULL::text'",
	} {
		other := engine.New()
		op, done := enginetest.RoleOperation(t, other, "postgres")
		enginetest.RoleExecute(t, other, op, setup)
		before := other.Catalog.State
		err := enginetest.ExtensionUnitExecute(t, other, op, enginetest.Statement(t, "CREATE EXTENSION dblink"))
		done()
		if err == nil || other.Catalog.State != before || other.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "dblink"}) != (ids.ObjectAddress{}) {
			t.Fatal("collision published partial installation", err)
		}
	}
}
