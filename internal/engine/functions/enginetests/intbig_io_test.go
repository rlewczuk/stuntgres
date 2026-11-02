package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestIntbigGkeyIOBinding(t *testing.T) {
	for _, slot := range []int{4, 5} {
		t.Run(map[int]string{4: "input", 5: "output"}[slot], func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			enginetest.RoleExecute(t, e, op, "CREATE EXTENSION intarray")
			view := e.CatalogRead()
			ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "intarray"}))
			procedure := view.MustProcedure(ext.Installation.Members[slot])
			arg := parser.Expr(parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}})
			if slot == 5 {
				arg = parser.CastExpr{Child: arg, TypeName: parser.TypeName{Name: "intbig_gkey"}, Syntax: parser.PostfixCast}
			}
			raw := enginetest.FunctionCall(procedure.Name(), arg)
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			b := core.NewExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, op, nil)
			b.Query.Auth, b.Params.NoParameters = &auth, true
			bound, err := b.BindFunctionProvider(raw)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal("genuine selected role", err)
			}
			node, ok := bound.(functions.IntbigIO)
			if !ok || node.Member != uint8(slot) || node.RoutineReference != procedure || node.Datum != view.MustType(ext.Installation.Members[3]).SQLType || node.PlanningFold() != expr.FoldConstant || node.IntrinsicVolatility() != expr.Immutable {
				t.Fatal("selected installed I/O ownership", bound)
			}
			selected, err := exec.ExpressionRoutineDescriptor(node)
			if err != nil || selected != procedure {
				t.Fatal("opaque routine reference changed identity", selected, err)
			}
			found := false
			for _, dependency := range b.Dependencies {
				if dependency.Ref.Address == procedure.Address() {
					found = true
					if dependency.Ref != node.Ref || dependency.Revision != procedure.Revision() {
						t.Fatal("routine capture lost epoch/revision", dependency)
					}
				}
			}
			if !found {
				t.Fatal("installed call did not capture selected declaration")
			}
			frozen, ok := b.NormalizeStoredExpression(raw).(parser.FunctionExpr)
			if !ok || !frozen.StoredProviderRoutine || frozen.StoredProviderSymbol != procedure.Display.Source {
				t.Fatal("stored syntax lost checked original symbol", frozen)
			}
			capture, ok, err := node.Capture(expr.CaptureFunction)
			if err != nil || !ok || capture.Check(node, 1) != nil {
				t.Fatal("ordinary capture protocol", capture, err)
			}
			rebuilt, err := node.Rebuild(node.Info, node.Children())
			if err != nil || !reflect.DeepEqual(rebuilt, node) {
				t.Fatal("immutable rebuild changed retained call", rebuilt, err)
			}
			if _, err := node.Render(); err != nil {
				t.Fatal(err)
			}
			calls := 0
			child := intbigCountingOperand{Info: expr.Info{Usage: node.Formal.Usage()}, Value: sqltype.Null(node.Formal), Calls: &calls}
			counted, err := node.Rebuild(node.Info, []expr.Expr{child})
			if err != nil {
				t.Fatal(err)
			}
			value, err := counted.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context()}})
			if err != nil || calls != 1 || !value.IsNull() || value.Type() != node.Usage.Type {
				t.Fatal("strict NULL did not evaluate exactly one owned child", calls, value, err)
			}
			if slot == 4 {
				calls = 0
				child.Value = sqltype.String(node.Formal, "x")
				counted, err := node.Rebuild(node.Info, []expr.Expr{child})
				if err != nil {
					t.Fatal(err)
				}
				value, err := counted.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context()}})
				var diagnostic *sqlstate.Error
				want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cannot accept a value of type intbig_gkey"}
				if calls != 1 || value.Type() != nil || !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
					t.Fatal("genuine cstring kernel did not reject exactly once", calls, value, diagnostic, err)
				}
			}
			destination, err := sqltype.IntbigGkey(65010, "intbig_gkey", "public.intbig_gkey")
			if err != nil {
				t.Fatal(err)
			}
			info := node.Info
			children := node.Children()
			if slot == 4 {
				info.Usage = destination.Usage()
			} else {
				children[0] = plan.LiteralExpr{Info: expr.Info{Usage: destination.Usage()}, Value: sqltype.Null(destination)}
			}
			retagged, changed, err := node.Retag(info, children, false, expr.Retag{Type: func(old *sqltype.Descriptor) *sqltype.Descriptor {
				if old == node.Datum {
					return destination
				}
				return old
			}})
			if err != nil || !changed || retagged.(functions.IntbigIO).Datum != destination || node.Datum == destination {
				t.Fatal("retag retained source generation or mutated source", retagged, changed, err)
			}
			text, _ := view.LookupType(sqltype.Text)
			wrong := plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.Null(text)}
			if _, err := node.Rebuild(node.Info, []expr.Expr{wrong}); err == nil {
				t.Fatal("foreign formal rebuilt as installed key I/O")
			}
			evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context()}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineRecipe: true, RoutineChecks: b.Dependencies}}
			if err := exec.AdmitRoutineCall(&evaluation, node, true, false); err != nil {
				t.Fatal("genuine captured declaration admission", err)
			}
			for _, mode := range []string{"missing", "foreign", "epoch", "revision"} {
				foreign := engine.New()
				var checks []catalog.ExpressionDependency
				for _, dependency := range b.Dependencies {
					if dependency.Ref.Address == procedure.Address() {
						switch mode {
						case "missing":
							continue
						case "foreign":
							dependency.Ref = foreign.Catalog.Domain.Ref(dependency.Ref.Epoch, dependency.Ref.Address)
						case "epoch":
							dependency.Ref.Epoch++
						case "revision":
							dependency.Revision++
						}
					}
					checks = append(checks, dependency)
				}
				evaluation.RoutineChecks = checks
				if err := exec.AdmitRoutineCall(&evaluation, node, true, false); err == nil {
					t.Fatal("invalid captured declaration admitted", mode)
				}
			}
			for _, checks := range [][]catalog.ExpressionDependency{nil, {}} {
				evaluation.RoutineChecks, evaluation.RoutineReferences = checks, nil
				if err := exec.AdmitRoutineCall(&evaluation, node, true, false); err == nil {
					t.Fatal("empty owning NULL manifest admitted", checks)
				}
			}
			foreign := engine.New()
			future := node.Ref
			future.Epoch++
			for _, refs := range [][]ids.ObjectRef{{}, {foreign.Catalog.Domain.Ref(node.Ref.Epoch, node.Ref.Address)}, {future}} {
				evaluation.RoutineChecks, evaluation.RoutineReferences = nil, refs
				if err := exec.AdmitRoutineCall(&evaluation, node, true, false); err == nil {
					t.Fatal("invalid ref-only NULL manifest admitted", refs)
				}
			}
			evaluation.RoutineReferences = []ids.ObjectRef{node.Ref}
			if err := exec.AdmitRoutineCall(&evaluation, node, true, false); err != nil {
				t.Fatal("valid ref-only NULL declaration rejected", err)
			}
			evaluation.RoutineChecks, evaluation.RoutineReferences = b.Dependencies, nil
			if err := exec.AdmitRoutineCall(&evaluation, node, true, false); err != nil {
				t.Fatal("negative probes changed original declaration authority", err)
			}
			// Genuine relabel and membership changes retain immutable origin,
			// unlike a same-spelled routine or a forged implementation pointer.
			target := "cstring"
			if slot == 5 {
				target = "intbig_gkey"
			}
			enginetest.RoleExecute(t, e, op, "ALTER FUNCTION "+procedure.Name()+"("+target+") RENAME TO ia_io_renamed")
			enginetest.RoleExecute(t, e, op, "ALTER EXTENSION intarray DROP FUNCTION ia_io_renamed("+target+")")
			b = enginetest.ExpressionBinder(t, t.Context(), e, sqltype.InputEnvironment{}, nil)
			renamed, err := b.BindFunctionProvider(enginetest.FunctionCall("ia_io_renamed", arg))
			if err != nil {
				t.Fatal("legitimate detached/relabelled origin", err)
			}
			retained := renamed.(functions.IntbigIO)
			if retained.Member != uint8(slot) || retained.Ref.Address != node.Ref.Address || retained.Name != "ia_io_renamed" || node.Name != procedure.Name() || node.RoutineReference != procedure {
				t.Fatal("rebind mutated old captured definition", retained, node)
			}
		})
	}
}

