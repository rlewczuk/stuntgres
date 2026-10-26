package core_test

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Use the genuine DEFAULT consumer policy and target namespace. Projection
// binding alone would miss the earlier volatile-default admission boundary.
func arrayStrictNullBindDefault(t *testing.T, e *core.Engine, op *session.Operation, scope *core.StatementScope, text string, usage sqltype.Usage) (expr.Expr, *analyze.Binder, exec.Context, error) {
	t.Helper()
	raw := enginetest.Statement(t, "SELECT "+text).(*parser.Select).Targets[0]
	_, _, evaluation := strictNullUnitBind(t, e, op, scope, "1")
	target := enginetest.RoleTableRef(t, e, "strict_null_rows")
	e.Mu.Lock()
	defer e.Mu.Unlock()
	binder := core.NewExpressionBinder(context.Background(), e, scope.Input, op, nil)
	binder.Query.Auth = core.RoutineAuthorityView(&evaluation)
	binder.Target.Ref = target
	binder.Target.Relation = evaluation.Catalog.MustRelation(target.Address)
	bindTestTargetNamespace(binder)
	node, err := binder.BindDefault(raw, catalog.Column{Name: "v", Usage: usage})
	evaluation.RoutineChecks = binder.Dependencies
	return node, binder, evaluation, err
}

