package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestInsertUsernameScalarRecipe(t *testing.T) {
	e, ext := ownershipUnitInstall(t, "insert_username")
	view := e.CatalogRead()
	provider := view.MustProcedure(ext.Members[0])
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := enginetest.Statement(t, "SELECT insert_username()").(*parser.Select).Targets[0]
	bound, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil { t.Fatal(err) }
	n, ok := bound.(plan.InsertUsernameExpr)
	if !ok || n.RoutineReference != provider || n.Ref.Address != provider.Address() || n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightKeep || len(n.Children()) != 0 { t.Fatal("scalar recipe lost authentic invocation provenance", bound) }
	if err := stored.ValidateInsertUsername(n, view); err != nil { t.Fatal(err) }
	found := false
	for _, dependency := range b.Dependencies { if dependency.Ref == n.Ref && dependency.Revision == provider.Revision() { found = true } }
	if !found { t.Fatal("selected provider absent from immutable dependency manifest") }
	for _, rebuild := range []func(expr.Info, []expr.Expr) (expr.Expr, error){n.Rebuild, n.Substitute} {
		mapped, err := rebuild(n.Info, nil)
		if err != nil { t.Fatal(err) }
		got := mapped.(plan.InsertUsernameExpr)
		if got.RoutineReference != provider || got.Ref != n.Ref || got.Occurrence != n.Occurrence || got.Source != n.Source { t.Fatal("rebuild replaced original carrier/occurrence") }
	}
	if _, err := n.Rebuild(n.Info, []expr.Expr{n}); err == nil { t.Fatal("unexpected child accepted") }
	capture, admitted, err := n.Capture(expr.CaptureFunction)
	if err != nil || !admitted || capture.Count != 0 { t.Fatal("zero-input function capture", capture, admitted, err) }
	refs := stored.DependRefs{Context: context.Background(), Catalog: view}
	refs.Expression(n)
	if _, ok := refs.Addresses[provider.Address()]; refs.Err != nil || !ok { t.Fatal("reference traversal lost provider", refs.Err) }
	mapped, err := stored.RemapExpression(n, func(ref ids.ObjectRef) ids.ObjectRef { ref.Epoch++; return ref }, nil)
	if err != nil { t.Fatal(err) }
	if got := mapped.(plan.InsertUsernameExpr); got.Ref.Epoch != n.Ref.Epoch+1 || got.RoutineReference != provider { t.Fatal("reference remap lost original declaration") }
	retagged, _, err := stored.RetagTypes(view, n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
	if err != nil || retagged.(plan.InsertUsernameExpr).RoutineReference != provider { t.Fatal("identity retag", err) }
	for _, mutate := range []func(*plan.InsertUsernameExpr){
		func(v *plan.InsertUsernameExpr) { v.Ref.Address.ObjectID++ },
		func(v *plan.InsertUsernameExpr) { v.RoutineReference = nil },
		func(v *plan.InsertUsernameExpr) { p := *provider; p.Origin = nil; v.RoutineReference = &p },
		func(v *plan.InsertUsernameExpr) { p := *provider; p.Display.Source = "forged"; v.RoutineReference = &p },
	} {
		bad := n; mutate(&bad)
		if err := stored.ValidateInsertUsername(bad, view); err == nil { t.Fatal("forged scalar authority admitted") }
	}
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "ALTER FUNCTION insert_username() RENAME TO iu_scalar_real")
	current := e.CatalogRead()
	if err := stored.ValidateInsertUsername(n, current); err != nil { t.Fatal("retained declaration lost genuine renamed identity", err) }
	// A TRIGGER result is not a storable column default; exercise the checked
	// provider renderer without weakening the default renderer's type gate.
	if _, err := (stored.ScalarRenderer{}).SQL(context.Background(), current, n); err == nil {
		t.Fatal("trigger-result call accepted as a storable default")
	}
	text, err := (stored.ScalarRenderer{}).ScalarFunctionSQL(context.Background(), current, n)
	if err != nil || text != `"public"."iu_scalar_real"()` { t.Fatal("scalar replay failed to use current provider name", text, err) }
	if got, err := exec.ExpressionRoutineDescriptor(n); err != nil || got != provider { t.Fatal("presentation replaced captured original", err) }
	if n.RoutineReference != provider || n.Ref.Address != ext.Members[0] { t.Fatal("mapping mutated source recipe") }
}