func TestIntbigGkeyIOSpoofedDeclaration(t *testing.T) {
	for _, mutate := range []func(*catalog.Procedure){
		func(p *catalog.Procedure) { p.Implementation = extension.StockSeg },
		func(p *catalog.Procedure) { p.Strict = false },
		func(p *catalog.Procedure) { p.Result = 25 },
		func(p *catalog.Procedure) { p.Display.Source = "bqarr_in" },
		func(p *catalog.Procedure) { origin := *p.Origin; origin.Declaration = 1; p.Origin = &origin },
	} {
		e := engine.New()
		op, done := enginetest.RoleOperation(t, e, "postgres")
		enginetest.RoleExecute(t, e, op, "CREATE EXTENSION intarray")
		done()
		view := e.CatalogRead()
		ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "intarray"}))
		original := view.MustProcedure(ext.Installation.Members[4])
		copy := *original
		mutate(&copy)
		candidate := catalog.CloneState(e.Catalog.State)
		candidate.SetDescriptor(copy.Address(), &copy)
		e.Catalog.State = candidate
		b := enginetest.ExpressionBinder(t, t.Context(), e, sqltype.InputEnvironment{}, nil)
		if node, err := b.BindFunctionProvider(enginetest.FunctionCall("_intbig_in", parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}})); err == nil || node != nil {
			t.Fatal("forged support declaration acquired execution", node, err)
		}
		if original.Implementation != extension.StockIntarray || original.Display.Source != "_intbig_in" || !original.Strict {
			t.Fatal("negative binding mutated source declaration")
		}
	}
}