func TestArrayRandomStrictNullStoredDefaultClosure(t *testing.T) {
	t.Run("stored_view_original_demand", arrayStrictNullStoredView)
	t.Run("unsupported_default_math_remains_rejected", func(t *testing.T) {
		e, op, scope := arrayStrictNullFixture(t)
		text := "array_sample(NULL::integer[],abs(-1))"
		projection, _, _ := strictNullUnitBind(t, e, op, scope, text)
		if _, _, _, err := arrayStrictNullBindDefault(t, e, op, scope, text, projection.ResultUsage()); err == nil || err.Error() != "stuntgres: functions are unsupported" {
			t.Fatal("strict NULL closure widened the existing DEFAULT abs restriction", err)
		}
	})
	t.Run("original_render_rebind_retag", func(t *testing.T) {
		e, op, scope := arrayStrictNullFixture(t)
		for _, text := range []string{
			"array_sample(NULL::integer[],nextval('strict_null_steps')::integer)",
			"array_sample(NULL::integer[],ln(1::numeric)::integer)",
			"array_shuffle(NULL::integer[])",
		} {
			t.Run(text, func(t *testing.T) {
				projection, _, _ := strictNullUnitBind(t, e, op, scope, text)
				node, binder, evaluation, err := arrayStrictNullBindDefault(t, e, op, scope, text, projection.ResultUsage())
				call, ok := node.(functions.ArrayRandom)
				if err != nil || !ok || !analyze.ClosedSequenceDefault(node) || call.Validate() != nil {
					t.Fatal("closed strict NULL default was not retained as its real provider", node, err)
				}
				if node.ResultUsage() != projection.ResultUsage() {
					t.Fatal("DEFAULT storage coercion changed destination type/typmod/collation")
				}
				before := arrayStrictNullSnapshot(t, node)
				manifest := slices.Clone(binder.Dependencies)
				selected, err := exec.ExpressionRoutineDescriptor(node)
				if err != nil || selected == nil || selected.Builtin == nil {
					t.Fatal("stored original lost selected declaration", selected, err)
				}
				declaration := *selected
				replayBefore, err := call.Render()
				if err != nil {
					t.Fatal("original replay contract", err)
				}
				var renderedRefs []ids.ObjectRef
				renderer := stored.ScalarRenderer{Dependencies: &renderedRefs, CapturedChecks: manifest}
				canonical, err := renderer.SQL(context.Background(), evaluation.Catalog, node)
				if err != nil || !strings.Contains(canonical, "pg_catalog."+call.Kind.Name()+"(") {
					t.Fatal("default rendering replaced the original by NULL", canonical, err)
				}
				outer := randomPlanningUnitDependency(t, e, manifest, call.Kind.Name())
				if !slices.Contains(renderedRefs, outer.Ref) {
					t.Fatal("rendered default omitted original outer dependency", renderedRefs)
				}
				if strings.Contains(text, "nextval") {
					child := randomPlanningUnitDependency(t, e, manifest, "nextval")
					sequence := enginetest.CatalogRef(t, e, evaluation.Catalog, "strict_null_steps")
					if !strings.Contains(canonical, "pg_catalog.nextval(") || !slices.Contains(renderedRefs, child.Ref) || !slices.Contains(renderedRefs, sequence) {
						t.Fatal("rendering erased selected nextval/sequence dependencies", canonical, renderedRefs)
					}
				}
				rebound, reboundBinder, reboundContext, err := arrayStrictNullBindDefault(t, e, op, scope, canonical, node.ResultUsage())
				reboundCall, ok := rebound.(functions.ArrayRandom)
				reboundDescriptor, descriptorErr := exec.ExpressionRoutineDescriptor(rebound)
				if err != nil || !ok || reboundCall.Validate() != nil || descriptorErr != nil || !exec.SameRoutineDeclaration(selected, reboundDescriptor) {
					t.Fatal("symbolic DEFAULT replay changed selected original", rebound, err, descriptorErr)
				}
				if rebound.ResultUsage() != node.ResultUsage() {
					t.Fatal("DEFAULT rebind changed destination type/typmod/collation")
				}
				for _, dependency := range manifest {
					if dependency.Ref.Address.ClassID == ids.ProcedureClassID && !slices.Contains(reboundBinder.Dependencies, dependency) {
						t.Fatal("replay lost original routine dependency", dependency)
					}
				}
				// Retag the original executable graph, not a folded NULL. The
				// real catalog mapper retains each selected original declaration.
				remapped, err := stored.RemapExpression(node, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
				if err != nil {
					t.Fatal("remapping stored original", err)
				}
				retagged, _, err := stored.RetagTypes(evaluation.Catalog, remapped, func(old *sqltype.Descriptor) *sqltype.Descriptor {
					if old == nil {
						return nil
					}
					current, _ := evaluation.Catalog.LookupType(old.ID())
					return current
				})
				retaggedDescriptor, descriptorErr := exec.ExpressionRoutineDescriptor(retagged)
				if _, ok := retagged.(functions.ArrayRandom); err != nil || !ok || descriptorErr != nil || retaggedDescriptor != selected || !analyze.ClosedSequenceDefault(retagged) {
					t.Fatal("retag lost original DEFAULT carrier/declaration", retagged, err, descriptorErr)
				}
				if retagged.ResultUsage() != node.ResultUsage() {
					t.Fatal("DEFAULT retag changed destination usage")
				}
				retaggedSQL, err := stored.DefaultSQL(context.Background(), evaluation.Catalog, retagged)
				if err != nil || retaggedSQL != canonical {
					t.Fatal("retag changed symbolic stored default", retaggedSQL, canonical, err)
				}
				for _, planner := range arrayStrictNullPlanners {
					for _, original := range []struct {
						node expr.Expr
						context exec.Context
					}{{node, evaluation}, {rebound, reboundContext}, {retagged, evaluation}} {
						local := original.context
						folded, _, err := planner.run(&local, original.node, true)
						if err != nil {
							t.Fatal("stored original failed post-child planning", planner.name, err)
						}
						arrayStrictNullResult(t, original.node, folded)
						if value, err := folded.Eval(&local); err != nil || !value.IsNull() {
							t.Fatal("command-private DEFAULT result", value, err)
						}
					}
				}
				if !reflect.DeepEqual(node, before) || !reflect.DeepEqual(manifest, binder.Dependencies) || !reflect.DeepEqual(*selected, declaration) {
					t.Fatal("DEFAULT eligibility/render/replay/retag/planning mutated original")
				}
				replayAfter, err := call.Render()
				if err != nil || replayAfter.Replay.RelationOnly != replayBefore.Replay.RelationOnly {
					t.Fatal("DEFAULT eligibility/rendering changed original provider replay contract", err)
				}
				arrayStrictNullUncalled(t, e)
			})
		}
	})

	t.Run("child_failures_remain_at_consumption", func(t *testing.T) {
		e, op, scope := arrayStrictNullFixture(t)
		reader, done := enginetest.RoleOperation(t, e, "array_strict_reader")
		defer done()
		for _, text := range []string{"array_sample(NULL::integer[],1/0)", "array_sample(NULL::integer[],ln(1::numeric)::integer)"} {
			projection, _, _ := strictNullUnitBind(t, e, op, scope, text)
			node, binder, evaluation, err := arrayStrictNullBindDefault(t, e, op, scope, text, projection.ResultUsage())
			if err != nil || !analyze.ClosedSequenceDefault(node) {
				t.Fatal("DEFAULT eligibility evaluated immutable sibling", text, err)
			}
			if canonical, err := stored.DefaultSQL(context.Background(), evaluation.Catalog, node); err != nil || !strings.Contains(canonical, "pg_catalog.array_sample(") {
				t.Fatal("rendering evaluated immutable sibling", canonical, err)
			}
			want := sqlstate.Error{Code: "22012", Message: "division by zero"}
			if strings.Contains(text, "ln(") {
				child := randomPlanningUnitDependency(t, e, binder.Dependencies, "ln")
				selected := e.CatalogRead().MustProcedure(child.Ref.Address)
				if selected.Address().ObjectID != 1734 || string(selected.Signature) != "1700" || selected.Result != 1700 || !selected.Strict || selected.Volatility != expr.Immutable {
					t.Fatal("DEFAULT witness did not select the real numeric ln declaration", selected)
				}
				if err := enginetest.SetObjectACL(e, context.Background(), child.Ref, authorization.ACLValue{Explicit: true}); err != nil {
					t.Fatal(err)
				}
				evaluation.RoutineAuthority = eMustJSONBRoutineAuthority(t, e, reader)
				want = sqlstate.Error{Code: "42501", Message: "permission denied for function ln"}
			}
			for _, planner := range arrayStrictNullPlanners {
				local := evaluation
				_, _, err := planner.run(&local, node, true)
				nullCountUnitDiagnostic(t, err, want)
			}
			arrayStrictNullUncalled(t, e)
		}
	})

	t.Run("bounded_closure_rejections", func(t *testing.T) {
		e, op, scope := arrayStrictNullFixture(t)
		for _, text := range []string{
			"array_sample(ARRAY[7],nextval('strict_null_steps')::integer)",
			"array_sample(NULL::integer[],v)",
			"array_sample($1::integer[],nextval('strict_null_steps')::integer)",
			"array_sample(NULL::integer[],$1::integer)",
			"array_sample(NULL::integer[],random(1,1))",
		} {
			t.Run(text, func(t *testing.T) {
				node, _, _ := strictNullUnitBind(t, e, op, scope, text)
				before := arrayStrictNullSnapshot(t, node)
				if analyze.ClosedSequenceDefault(node) {
					t.Fatal("unrelated volatile/frame-dependent DEFAULT gained closure", text)
				}
				if _, _, _, err := arrayStrictNullBindDefault(t, e, op, scope, text, node.ResultUsage()); err == nil {
					t.Fatal("DEFAULT binder accepted excluded closure", text)
				}
				if _, err := stored.DefaultSQL(context.Background(), e.CatalogRead(), node); err == nil {
					t.Fatal("DEFAULT renderer accepted excluded closure", text)
				}
				if !reflect.DeepEqual(node, before) {
					t.Fatal("negative closure probe mutated original")
				}
			})
		}
		node, binder, _ := strictNullUnitBind(t, e, op, scope, "array_sample(NULL::integer[],nextval('strict_null_steps')::integer)")
		call := node.(functions.ArrayRandom)
		for _, tc := range []struct {
			name string
			mutate func(*functions.ArrayRandom)
		}{
			{"kind", func(n *functions.ArrayRandom) { n.Kind = functions.ArrayRandomKind(255) }},
			{"arity", func(n *functions.ArrayRandom) { n.Args = n.Args[:1] }},
			{"missing_child", func(n *functions.ArrayRandom) { n.Args[1] = nil }},
			{"namespace", func(n *functions.ArrayRandom) { n.Namespace = "not_pg_catalog" }},
		} {
			t.Run(tc.name, func(t *testing.T) {
				bad := call
				bad.Args = slices.Clone(call.Args)
				tc.mutate(&bad)
				if analyze.ClosedSequenceDefault(bad) {
					t.Fatal("malformed ArrayRandom gained DEFAULT closure", tc.name)
				}
				if _, err := stored.DefaultSQL(context.Background(), e.CatalogRead(), bad); err == nil {
					t.Fatal("malformed ArrayRandom DEFAULT rendered", tc.name)
				}
			})
		}
		other, otherOp, otherScope := arrayStrictNullFixture(t)
		foreign, _, _ := strictNullUnitBind(t, other, otherOp, otherScope, "array_sample(NULL::integer[],nextval('strict_null_steps')::integer)")
		// Boolean eligibility has no catalog authority. Structural closure may
		// succeed; the real renderer must still reject foreign ownership/types.
		forged := call
		forged.Info.RoutineReference = foreign.(functions.ArrayRandom).Info.RoutineReference
		for _, candidate := range []expr.Expr{foreign, forged} {
			if _, err := stored.DefaultSQL(context.Background(), e.CatalogRead(), candidate); err == nil {
				t.Fatal("closed NULL DEFAULT laundered foreign catalog authority")
			}
		}
		selected, err := exec.ExpressionRoutineDescriptor(node)
		if err != nil || selected == nil {
			t.Fatal("missing actual selected default declaration", err)
		}
		stale := *selected
		stale.ObjectRevision++
		forged = call
		forged.Info.RoutineReference = &stale
		if _, err := (stored.ScalarRenderer{CapturedChecks: binder.Dependencies}).SQL(context.Background(), e.CatalogRead(), forged); err == nil {
			t.Fatal("erased DEFAULT rescued stale owning declaration revision")
		}
		for _, dependency := range binder.Dependencies {
			if dependency.Ref.Address.ClassID != ids.ProcedureClassID {
				continue
			}
			missing := make([]catalog.ExpressionDependency, 0, len(binder.Dependencies))
			for _, entry := range binder.Dependencies {
				if entry.Ref.Address != dependency.Ref.Address {
					missing = append(missing, entry)
				}
			}
			if _, err := (stored.ScalarRenderer{CapturedChecks: missing}).SQL(context.Background(), e.CatalogRead(), node); err == nil {
				t.Fatal("original default rendering ignored missing routine lifetime", dependency.Ref.Address)
			}
		}
		arrayStrictNullUncalled(t, e)
		arrayStrictNullUncalled(t, other)
	})

	t.Run("published_default_keeps_original_manifest", func(t *testing.T) {
		e, op, scope := arrayStrictNullFixture(t)
		if err := scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
		enginetest.RoleExecute(t, e, op, "CREATE TABLE array_strict_default(v integer[] DEFAULT array_sample(NULL::integer[],nextval('strict_null_steps')::integer))")
		view := e.CatalogRead()
		ref := enginetest.RoleTableRef(t, e, "array_strict_default")
		relation := view.MustRelation(ref.Address)
		definition, ok := relation.ColumnMeta.Defaults[1]
		call, genuine := definition.Expression.(functions.ArrayRandom)
		if !ok || !genuine || call.Validate() != nil || definition.OID == 0 {
			t.Fatal("published DEFAULT was replaced by NULL instead of retaining original", definition)
		}
		before := arrayStrictNullSnapshot(t, definition.Expression)
		manifest := slices.Clone(definition.Dependencies)
		if err := (core.RelationProvider).Validate(view, relation); err != nil {
			t.Fatal("published strict NULL DEFAULT descriptor rejected", err)
		}
		var rendered []ids.ObjectRef
		canonical, err := (stored.ScalarRenderer{Owner: relation, CapturedManifest: manifest, Dependencies: &rendered}).SQL(context.Background(), view, definition.Expression)
		if err != nil || !strings.Contains(canonical, "pg_catalog.array_sample(") || !strings.Contains(canonical, "pg_catalog.nextval(") {
			t.Fatal("published DEFAULT lost original symbolic calls", canonical, err)
		}
		for _, name := range []string{"array_sample", "nextval"} {
			found := false
			for _, dependency := range manifest {
				if dependency.Address.ClassID == ids.ProcedureClassID && view.MustProcedure(dependency.Address).Name() == name {
					found = slices.Contains(rendered, dependency)
				}
			}
			if !found {
				t.Fatal("published/rendered manifest omitted original routine", name, manifest, rendered)
			}
		}
		sequence := enginetest.CatalogRef(t, e, view, "strict_null_steps")
		if !slices.Contains(manifest, sequence) || !slices.Contains(rendered, sequence) || !reflect.DeepEqual(definition.Expression, before) || !reflect.DeepEqual(definition.Dependencies, manifest) {
			t.Fatal("published DEFAULT sequence dependency or original graph changed")
		}
		arrayStrictNullUncalled(t, e)
	})
}
