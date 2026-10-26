package core_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

var arrayStrictNullPlanners = []struct {
	name string
	run  func(*exec.Context, expr.Expr, bool) (expr.Expr, bool, error)
}{{"preflight", exec.PreflightCoercionsWithPolicy}, {"conditional", exec.SimplifyConditional}}

func arrayStrictNullFixture(t *testing.T) (*core.Engine, *session.Operation, *core.StatementScope) {
	t.Helper()
	e, op, scope := strictNullUnitFixture(t)
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, e, op, "CREATE ROLE array_strict_reader LOGIN")
	enginetest.RoleExecute(t, e, op, "CREATE ROLE array_strict_owner LOGIN")
	enginetest.RoleExecute(t, e, op, "GRANT USAGE,SELECT ON SEQUENCE strict_null_steps TO array_strict_reader")
	var err error
	scope, err = e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { scope.Finish(nil) })
	arrayStrictNullAdmitScope(t, scope)
	return e, op, scope
}

func arrayStrictNullAdmitScope(t *testing.T, scope *core.StatementScope) {
	t.Helper()
	result, err := scope.Execute(enginetest.Statement(t, "SELECT 1"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != nil {
		if err := result.Rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func arrayStrictNullResult(t *testing.T, original, folded expr.Expr) {
	t.Helper()
	literal, ok := folded.(plan.LiteralExpr)
	if !ok || !literal.Value.IsNull() || literal.Value.Type() != original.ResultUsage().Type {
		t.Fatalf("expected command-private typed NULL, got %#v", folded)
	}
	want := expr.InfoOf(original)
	want.RoutineReference = nil
	if !reflect.DeepEqual(literal.Info, want) {
		t.Fatalf("lost usage/typmod/collation/position/properties/recipe: got %#v, want %#v", literal.Info, want)
	}
	if descriptor, err := exec.ExpressionRoutineDescriptor(folded); err != nil || descriptor != nil {
		t.Fatal("folded literal retained callable", descriptor, err)
	}
	if _, composite := folded.(expr.Composite); composite {
		t.Fatal("folded result retained executable children")
	}
}

func arrayStrictNullAdmitTree(c *exec.Context, node expr.Expr) error {
	// The production walker includes non-Composite coercion/arithmetic
	// wrappers and selects each real stored recipe's admission authority.
	return view.ValidateExpressionJoinExecution(c, node, c.Catalog)
}

func arrayStrictNullSnapshot(t *testing.T, node expr.Expr) expr.Expr {
	t.Helper()
	// Force COW at genuine leaves through the existing structural mapper,
	// which also visits non-Composite plan wrappers. No types, references or
	// providers are substituted, and no catalog authority is manufactured.
	copied, _, err := stored.MapExpression(catalog.Read{}, node,
		func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ },
		nil, nil, func(child expr.Expr) (expr.Expr, bool, error) {
			switch child.(type) {
			case plan.LiteralExpr, plan.ParameterExpr, plan.ColumnExpr:
				return child, true, nil
			}
			return child, false, nil
		})
	if err != nil {
		t.Fatal("copying original provider graph", err)
	}
	return copied
}

func arrayStrictNullUncalled(t *testing.T, e *core.Engine) {
	t.Helper()
	sequence := enginetest.CatalogRef(t, e, e.CatalogRead(), "strict_null_steps")
	e.Mu.Lock()
	called := e.Storage.Sequences[sequence.Address].Called
	e.Mu.Unlock()
	if called {
		t.Fatal("planning or eliminated result executed nextval")
	}
}

func TestArrayRandomStrictNullPlanning(t *testing.T) {
	for _, planner := range arrayStrictNullPlanners {
		t.Run(planner.name, func(t *testing.T) {
			e, op, scope := arrayStrictNullFixture(t)
			reader, done := enginetest.RoleOperation(t, e, "array_strict_reader")
			defer done()
			for _, text := range []string{
				"array_sample(NULL::integer[],nextval('strict_null_steps')::integer)",
				"array_sample($1::integer[],nextval('strict_null_steps')::integer)",
				"array_sample(ARRAY[nextval('strict_null_steps')::integer],NULL::integer)",
				"array_sample(NULL::integer[],abs(-1))",
			} {
				t.Run(text, func(t *testing.T) {
					node, binder, evaluation := strictNullUnitBind(t, e, op, scope, text)
					call, ok := node.(functions.ArrayRandom)
					if !ok || call.Kind != functions.ArraySample || call.Validate() != nil {
						t.Fatal("not a real bound ArrayRandom", node)
					}
					before := arrayStrictNullSnapshot(t, call)
					manifest := append([]catalog.ExpressionDependency(nil), binder.Dependencies...)
					if len(binder.Params.Parameters) != 0 {
						evaluation.Parameters = []sqltype.Value{sqltype.Null(binder.Params.Parameters[1].Type)}
					}
					folded, changed, err := planner.run(&evaluation, node, true)
					if err != nil || !changed {
						t.Fatal("post-child strict NULL was not eliminated", changed, err)
					}
					arrayStrictNullResult(t, node, folded)
					if value, err := folded.Eval(&evaluation); err != nil || !value.IsNull() {
						t.Fatal("eliminated executable", value, err)
					}
					if !reflect.DeepEqual(node, before) || !reflect.DeepEqual(binder.Dependencies, manifest) {
						t.Fatal("planner mutated original graph/Info/manifest")
					}
					arrayStrictNullUncalled(t, e)
				})
			}
			// Unknown imports are not NULL proofs, including a missing second slot.
			for _, text := range []string{"array_sample($1::integer[],nextval('strict_null_steps')::integer)", "array_sample($2::integer[],nextval('strict_null_steps')::integer)"} {
				node, binder, evaluation := strictNullUnitBind(t, e, op, scope, text)
				parameter := 1
				if text == "array_sample($2::integer[],nextval('strict_null_steps')::integer)" {
					parameter = 2
				}
				frames := [][]sqltype.Value{nil, make([]sqltype.Value, parameter), make([]sqltype.Value, parameter-1)}
				if parameter == 2 {
					frames = append(frames, []sqltype.Value{sqltype.Null(binder.Params.Parameters[2].Type), {}})
				}
				for _, frame := range frames {
					evaluation.Parameters = frame
					planned, _, err := planner.run(&evaluation, node, true)
					if _, ok := planned.(functions.ArrayRandom); err != nil || !ok {
						t.Fatal("unknown import became NULL", text, planned, err)
					}
				}
			}
			for _, text := range []string{"array_sample(NULL::integer[],1/0)", "array_sample(ARRAY[1/0],NULL::integer)"} {
				node, _, evaluation := strictNullUnitBind(t, e, op, scope, text)
				_, _, err := planner.run(&evaluation, node, true)
				nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "22012", Message: "division by zero"})
			}
			node, binder, evaluation := strictNullUnitBind(t, e, op, scope, "array_sample(NULL::integer[],abs(-1))")
			child := randomPlanningUnitDependency(t, e, binder.Dependencies, "abs")
			if err := enginetest.SetObjectACL(e, context.Background(), child.Ref, authorization.ACLValue{Explicit: true}); err != nil {
				t.Fatal(err)
			}
			evaluation.RoutineAuthority = eMustJSONBRoutineAuthority(t, e, reader)
			_, _, err := planner.run(&evaluation, node, true)
			nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for function abs"})
			arrayStrictNullUncalled(t, e)
			literal, literalBinder, literalContext := strictNullUnitBind(t, e, op, scope, "array_sample(NULL::integer[],nextval('strict_null_steps')::integer)")
			for _, name := range []string{"array_sample", "nextval"} {
				dependency := randomPlanningUnitDependency(t, e, literalBinder.Dependencies, name)
				if err := enginetest.SetObjectACL(e, context.Background(), dependency.Ref, authorization.ACLValue{Explicit: true}); err != nil {
					t.Fatal(err)
				}
			}
			literalContext.RoutineAuthority = eMustJSONBRoutineAuthority(t, e, reader)
			folded, _, err := planner.run(&literalContext, literal, true)
			if err != nil {
				t.Fatal("literal erasure demanded revoked outer/volatile sibling", err)
			}
			arrayStrictNullResult(t, literal, folded)
			if err := arrayStrictNullAdmitTree(&literalContext, folded); err != nil {
				t.Fatal("erased literal retained admission demand", err)
			}
			if value, err := folded.Eval(&literalContext); err != nil || !value.IsNull() {
				t.Fatal("erased literal retained evaluation demand", value, err)
			}
			arrayStrictNullUncalled(t, e)
			outer := randomPlanningUnitDependency(t, e, literalBinder.Dependencies, "array_sample")
			owner := e.CatalogRead().MustProcedure(outer.Ref.Address).Owner
			principal := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("array_strict_reader")
			if err := enginetest.SetObjectACL(e, context.Background(), outer.Ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{objectACLUnitEntry(owner, principal, authorization.ExecutePrivilege, 0)}}); err != nil {
				t.Fatal(err)
			}

			// Row NULL is deliberately not execution-known. Both row frames keep
			// the real volatile call, and only actual evaluation consumes a value.
			node, _, evaluation = strictNullUnitBind(t, e, op, scope, "array_sample(ARRAY[7],v+nextval('strict_null_steps')::integer)")
			for _, nullRow := range []bool{true, false} {
				row := sqltype.Null(node.(functions.ArrayRandom).Args[1].ResultUsage().Type)
				if !nullRow {
					row, err = sqltype.Integer(row.Type(), 0)
					if err != nil {
						t.Fatal(err)
					}
				}
				evaluation.Row = []sqltype.Value{row}
				planned, _, err := planner.run(&evaluation, node, true)
				if _, ok := planned.(functions.ArrayRandom); err != nil || !ok {
					t.Fatal("runtime row was erased", planned, err)
				}
				denied := evaluation
				denied.RoutineAuthority = eMustJSONBRoutineAuthority(t, e, reader)
				err = arrayStrictNullAdmitTree(&denied, planned)
				nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for function nextval"})
			}
			arrayStrictNullUncalled(t, e)

			// No-frame literal carriers must still respect RECORD, SRF and
			// compound NULL-aware semantics, not just their bound counterparts.
			for _, text := range []string{"pg_identify_object(NULL,nextval('strict_null_steps')::oid,0)", "generate_series(NULL::integer,nextval('strict_null_steps')::integer)"} {
				node, _, evaluation := strictNullUnitBind(t, e, op, scope, text)
				planned, _, err := planner.run(&evaluation, node, true)
				if err != nil || exec.RandomLiteralNull(planned) {
					t.Fatal("excluded literal provider erased", text, planned, err)
				}
				if descriptor, err := exec.ExpressionRoutineDescriptor(planned); err != nil || descriptor == nil {
					t.Fatal("excluded provider lost initialization identity", text, descriptor, err)
				}
			}
			for _, tc := range []struct {
				text string
				want any
			}{
				{"1 IS DISTINCT FROM NULL::integer", true},
				{"NULL::integer IS NOT DISTINCT FROM NULL::integer", true},
				{"NULLIF(1,NULL::integer)", int64(1)},
				{"GREATEST(1,NULL::integer)", int64(1)},
				{"LEAST(1,NULL::integer)", int64(1)},
				{"1 IN (1,NULL::integer)", true},
				{"1 NOT IN (1,NULL::integer)", false},
				{"NULL::integer = ANY(ARRAY[]::integer[])", false},
				{"NULL::integer = ALL(ARRAY[]::integer[])", true},
			} {
				node, _, evaluation := strictNullUnitBind(t, e, op, scope, tc.text)
				planned, _, err := planner.run(&evaluation, node, true)
				if err != nil {
					t.Fatal(tc.text, err)
				}
				value, err := planned.Eval(&evaluation)
				if err != nil || enginetest.Export(value) != tc.want {
					t.Fatal("compound literal semantics", tc.text, value, err)
				}
			}
			arrayStrictNullUncalled(t, e)
			for _, nullRow := range []bool{true, false} {
				name := "runtime_nonnull_effect"
				if nullRow {
					name = "runtime_null_effect"
				}
				t.Run(name, func(t *testing.T) {
					e, op, scope := arrayStrictNullFixture(t)
					node, _, evaluation := strictNullUnitBind(t, e, op, scope, "array_sample(ARRAY[7],v+nextval('strict_null_steps')::integer)")
					typ := node.(functions.ArrayRandom).Args[1].ResultUsage().Type
					row := sqltype.Null(typ)
					if !nullRow {
						var err error
						row, err = sqltype.Integer(typ, 0)
						if err != nil {
							t.Fatal(err)
						}
					}
					evaluation.Row = []sqltype.Value{row}
					planned, _, err := planner.run(&evaluation, node, true)
					if _, ok := planned.(functions.ArrayRandom); err != nil || !ok {
						t.Fatal("runtime frame lost invocation", planned, err)
					}
					arrayStrictNullUncalled(t, e)
					if err := arrayStrictNullAdmitTree(&evaluation, planned); err != nil {
						t.Fatal("authorized runtime graph", err)
					}
					value, err := planned.Eval(&evaluation)
					if err != nil || value.IsNull() != nullRow || !nullRow && value.ArrayLen() != 1 {
						t.Fatal("runtime provider result", value, err)
					}
					sequence := enginetest.CatalogRef(t, e, e.CatalogRead(), "strict_null_steps")
					e.Mu.Lock()
					called := e.Storage.Sequences[sequence.Address].Called
					e.Mu.Unlock()
					if !called {
						t.Fatal("runtime NULL suppressed nextval evaluation")
					}
				})
			}
		})
	}
}