type intbigCountingOperand struct {
	expr.Info
	Value sqltype.Value
	Calls *int
}

func (e intbigCountingOperand) Eval(expr.Context) (sqltype.Value, error) {
	(*e.Calls)++
	return e.Value, nil
}

func TestIntbigGkeyIONullLifetimeWithoutExecute(t *testing.T) {
	e := engine.New()
	admin, adminDone := enginetest.RoleOperation(t, e, "postgres")
	enginetest.RoleExecute(t, e, admin, "CREATE EXTENSION intarray")
	enginetest.RoleExecute(t, e, admin, "CREATE ROLE ia_io_reader LOGIN")
	enginetest.RoleExecute(t, e, admin, "REVOKE EXECUTE ON FUNCTION _intbig_in(cstring) FROM PUBLIC")
	enginetest.RoleExecute(t, e, admin, "REVOKE EXECUTE ON FUNCTION _intbig_out(intbig_gkey) FROM PUBLIC")
	adminDone()
	reader, done := enginetest.RoleOperation(t, e, "ia_io_reader")
	defer done()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(reader)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	bind := func(raw parser.FunctionExpr) (expr.Expr, []catalog.ExpressionDependency) {
		t.Helper()
		e.Mu.Lock()
		b := core.NewExpressionBinder(t.Context(), e, sqltype.InputEnvironment{}, reader, nil)
		b.Query.Auth = &auth
		node, err := b.BindFunctionProvider(raw)
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		return node, b.Dependencies
	}
	null := parser.Expr(parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}})
	for _, raw := range []parser.FunctionExpr{
		enginetest.FunctionCall("_intbig_in", null),
		enginetest.FunctionCall("_intbig_out", parser.CastExpr{Child: null, TypeName: parser.TypeName{Name: "intbig_gkey"}, Syntax: parser.PostfixCast}),
	} {
		node, dependencies := bind(raw)
		c := exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context()}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineRecipe: true, RoutineChecks: dependencies}}
		captured, err := exec.ExpressionRoutineDescriptor(node)
		if err != nil {
			t.Fatal(err)
		}
		var diagnostic *sqlstate.Error
		// Prove the role actually lacks EXECUTE before checking that strict
		// NULL validates its lifetime without acquiring invocation rights.
		err = exec.CheckRecipeRoutineDescriptor(&c, auth.AdmissionAccess(), dependencies, captured)
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" {
			t.Fatal("fixture did not revoke genuine support invocation", err)
		}
		if err := exec.AdmitRoutineCall(&c, node, true, false); err != nil {
			t.Fatal("valid NULL lifetime incorrectly demanded EXECUTE", err)
		}
	}
	node, dependencies := bind(enginetest.FunctionCall("_intbig_in", enginetest.StringLiteral("x")))
	c := exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context()}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineRecipe: true, RoutineChecks: dependencies}}
	var diagnostic *sqlstate.Error
	if err := exec.AdmitRoutineCall(&c, node, true, false); !errors.As(err, &diagnostic) || diagnostic.Code != "42501" {
		t.Fatal("nonnull support invocation bypassed ordinary EXECUTE", err)
	}
}

