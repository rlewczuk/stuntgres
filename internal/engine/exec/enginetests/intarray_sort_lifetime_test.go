package enginetests_test

import (
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestIntarraySortNullLifetime(t *testing.T) {
	t.Run("temporary_epoch_bridge", intarraySortNullEpochBridge)
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION intarray")
	enginetest.RoleExecute(t, e, op, "CREATE ROLE ia_sort_denied LOGIN")
	for _, signature := range []string{"sort(integer[])", "sort(integer[],text)", "sort_asc(integer[])", "sort_desc(integer[])", "uniq(integer[])"} {
		enginetest.RoleExecute(t, e, op, "REVOKE EXECUTE ON FUNCTION "+signature+" FROM PUBLIC")
	}
	deniedOp, deniedDone := enginetest.RoleOperation(t, e, "ia_sort_denied")
	defer deniedDone()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	view := e.CatalogRead()
	installation := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "intarray"})).Installation
	for operation := sqltype.IntarraySort; operation < sqltype.IntarraySortOperationCount; operation++ {
		proc := view.MustProcedure(installation.Members[extension.IntarraySortProcedureStart+int(operation)])
		args := make([]parser.Expr, operation.Arity())
		for i := range args {
			typ := parser.TypeName{Name: "integer"}
			if i == 0 {
				typ.ArrayBounds = []int32{-1}
			} else {
				typ.Name = "text"
			}
			args[i] = parser.CastExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}, TypeName: typ, Syntax: parser.PostfixCast}
		}
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			e.Mu.Unlock()
			t.Fatal(err)
		}
		denied, err := e.AuthorizationLocked(deniedOp)
		if err != nil {
			e.Mu.Unlock()
			t.Fatal(err)
		}
		b := core.NewExpressionBinder(t.Context(), e, sqltype.InputEnvironment{}, op, nil)
		b.Query.Auth, b.Params.NoParameters = &auth, true
		bound, err := b.BindFunctionProvider(enginetest.FunctionCall(proc.Name(), args...))
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		node, ok := bound.(functions.IntarraySort)
		if !ok {
			t.Fatalf("selected node %T", bound)
		}
		for nullAt := range operation.Arity() {
			call := node
			for i := range operation.Arity() {
				value := sqltype.Null(call.Formals[i])
				if i != nullAt {
					if call.Formals[i].IsArray() {
						value, err = call.Formals[i].Input(t.Context(), "{22,NULL}", -1)
					} else {
						value, err = call.Formals[i].Input(t.Context(), "ASC", -1)
					}
					if err != nil {
						t.Fatal(err)
					}
				}
				call.Args[i] = plan.LiteralExpr{Info: expr.Info{Usage: call.Formals[i].Usage()}, Value: value}
			}
			program := exec.RoutineProgram{}
			access := denied.AdmissionAccess()
			host := intarraySliceAdmissionHost{Host: scope.WriteHost(), Access: access, Dependencies: b.Dependencies}
			ctx := exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context(), Catalog: view, Host: host}, EvalAdmission: exec.EvalAdmission{RoutineProgram: &program}}
			for _, lane := range []string{"direct", "checks", "references"} {
				ctx.RoutineChecks, ctx.RoutineReferences, ctx.RoutineRecipe = nil, nil, false
				switch lane {
				case "checks":
					ctx.RoutineChecks, ctx.RoutineRecipe = b.Dependencies, true
				case "references":
					ctx.RoutineReferences = []ids.ObjectRef{call.Ref}
				}
				if err := exec.AdmitRoutineCall(&ctx, call, true, false); err != nil || len(program.Calls) != 0 {
					t.Fatal("valid NULL lifetime consumed a call or checked EXECUTE", operation, nullAt, lane, err)
				}
				value, err := call.Eval(&ctx)
				if err != nil || !value.IsNull() || value.Type() != call.Usage.Type || len(program.Calls) != 0 {
					t.Fatal("valid strict lifetime did not return unconsumed typed NULL", operation, nullAt, lane, err)
				}
			}
			// A valid reference cannot override a present but empty checks owner.
			ctx.RoutineChecks = []catalog.ExpressionDependency{}
			if err := exec.AdmitRoutineCall(&ctx, call, true, false); err == nil {
				t.Fatal("checks precedence lost")
			}
			ctx.RoutineChecks, ctx.RoutineReferences = nil, []ids.ObjectRef{}
			if err := exec.AdmitRoutineCall(&ctx, call, true, false); err == nil {
				t.Fatal("empty refs owner admitted")
			}
			ctx.RoutineReferences, ctx.RoutineRecipe = nil, true
			if err := exec.AdmitRoutineCall(&ctx, call, true, false); err == nil {
				t.Fatal("empty recipe admitted")
			}
			for _, mode := range []string{"missing", "domain", "epoch", "revision"} {
				checks := make([]catalog.ExpressionDependency, 0, len(b.Dependencies))
				for _, dep := range b.Dependencies {
					if dep.Ref.Address == proc.Address() {
						switch mode {
						case "missing":
							continue
						case "domain":
							dep.Ref = new(ids.Domain).Ref(dep.Ref.Epoch, dep.Ref.Address)
						case "epoch":
							dep.Ref.Epoch++
						case "revision":
							dep.Revision++
						}
					}
					checks = append(checks, dep)
				}
				ctx.RoutineChecks = checks
				if err := exec.AdmitRoutineCall(&ctx, call, true, false); err == nil {
					t.Fatal("NULL repaired invalid checks", mode)
				}
			}
			ctx.RoutineChecks, ctx.RoutineRecipe = nil, false
			for _, ref := range []ids.ObjectRef{new(ids.Domain).Ref(call.Ref.Epoch, call.Ref.Address), call.Ref} {
				if ref == call.Ref {
					ref.Epoch++
				}
				ctx.RoutineReferences = []ids.ObjectRef{ref}
				if err := exec.AdmitRoutineCall(&ctx, call, true, false); err == nil {
					t.Fatal("NULL repaired foreign reference")
				}
			}
			ctx.RoutineReferences = nil
			state := catalog.CloneState(view.State())
			changed := *proc
			changed.ObjectRevision = proc.Revision() + 1
			state.SetDescriptor(proc.Address(), &changed)
			current := access
			current.Recapture(e.Catalog.Read(state))
			if _, err := current.Object(call.Ref); err != nil {
				t.Fatal("candidate authorization snapshot is not coherent", err)
			}
			host.Access = current
			ctx.Host = host
			ctx.Catalog = current.Catalog
			if err := exec.AdmitRoutineCall(&ctx, call, true, false); err == nil {
				t.Fatal("direct stale revision admitted")
			}
			ctx.RoutineChecks, ctx.RoutineRecipe = b.Dependencies, true
			if err := exec.AdmitRoutineCall(&ctx, call, true, false); err != nil {
				t.Fatal("checked immutable identity cannot follow presentation revision", err)
			}
			state.RemoveDescriptor(proc.Address())
			current.Recapture(e.Catalog.Read(state))
			host.Access = current
			ctx.Host = host
			ctx.Catalog = current.Catalog
			if err := exec.AdmitRoutineCall(&ctx, call, true, false); err == nil {
				t.Fatal("NULL rescued retired declaration")
			}
			if len(program.Calls) != 0 {
				t.Fatal("NULL lifetime recorded consumed calls")
			}
		}
		// Ordinary non-NULL execution still checks the genuinely denied role.
		populated := node
		for i := range operation.Arity() {
			value := sqltype.Value{}
			if populated.Formals[i].IsArray() {
				value, err = populated.Formals[i].Input(t.Context(), "{22}", -1)
			} else {
				value, err = populated.Formals[i].Input(t.Context(), "ASC", -1)
			}
			if err != nil {
				t.Fatal(err)
			}
			populated.Args[i] = plan.LiteralExpr{Info: expr.Info{Usage: populated.Formals[i].Usage()}, Value: value}
		}
		deniedContext := exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context(), Catalog: view}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &denied, RoutineRecipe: true, RoutineChecks: b.Dependencies}}
		if err := exec.AdmitRoutineCall(&deniedContext, populated, true, false); err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Fatal("nonnull EXECUTE denial changed", err)
		}
	}
}