func TestArrayRandomStrictNullOriginalAuthority(t *testing.T) {
	e, op, scope := arrayStrictNullFixture(t)
	other, otherOp, otherScope := arrayStrictNullFixture(t)
	_, _, foreign := strictNullUnitBind(t, other, otherOp, otherScope, "1")
	node, binder, evaluation := strictNullUnitBind(t, e, op, scope, "array_sample(NULL::integer[],nextval('strict_null_steps')::integer)")
	call := node.(functions.ArrayRandom)
	before := arrayStrictNullSnapshot(t, call)
	manifest := append([]catalog.ExpressionDependency(nil), binder.Dependencies...)
	captured, err := exec.ExpressionRoutineDescriptor(node)
	if err != nil || captured == nil || captured.Address() != (ids.ObjectAddress{ids.ProcedureClassID, 6216, 0}) || string(captured.Signature) != "2277,23" || captured.Result != 2277 || captured.RoutineKind != catalog.FunctionRoutine || !captured.Strict || captured.Volatility != expr.Volatile || captured.Builtin == nil || !exec.StrictScalarRoutine(captured) {
		t.Fatal("selected array_sample IN(anyarray,integer) strict volatile scalar declaration", captured, err)
	}
	builtin := captured.Builtin
	if builtin.Identity != (catalog.RoutineIdentity{ID: 6216, Namespace: 11, Name: "array_sample", Signature: captured.Signature}) || builtin.Kind != catalog.FunctionRoutine || builtin.Result != 2277 || !builtin.Strict || builtin.Volatility != expr.Volatile || builtin.ReturnsSet || builtin.Variadic != 0 || builtin.Defaults != 0 || builtin.Outputs != nil {
		t.Fatal("selected declaration is not exactly two required IN arguments and scalar anyarray result", builtin)
	}
	declaration := *captured
	outer := randomPlanningUnitDependency(t, e, manifest, "array_sample")
	child := randomPlanningUnitDependency(t, e, manifest, "nextval")
	for _, dependency := range []catalog.ExpressionDependency{outer, child} {
		if dependency.Ref.Domain() != e.Catalog.Domain || dependency.Ref.Epoch != evaluation.Catalog.Epoch() || dependency.Revision != evaluation.Catalog.MustProcedure(dependency.Ref.Address).Revision() {
			t.Fatal("original selected lifetime", dependency)
		}
	}
	for _, planner := range arrayStrictNullPlanners {
		local := evaluation
		folded, _, err := planner.run(&local, node, true)
		if err != nil {
			t.Fatal(planner.name, err)
		}
		arrayStrictNullResult(t, node, folded)
	}
	// Validate both original declarations explicitly: invocation pruning is
	// not itself an original-lifetime proof, nor does it require child EXECUTE.
	auth, _, err := exec.RoutineAdmissionAuthority(&evaluation)
	if err != nil {
		t.Fatal(err)
	}
	for _, dependency := range []catalog.ExpressionDependency{outer, child} {
		selected := evaluation.Catalog.MustProcedure(dependency.Ref.Address)
		if err := exec.AdmitRoutineDescriptor(auth, manifest, selected, nil); err != nil {
			t.Fatal("intact original lifetime", err)
		}
		missing := make([]catalog.ExpressionDependency, 0, len(manifest))
		for _, entry := range manifest {
			if entry.Ref.Address != dependency.Ref.Address {
				missing = append(missing, entry)
			}
		}
		for _, entries := range [][]catalog.ExpressionDependency{nil, {}, missing} {
			if err := exec.AdmitRoutineDescriptor(auth, entries, selected, nil); err == nil {
				t.Fatal("erasure manufactured missing original lifetime", dependency.Ref.Address)
			}
		}
		for _, epoch := range []bool{false, true} {
			stale := append([]catalog.ExpressionDependency(nil), manifest...)
			for i := range stale {
				if stale[i].Ref.Address == dependency.Ref.Address {
					if epoch {
						stale[i].Ref.Epoch++
					} else {
						stale[i].Revision++
					}
				}
			}
			if err := exec.AdmitRoutineDescriptor(auth, stale, selected, nil); err == nil {
				t.Fatal("erasure rescued stale epoch/revision")
			}
		}
		foreignAuth, _, err := exec.RoutineAdmissionAuthority(&foreign)
		if err != nil {
			t.Fatal(err)
		}
		if err := exec.AdmitRoutineDescriptor(foreignAuth, manifest, selected, nil); err == nil {
			t.Fatal("foreign same-OID catalog acquired original lifetime")
		}
	}
	// Rewrite the original carrier, not its command-private NULL surrogate.
	mapped, err := stored.RemapExpression(node, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
	if err != nil {
		t.Fatal("original graph remap", err)
	}
	mapped, retagged, err := stored.RetagTypes(other.CatalogRead(), mapped, func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == nil {
			return nil
		}
		current, _ := other.CatalogRead().LookupType(old.ID())
		return current
	})
	if err != nil || !retagged {
		t.Fatal("original graph did not retag to real destination types", retagged, err)
	}
	mappedCall, ok := mapped.(functions.ArrayRandom)
	mappedDescriptor, err := exec.ExpressionRoutineDescriptor(mapped)
	if !ok || mappedCall.Validate() != nil || err != nil || mappedDescriptor != captured || mappedCall.ResultUsage().Type == call.ResultUsage().Type || len(mappedCall.Args) != len(call.Args) {
		t.Fatal("retag lost original selected declaration/children", mapped, err)
	}
	if mappedDescriptor.Revision() != outer.Revision || !exec.SameRoutineDeclaration(mappedDescriptor, captured) {
		t.Fatal("retag replaced selected original declaration/revision")
	}
	foreignAuth, _, err := exec.RoutineAdmissionAuthority(&foreign)
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.AdmitRoutineDescriptor(foreignAuth, manifest, mappedDescriptor, nil); err == nil {
		t.Fatal("retagging types manufactured a foreign original lifetime")
	}
	selectedMapped := make(map[ids.ObjectAddress]*catalog.Procedure)
	plan.WalkExpression(mapped, func(n expr.Expr) {
		descriptor, err := exec.ExpressionRoutineDescriptor(n)
		if err != nil {
			t.Fatal("mapped callable carrier", err)
		}
		if descriptor != nil {
			selectedMapped[descriptor.Address()] = descriptor
		}
	})
	for _, dependency := range []catalog.ExpressionDependency{outer, child} {
		selected := selectedMapped[dependency.Ref.Address]
		if selected == nil || selected.Revision() != dependency.Revision || !exec.SameRoutineDeclaration(selected, evaluation.Catalog.MustProcedure(dependency.Ref.Address)) {
			t.Fatal("mapped graph lost original outer/nextval identity", dependency)
		}
		if err := exec.AdmitRoutineDescriptor(auth, manifest, selected, nil); err != nil {
			t.Fatal("remapped original dependencies", err)
		}
	}
	for _, planner := range arrayStrictNullPlanners {
		local := evaluation
		folded, _, err := planner.run(&local, mapped, true)
		if err != nil {
			t.Fatal("remapped independent plan", planner.name, err)
		}
		arrayStrictNullResult(t, mapped, folded)
	}
	// Real catalog-local declaration replacement must not be confused with an
	// equal numeric identity. This is the existing descriptor-COW probe pattern.
	changed := *core.RoutineAuthorityView(&evaluation)
	changed.Catalog = changed.Catalog.WithState(catalog.CloneState(changed.Catalog.State()))
	replacement := *captured
	replacement.Strict = false
	changed.Catalog.State().SetDescriptor(captured.Address(), &replacement)
	probe := evaluation
	probe.RoutineAuthority = &changed
	changedAuth, _, err := exec.RoutineAdmissionAuthority(&probe)
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.AdmitRoutineDescriptor(changedAuth, manifest, captured, nil); err == nil {
		t.Fatal("incompatible selected declaration accepted")
	}
	forged := call
	forged.Info.RoutineReference = &replacement
	if err := exec.AdmitRoutineCall(&evaluation, forged, false, false); err == nil {
		t.Fatal("forged carrier declaration accepted")
	}
	forged = call
	table := enginetest.RoleTableRef(t, e, "strict_null_rows")
	forged.Info.RoutineRecipeReference = evaluation.Catalog.MustRelation(table.Address)
	if _, _, err := stored.RetagTypes(e.CatalogRead(), forged, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }); err == nil {
		t.Fatal("real table passed as a forged owning view recipe")
	}
	// Direct-source lifetime retains its old snapshot, but must reject the
	// refreshed owner-COW revision even when this invocation is NULL.
	reader, done := enginetest.RoleOperation(t, e, "array_strict_reader")
	defer done()
	evaluation.RoutineAuthority, evaluation.RoutineChecks = nil, nil
	evaluation.RoutineRecipe = false
	scope.RoutineAuth = *eMustJSONBRoutineAuthority(t, e, reader)
	scope.RoutineDependencies = manifest
	if err := exec.AdmitRoutineCall(&evaluation, node, false, false); err != nil {
		t.Fatal("valid original direct-source NULL", err)
	}
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	target := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("array_strict_owner")
	if err := role.StageOwnership(stage, base.MustProcedure(captured.Address()), target); err != nil {
		t.Fatal(err)
	}
	ownershipUnitPublish(t, e, stage)
	e.DiscardCatalogStage(stage)
	if err := exec.AdmitRoutineCall(&evaluation, node, false, false); err != nil {
		t.Fatal("retained snapshot invalidated by owner COW", err)
	}
	scope.RoutineAuth = *eMustJSONBRoutineAuthority(t, e, reader)
	err = exec.AdmitRoutineCall(&evaluation, node, false, false)
	var conflict catalog.Conflict
	if !errors.As(err, &conflict) {
		t.Fatal("NULL rescued refreshed stale source", err)
	}
	if !reflect.DeepEqual(node, before) || !reflect.DeepEqual(binder.Dependencies, manifest) || !reflect.DeepEqual(*captured, declaration) {
		t.Fatal("planning/remap/lifetime probes mutated original graph/manifest/declaration")
	}
	arrayStrictNullUncalled(t, e)
}