func TestIntbigGkeyIODirectLifetime(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, query := range []string{
		"CREATE EXTENSION intarray",
		"CREATE ROLE ia_io_reader LOGIN",
		"CREATE ROLE ia_io_owner LOGIN",
	} {
		enginetest.RoleExecute(t, e, op, query)
	}
	reader, readerDone := enginetest.RoleOperation(t, e, "ia_io_reader")
	defer readerDone()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	result, err := scope.Execute(enginetest.Statement(t, "SELECT 1"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != nil {
		if err := result.Rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	b := core.NewExpressionBinder(t.Context(), e, scope.Input, op, nil)
	b.Query.Auth = &auth
	node, err := b.BindFunctionProvider(enginetest.FunctionCall("_intbig_in", parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}))
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	captured, err := exec.ExpressionRoutineDescriptor(node)
	if err != nil || captured == nil {
		t.Fatal("missing direct support declaration", err)
	}
	for _, dependency := range b.Dependencies {
		if dependency.Ref.Address == captured.Address() {
			if err := enginetest.SetObjectACL(e, t.Context(), dependency.Ref, authorization.ACLValue{Explicit: true}); err != nil {
				t.Fatal(err)
			}
		}
	}
	currentAuthority := func() core.AuthorizationView {
		t.Helper()
		e.Mu.Lock()
		defer e.Mu.Unlock()
		auth, err := e.AuthorizationLocked(reader)
		if err != nil {
			t.Fatal(err)
		}
		return auth
	}
	scope.RoutineAuth, scope.RoutineDependencies = currentAuthority(), b.Dependencies
	c := exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context(), Host: core.HostOf(scope), Catalog: auth.Catalog, Input: scope.Input, Output: scope.Output}}
	if err := exec.AdmitRoutineCall(&c, node, true, false); err != nil {
		t.Fatal("direct NULL source demanded EXECUTE", err)
	}
	before := e.CatalogRead()
	stage := e.BeginCatalogStage(before)
	defer e.DiscardCatalogStage(stage)
	owner := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("ia_io_owner")
	if err := role.StageOwnership(stage, before.MustProcedure(captured.Address()), owner); err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(t.Context(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	if err := exec.AdmitRoutineCall(&c, node, true, false); err != nil {
		t.Fatal("owner COW changed retained direct authority snapshot", err)
	}
	scope.RoutineAuth = currentAuthority()
	var conflict catalog.Conflict
	if err := exec.AdmitRoutineCall(&c, node, true, false); !errors.As(err, &conflict) {
		t.Fatal("support NULL rescued stale direct revision", err)
	}
	if before.MustProcedure(captured.Address()).Revision() != captured.Revision() || captured.Owner == owner {
		t.Fatal("owner COW mutated original support declaration")
	}
}