func intarraySortNullEpochBridge(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	// The extension is genuinely installed into the session-owned namespace;
	// ordinary ALTER FUNCTION correctly forbids crossing the TEMP boundary.
	enginetest.RoleExecute(t, e, op, "CREATE TEMP TABLE sort_epoch_consumer(a integer[])")
	view := e.CatalogRead()
	var consumer *catalog.Relation
	for _, relation := range view.Relations() {
		if relation.Name() == "sort_epoch_consumer" {
			consumer = relation
		}
	}
	if consumer == nil {
		t.Fatal("genuine TEMP consumer absent")
	}
	namespace := view.MustNamespace(consumer.Namespace())
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION intarray SCHEMA "+catalog.DependencyIdentifier(namespace.Name()))
	view = e.CatalogRead()
	consumer = view.MustRelation(consumer.Address())
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	installation := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "intarray"})).Installation
	for operation := sqltype.IntarraySort; operation < sqltype.IntarraySortOperationCount; operation++ {
		proc := view.MustProcedure(installation.Members[extension.IntarraySortProcedureStart+int(operation)])
		args := make([]parser.Expr, operation.Arity())
		for i := range args {
			typ := parser.TypeName{Name: "integer"}
			if i == 0 {
				typ.ArrayBounds = []int32{-1}
			} else {
				typ.Name = "text"
			}
			args[i] = parser.CastExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}, TypeName: typ, Syntax: parser.PostfixCast}
		}
		raw := enginetest.FunctionCall(proc.Name(), args...)
		raw.Name.Parts = []parser.Identifier{{Text: namespace.Name()}, {Text: proc.Name()}}
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			e.Mu.Unlock()
			t.Fatal(err)
		}
		b := core.NewExpressionBinder(t.Context(), e, sqltype.InputEnvironment{}, op, nil)
		b.Query.Auth, b.Params.NoParameters = &auth, true
		bound, err := b.BindFunctionProvider(raw)
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		node := bound.(functions.IntarraySort)
		state := catalog.CloneState(view.State())
		state.SetEpoch(view.Epoch() + 1)
		access := auth.AdmissionAccess()
		access.Recapture(e.Catalog.Read(state))
		if _, err := access.Object(access.Catalog.Owner().Domain.Ref(access.Catalog.Epoch(), node.Ref.Address)); err != nil {
			t.Fatal("epoch candidate authorization snapshot is not coherent", err)
		}
		host := intarraySliceAdmissionHost{Host: scope.WriteHost(), Access: access, Dependencies: b.Dependencies}
		program := exec.RoutineProgram{}
		ctx := exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context(), Catalog: access.Catalog, Host: host}, EvalAdmission: exec.EvalAdmission{RoutineConsumer: consumer, RoutineProgram: &program, RoutineRecipe: true, RoutineChecks: b.Dependencies}}
		if err := exec.AdmitRoutineCall(&ctx, node, true, false); err != nil {
			t.Fatal("checked TEMP lifetime lost valid epoch bridge", operation, err)
		}
		ctx.RoutineChecks, ctx.RoutineReferences = nil, []ids.ObjectRef{node.Ref}
		if err := exec.AdmitRoutineCall(&ctx, node, true, false); err != nil {
			t.Fatal("reference TEMP lifetime lost valid epoch bridge", operation, err)
		}
		ctx.RoutineReferences, ctx.RoutineChecks = nil, b.Dependencies
		copyConsumer := *consumer
		ctx.RoutineConsumer = &copyConsumer
		if err := exec.AdmitRoutineCall(&ctx, node, true, false); err == nil {
			t.Fatal("copied consumer granted epoch bridge")
		}
		ctx.RoutineConsumer, ctx.RoutineChecks, ctx.RoutineRecipe = nil, nil, false
		if err := exec.AdmitRoutineCall(&ctx, node, true, false); err == nil {
			t.Fatal("direct source inherited stored epoch bridge")
		}
		if len(program.Calls) != 0 {
			t.Fatal("epoch-only NULL validation consumed a call")
		}
	}
}