func TestArrayRandomStrictNullConcurrentReuse(t *testing.T) {
	e, op, scope := arrayStrictNullFixture(t)
	reader, done := enginetest.RoleOperation(t, e, "array_strict_reader")
	defer done()
	readerScope, err := e.StartStatement(reader)
	if err != nil {
		t.Fatal(err)
	}
	defer readerScope.Finish(nil)
	arrayStrictNullAdmitScope(t, readerScope)
	node, binder, evaluation := strictNullUnitBind(t, e, op, scope, "array_sample($1::integer[],nextval('strict_null_steps')::integer)")
	before := arrayStrictNullSnapshot(t, node)
	manifest := append([]catalog.ExpressionDependency(nil), binder.Dependencies...)
	child := randomPlanningUnitDependency(t, e, manifest, "nextval")
	if err := enginetest.SetObjectACL(e, context.Background(), child.Ref, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	// Import the positive frame through the real binder; no synthetic providers.
	positive, _, positiveContext := strictNullUnitBind(t, e, op, scope, "ARRAY[7]")
	array, err := positive.Eval(&positiveContext)
	if err != nil {
		t.Fatal(err)
	}
	null := sqltype.Null(binder.Params.Parameters[1].Type)
	ownerContext := evaluation
	ownerContext.RoutineAuthority = eMustJSONBRoutineAuthority(t, e, op)
	readerContext := evaluation
	readerContext.Host = core.HostOf(readerScope)
	readerContext.RoutineAuthority = eMustJSONBRoutineAuthority(t, e, reader)
	for round := range 2 {
		contexts := []exec.Context{ownerContext, readerContext}
		frames := [][]sqltype.Value{{null}, {array}}
		if round == 1 {
			contexts[0], contexts[1] = contexts[1], contexts[0]
		}
		type outcome struct {
			worker int
			folded expr.Expr
			err error
		}
		start := make(chan struct{})
		results := make(chan outcome, 2)
		var workers sync.WaitGroup
		for i := range contexts {
			workers.Add(1)
			go func(i int, local exec.Context) {
				defer workers.Done()
				<-start
				local.Parameters = frames[i]
				// Each planner starts independently from the immutable original.
				for _, planner := range arrayStrictNullPlanners {
					folded, _, err := planner.run(&local, node, true)
					if err == nil {
						err = arrayStrictNullAdmitTree(&local, folded)
					}
					results <- outcome{i, folded, err}
				}
			}(i, contexts[i])
		}
		// Drain before joining: four outcomes cannot fit in a two-item buffer.
		close(start)
		got := make([]outcome, 0, 4)
		for range 4 {
			got = append(got, <-results)
		}
		workers.Wait()
		close(results)
		for _, result := range got {
			if result.worker == 0 {
				if result.err != nil {
					t.Fatal("NULL worker acquired sibling demand", round, result.err)
				}
				arrayStrictNullResult(t, node, result.folded)
			} else if round == 0 {
				nullCountUnitDiagnostic(t, result.err, sqlstate.Error{Code: "42501", Message: "permission denied for function nextval"})
			} else if _, ok := result.folded.(functions.ArrayRandom); result.err != nil || !ok {
				t.Fatal("authorized nonNULL worker inherited erasure", result.folded, result.err)
			}
		}
		if !reflect.DeepEqual(node, before) || !reflect.DeepEqual(binder.Dependencies, manifest) {
			t.Fatal("concurrent commands mutated original carrier/manifest")
		}
		arrayStrictNullUncalled(t, e)
	}
	// A current regrant on the same reader host recovers real invocation. All
	// publication happens after workers join, never concurrently with reads.
	owner := e.CatalogRead().MustProcedure(child.Ref.Address).Owner
	principal := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("array_strict_reader")
	if err := enginetest.SetObjectACL(e, context.Background(), child.Ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{objectACLUnitEntry(owner, principal, authorization.ExecutePrivilege, 0)}}); err != nil {
		t.Fatal(err)
	}
	for _, planner := range arrayStrictNullPlanners {
		local := readerContext
		local.RoutineAuthority = eMustJSONBRoutineAuthority(t, e, reader)
		local.Parameters = []sqltype.Value{array}
		planned, _, err := planner.run(&local, node, true)
		if _, ok := planned.(functions.ArrayRandom); err != nil || !ok {
			t.Fatal("regrant reused NULL command", planned, err)
		}
		auth, _, err := exec.RoutineAdmissionAuthority(&local)
		if err != nil {
			t.Fatal(err)
		}
		if err := exec.AdmitRoutineDescriptor(auth, manifest, local.Catalog.MustProcedure(child.Ref.Address), nil); err != nil {
			t.Fatal("current regrant did not authorize surviving nextval", err)
		}
	}
	// Execute once only: nextval=1 is a legal sample size for the singleton.
	local := readerContext
	local.RoutineAuthority = eMustJSONBRoutineAuthority(t, e, reader)
	local.Parameters = []sqltype.Value{array}
	planned, _, err := exec.SimplifyConditional(&local, node, true)
	if err != nil {
		t.Fatal(err)
	}
	value, err := planned.Eval(&local)
	if err != nil || value.IsNull() || value.ArrayLen() != 1 {
		t.Fatal("authorized surviving provider did not invoke", value, err)
	}
	sequence := enginetest.CatalogRef(t, e, e.CatalogRead(), "strict_null_steps")
	e.Mu.Lock()
	called := e.Storage.Sequences[sequence.Address].Called
	e.Mu.Unlock()
	if !called || !reflect.DeepEqual(node, before) || !reflect.DeepEqual(binder.Dependencies, manifest) {
		t.Fatal("real invocation absent or original mutated")
	}
	local.Parameters = []sqltype.Value{null}
	again, _, err := exec.SimplifyConditional(&local, node, true)
	if err != nil {
		t.Fatal("same reader host failed to recover NULL frame", err)
	}
	arrayStrictNullResult(t, node, again)
}
