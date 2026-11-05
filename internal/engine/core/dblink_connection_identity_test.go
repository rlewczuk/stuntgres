package core_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestDblinkConnectionIdentity(t *testing.T) {
	e := engine.New()
	installed := dblinkUnitInstall(t, e)
	view := e.CatalogRead()
	if len(installed.Members) != 13 || len(installed.Installation.Declarations) != 13 {
		t.Fatal("selected manifest is not thirteen authentic origins")
	}
	for offset, tc := range []struct {
		name, symbol, signature string
		result                  uint32
		strict, definer         bool
	}{
		{"dblink_connect", "dblink_connect", "25", 25, true, false},
		{"dblink_connect", "dblink_connect", "25,25", 25, true, false},
		{"dblink_connect_u", "dblink_connect", "25", 25, true, true},
		{"dblink_connect_u", "dblink_connect", "25,25", 25, true, true},
		{"dblink_disconnect", "dblink_disconnect", "", 25, true, false},
		{"dblink_disconnect", "dblink_disconnect", "25", 25, true, false},
		{"dblink_get_connections", "dblink_get_connections", "", 1009, false, false},
	} {
		p := view.MustProcedure(installed.Members[6+offset])
		symbol, err := extension.ProcedureSymbol(view, p)
		name, nameErr := extension.ProcedureInstallationName(view, p)
		binding, bindingErr := extension.ProcedureBindingName(view, p)
		if err != nil || nameErr != nil || bindingErr != nil || symbol != tc.symbol || name != tc.name || binding != tc.name || p.Origin.Declaration != 6+offset || string(p.Signature) != tc.signature || uint32(p.Result) != tc.result || p.Strict != tc.strict || p.SecurityDefiner != tc.definer || p.Volatility != expr.Volatile || p.Installed.Parallel != 'r' {
			t.Fatal("immutable SQL identity/shared symbol", offset, symbol, name, binding, err, nameErr, bindingErr)
		}
		if err := (stored.ProcedureProvider{}).Validate(view, p); err != nil {
			t.Fatal("shared provider rejected authentic connection declaration", offset, err)
		}
		state := catalog.CloneState(view.State())
		configured := *p
		configured.SecurityDefiner = !p.SecurityDefiner
		configured.Configuration = catalog.NewRoutineConfiguration([]string{"application_name=connection-environment"})
		configured.Parallel, configured.Cost = 's', 19
		state.SetDescriptor(configured.Address(), &configured)
		if err := (stored.ProcedureProvider{}).Validate(e.Catalog.Read(state), &configured); err != nil {
			t.Fatal("shared provider rejected current connection environment", offset, err)
		}
		if exec.SameRoutineDeclaration(&configured, p) {
			t.Fatal("exact declaration equality relaxed for current attributes")
		}
		if current, err := exec.ValidateStoredRoutineDescriptor(e.Catalog.Read(state), p); err != nil || current != &configured {
			t.Fatal("authentic original carrier did not follow current environment", offset, err)
		}
		if p.SecurityDefiner != tc.definer || p.Configuration != nil || p.Parallel != 0 || p.Cost != 0 {
			t.Fatal("current admission mutated original capture", offset)
		}
		for _, mutate := range []func(*catalog.Procedure){
			func(p *catalog.Procedure) { p.Display.Source = p.Name() + "_spoof" },
			func(p *catalog.Procedure) { p.Origin = nil },
			func(p *catalog.Procedure) { origin := *p.Origin; origin.Declaration = 0; p.Origin = &origin },
			func(p *catalog.Procedure) { p.Result = 2249 },
			func(p *catalog.Procedure) { p.Strict = !p.Strict },
			func(p *catalog.Procedure) { p.Volatility = expr.Immutable },
			func(p *catalog.Procedure) { p.Installed.Parallel = 's' },
		} {
			state := catalog.CloneState(view.State())
			bad := *p
			mutate(&bad)
			state.SetDescriptor(bad.Address(), &bad)
			if extension.StockDblink.ValidateProcedure(e.Catalog.Read(state), &bad) == nil {
				t.Fatal("forged installed connection declaration accepted", offset)
			}
			if (stored.ProcedureProvider{}).Validate(e.Catalog.Read(state), &bad) == nil {
				t.Fatal("shared provider accepted forged connection declaration", offset)
			}
			if _, err := exec.ValidateStoredRoutineDescriptor(view, &bad); err == nil {
				t.Fatal("current-environment fallback accepted forged original capture", offset)
			}
		}
	}
	for _, slot := range []int{0, 1, 2, 5} {
		state := catalog.CloneState(view.State())
		unsupported := *view.MustProcedure(installed.Members[slot])
		unsupported.SecurityDefiner = true
		unsupported.Configuration = catalog.NewRoutineConfiguration([]string{"application_name=not-a-connection-body"})
		state.SetDescriptor(unsupported.Address(), &unsupported)
		if extension.StockDblink.SupportsConnectionRoutine(&unsupported) || (stored.ProcedureProvider{}).Validate(e.Catalog.Read(state), &unsupported) == nil {
			t.Fatal("non-connection dblink body acquired routine environment", slot)
		}
	}
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	node, err := b.BindFunctionProvider(enginetest.FunctionCall("dblink_connect_u", enginetest.StringLiteral("named"), enginetest.StringLiteral("dbname=not_executed password=not_executed")))
	if err != nil {
		t.Fatal(err)
	}
	n, ok := node.(plan.InstalledDblinkConnectionExpr)
	if !ok || n.Operation != functions.DblinkConnectU || n.Count != 2 || n.Ref.Address != installed.Members[9] || n.CallRoutineReference() != n.Procedure {
		t.Fatalf("authentic connection recipe lost: %T", node)
	}
	if err := stored.ValidateInstalledDblinkConnection(n, view); err != nil {
		t.Fatal(err)
	}
	children := n.Children()
	children[0] = children[1]
	if reflect.DeepEqual(n.Args[0], n.Args[1]) {
		t.Fatal("Children exposed retained operand slots")
	}
	rebuilt, err := n.Rebuild(n.Info, n.Children())
	if err != nil {
		t.Fatal(err)
	}
	copy := rebuilt.(plan.InstalledDblinkConnectionExpr)
	if copy.Procedure != n.Procedure || copy.Ref != n.Ref || copy.Info != n.Info || !reflect.DeepEqual(copy.Args, n.Args) {
		t.Fatal("rebuild changed captured identity")
	}
	for _, mutate := range []func(*plan.InstalledDblinkConnectionExpr){
		func(n *plan.InstalledDblinkConnectionExpr) { n.Count = 3 },
		func(n *plan.InstalledDblinkConnectionExpr) { n.Operation = functions.DblinkConnect },
		func(n *plan.InstalledDblinkConnectionExpr) { n.Ref.Epoch++ },
		func(n *plan.InstalledDblinkConnectionExpr) { n.RoutineReference = nil },
		func(n *plan.InstalledDblinkConnectionExpr) { n.Args[0] = nil },
	} {
		bad := n
		mutate(&bad)
		if stored.ValidateInstalledDblinkConnection(bad, view) == nil {
			t.Fatal("forged connection carrier accepted")
		}
	}
}
