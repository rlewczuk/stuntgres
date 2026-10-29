package schemacmds_test

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestTcnProviderAuthority(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION tcn")
	v := e.CatalogRead()
	ext := v.MustExtension(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "tcn"}))
	d := v.MustProcedure(ext.Members[0])
	control := extension.StockTcn.Control()
	if control.Version != "1.0" || !control.Trusted || !control.Relocatable || !control.Superuser || control.Comment == nil || *control.Comment != "Triggered change notifications" {
		t.Fatal("control", control)
	}
	if err := extension.StockTcn.ValidateExtension(v, ext); err != nil {
		t.Fatal(err)
	}
	if d.Result != 2279 || d.Signature != "" || d.Strict || d.Volatility != expr.Volatile || d.Installed.Parallel != 'u' || d.Installed.ReturnsSet {
		t.Fatal("declaration", d)
	}
	for _, mutate := range []func(*catalog.Procedure){
		func(p *catalog.Procedure) { p.Strict = true },
		func(p *catalog.Procedure) { p.Signature = "23" },
		func(p *catalog.Procedure) { p.Result = 25 },
		func(p *catalog.Procedure) { p.Volatility = expr.Immutable },
		func(p *catalog.Procedure) { p.Implementation = new(extension.TcnProvider) },
		func(p *catalog.Procedure) { p.Origin = nil },
		func(p *catalog.Procedure) { origin := *p.Origin; origin.Declaration = 1; p.Origin = &origin },
		func(p *catalog.Procedure) {
			origin := *p.Origin
			installation := *origin.Installation
			installation.Version = "2.0"
			origin.Installation = &installation
			p.Origin = &origin
		},
		func(p *catalog.Procedure) { p.Display.Source = "decoy" },
		func(p *catalog.Procedure) { p.Body = []catalog.RoutineBodyEdge{{}} },
	} {
		bad := *d
		mutate(&bad)
		state := catalog.CloneState(v.State())
		state.SetDescriptor(d.Address(), &bad)
		if err := (stored.ProcedureProvider{}).Validate(e.Catalog.Read(state), &bad); err == nil {
			t.Fatal("forged provider accepted")
		}
	}
	binder := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	node, err := binder.BindFunctionProvider(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "triggered_change_notification"}}}})
	if err != nil {
		t.Fatal(err)
	}
	call, ok := node.(functions.Tcn)
	if !ok || call.RoutineReference != d || len(binder.Dependencies) != 2 {
		t.Fatal("installed carrier", node, binder.Dependencies)
	}
	procedureDependencies, typeDependencies := 0, 0
	for _, dependency := range binder.Dependencies {
		switch dependency.Ref.Address {
		case d.Address():
			procedureDependencies++
		case (ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 2279}):
			typeDependencies++
		default:
			t.Fatal("unexpected installed dependency", dependency)
		}
		descriptor := v.Get(dependency.Ref.Address)
		if descriptor == nil || dependency.Revision != descriptor.Revision() || dependency.Ref != v.Owner().Domain.Ref(v.Epoch(), descriptor.Address()) {
			t.Fatal("invalid installed dependency authority", dependency)
		}
	}
	if procedureDependencies != 1 || typeDependencies != 1 {
		t.Fatal("missing installed procedure/result dependency", binder.Dependencies)
	}
	rebuilt, err := call.Rebuild(call.Info, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.(functions.Tcn).RoutineReference != d {
		t.Fatal("rebuild lost authority")
	}
	if _, err := exec.ExpressionRoutineDescriptor(rebuilt); err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, e, op, "ALTER FUNCTION triggered_change_notification() RENAME TO real_tcn")
	text, err := (stored.Deparser{View: e.CatalogRead()}).SQL(context.Background(), call, stored.DeparseParent{})
	if err != nil || !strings.Contains(text, "real_tcn") {
		t.Fatal("current rendering", text, err)
	}
}

func TestTcnTriggerAdmission(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION tcn")
	v := e.CatalogRead()
	ext := v.MustExtension(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "tcn"}))
	d := v.MustProcedure(ext.Members[0])
	if !catalog.SupportsDMLTrigger(d) || !catalog.NativeDMLTrigger(d) || catalog.BuiltinDMLTrigger(d) != 0 {
		t.Fatal("native capability")
	}
	if err := (stored.ProcedureProvider{}).Validate(v, d); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*catalog.Procedure){func(p *catalog.Procedure) { p.Implementation = new(extension.TcnProvider) }, func(p *catalog.Procedure) { p.Result = 25 }, func(p *catalog.Procedure) { p.Strict = true }} {
		bad := *d
		mutate(&bad)
		if catalog.SupportsDMLTrigger(&bad) {
			t.Fatal("forged capability")
		}
	}
}
