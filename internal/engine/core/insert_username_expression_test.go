package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
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

func TestInsertUsernameScalarEvaluationAuthority(t *testing.T) {
	e, installed := ownershipUnitInstall(t, "insert_username")
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION autoinc")
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if err != nil { t.Fatal(err) }
	view := auth.Catalog
	provider := view.MustProcedure(installed.Members[0])
	trigger, _ := view.LookupType(2279)
	n := plan.InsertUsernameExpr{
		Info: expr.Info{Usage: trigger.Usage(), Property: expr.Properties{Volatility: expr.Volatile}, RoutineReference: provider},
		Ref: view.Owner().Domain.Ref(view.Epoch(), provider.Address()),
		Namespace: "public",
	}
	evaluation := exec.Context{
		EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: view},
		EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth},
	}
	_, err = n.Eval(&evaluation)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "XX000" || diagnostic.Message != "insert_username: not fired by trigger manager" {
		t.Fatal("genuine scalar invocation lost its protocol diagnostic", err)
	}
	autoinc := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "autoinc"}))
	different := view.MustProcedure(autoinc.Members[0])
	foreign, foreignInstalled := ownershipUnitInstall(t, "insert_username")
	foreignView := foreign.CatalogRead()
	foreignProvider := foreignView.MustProcedure(foreignInstalled.Members[0])
	for _, tc := range []struct {
		name string
		mutate func(*plan.InsertUsernameExpr)
		stale bool
	}{
		{"different_native_provider", func(v *plan.InsertUsernameExpr) {
			v.RoutineReference = different
			v.Ref = view.Owner().Domain.Ref(view.Epoch(), different.Address())
		}, false},
		{"zero_reference", func(v *plan.InsertUsernameExpr) { v.Ref = ids.ObjectRef{} }, false},
		{"foreign_reference", func(v *plan.InsertUsernameExpr) {
			v.Ref = foreignView.Owner().Domain.Ref(view.Epoch(), provider.Address())
		}, false},
		{"foreign_provider", func(v *plan.InsertUsernameExpr) {
			v.RoutineReference = foreignProvider
			v.Ref = foreignView.Owner().Domain.Ref(foreignView.Epoch(), foreignProvider.Address())
		}, false},
		{"stale_epoch", func(v *plan.InsertUsernameExpr) { v.Ref.Epoch = 0 }, true},
		{"future_epoch", func(v *plan.InsertUsernameExpr) { v.Ref.Epoch++ }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := n
			tc.mutate(&bad)
			if tc.name == "different_native_provider" {
				if err := bad.ValidateStoredProvider(); err != nil { t.Fatal("test did not reach concrete authentication", err) }
			}
			_, err := bad.Eval(&evaluation)
			diagnostic = nil
			if err == nil || errors.As(err, &diagnostic) && diagnostic.Code == "XX000" && diagnostic.Message == "insert_username: not fired by trigger manager" {
				t.Fatal("forged execution authority reached helper body", err)
			}
			if tc.stale && !errors.Is(err, ids.ErrCatalogReplaced) { t.Fatal("epoch mismatch did not reject catalog authority", err) }
		})
	}
	if n.RoutineReference != provider || n.Ref != view.Owner().Domain.Ref(view.Epoch(), provider.Address()) {
		t.Fatal("execution mutated retained recipe authority")
	}
}

func TestInsertUsernameScalarEpochBridge(t *testing.T) {
	e := engine.New()
	owner := sessionStart(t, e, nil)
	sessionSQL(t, owner, "CREATE TEMP TABLE iu_bridge_consumer(id integer)")
	namespace := e.CatalogRead().MustNamespace(owner.TemporaryNamespace)
	sessionSQL(t, owner, "CREATE EXTENSION insert_username SCHEMA "+catalog.QuoteIdentifier(namespace.Name()))
	before := e.CatalogRead()
	installed := before.MustExtension(before.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "insert_username"}))
	provider := before.MustProcedure(installed.Members[0])
	consumer := before.MustRelation(before.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace.Address(), Name: "iu_bridge_consumer"}))
	trigger, _ := before.LookupType(2279)
	n := plan.InsertUsernameExpr{
		Info: expr.Info{Usage: trigger.Usage(), Property: expr.Properties{Volatility: expr.Volatile}, RoutineReference: provider},
		Ref: before.Owner().Domain.Ref(before.Epoch(), provider.Address()),
		Namespace: namespace.Name(),
	}
	// Advance an isolated catalog generation while retaining the authentic
	// temporary namespace, provider and consumer, as in retained TEMP units.
	e.Mu.Lock()
	advanced := catalog.CloneState(before.State())
	advanced.SetEpoch(before.Epoch() + 1)
	e.Catalog.PublishLocked(advanced)
	e.Mu.Unlock()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if err != nil { t.Fatal(err) }
	current := auth.Catalog
	if current.Epoch() <= n.Ref.Epoch || current.Get(consumer.Address()) != consumer || current.Get(provider.Address()) != provider {
		t.Fatal("fixture did not retain genuine objects across catalog generations")
	}
	if !stored.ReferenceEpochValid(current, consumer, n.Ref) || stored.ReferenceEpochValid(current, nil, n.Ref) {
		t.Fatal("fixture lacks independently admissible contextual epoch authority")
	}
	evaluation := exec.Context{
		EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: current},
		EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineConsumer: consumer},
	}
	original := n
	_, err = n.Eval(&evaluation)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "XX000" || diagnostic.Message != "insert_username: not fired by trigger manager" {
		t.Fatal("lawful stored-consumer bridge did not reach authentic helper", err)
	}
	withoutConsumer := evaluation
	withoutConsumer.RoutineConsumer = nil
	if _, err := n.Eval(&withoutConsumer); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("same older reference gained authority without retained consumer", err)
	}
	if n.Ref != original.Ref || n.RoutineReference != original.RoutineReference || n.Namespace != original.Namespace || n.ResultUsage() != original.ResultUsage() || n.Ref.Epoch != before.Epoch() {
		t.Fatal("contextual epoch admission mutated source recipe")
	}
}
