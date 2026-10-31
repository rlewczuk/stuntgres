package binder_test

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
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
				enginetest.Diagnostic(t, err, want)
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


func strictNullUnitBind(t *testing.T, e *core.Engine, op *session.Operation, scope *core.StatementScope, text string) (expr.Expr, *analyze.Binder, exec.Context) {
	t.Helper()
	raw := enginetest.Statement(t, "SELECT "+text).(*parser.Select).Targets[0]
	ref := enginetest.RoleTableRef(t, e, "strict_null_rows")
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	b := core.NewExpressionBinder(context.Background(), e, scope.Input, op, nil)
	b.Query.Auth, b.Target.Ref = &auth, ref
	b.Target.Relation = auth.Catalog.MustRelation(ref.Address)
	bindTestTargetNamespace(b)
	b.Clause.AggregateClause = "SELECT"
	node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	e.Mu.Unlock()
	if err != nil {
		t.Fatal("actual provider binding", text, err)
	}
	return node, b, exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(scope), Catalog: auth.Catalog, Input: scope.Input, Output: scope.Output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineRecipe: true, RoutineChecks: b.Dependencies}}
}

// Called by the selected StoredDefaultClosure definition. This reaches actual
// stored source preparation, not a fresh scalar binding in place of the view.
func arrayStrictNullStoredView(t *testing.T) {
	e, op, setupScope := arrayStrictNullFixture(t)
	if err := setupScope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		"CREATE VIEW array_strict_literal_view AS SELECT array_sample(NULL::integer[],nextval('strict_null_steps')::integer) AS v",
		"CREATE VIEW array_strict_runtime_view AS SELECT array_sample(ARRAY[7],v+nextval('strict_null_steps')::integer) AS v FROM strict_null_rows",
		"CREATE VIEW array_strict_nonnull_view AS SELECT array_sample(ARRAY[7],nextval('strict_null_steps')::integer) AS v",
		"CREATE VIEW array_strict_unused_window AS SELECT 1 AS v WINDOW unused AS (ORDER BY nextval('strict_null_steps'))",
		"GRANT SELECT ON strict_null_rows TO array_strict_owner",
		"GRANT USAGE,SELECT ON SEQUENCE strict_null_steps TO array_strict_owner",
	} {
		enginetest.RoleExecute(t, e, op, sql)
	}
	literalRef := enginetest.CatalogRef(t, e, e.CatalogRead(), "array_strict_literal_view")
	beforeOwner := stored.ViewOf(e.CatalogRead().MustRelation(literalRef.Address))
	if beforeOwner == nil {
		t.Fatal("missing real stored literal view definition")
	}
	originalProject, ok := beforeOwner.Query().Root.(*plan.ProjectNode)
	if !ok || len(originalProject.Expressions) != 1 {
		t.Fatal("unexpected real stored projection", beforeOwner.Query().Root)
	}
	original := originalProject.Expressions[0]
	call, ok := original.(functions.ArrayRandom)
	if !ok || call.Validate() != nil || len(call.Args) != 2 {
		t.Fatalf("stored literal view did not retain real ArrayRandom: %#v", original)
	}
	graph := arrayStrictNullSnapshot(t, original)
	manifest := slices.Clone(beforeOwner.Dependencies())
	schema := slices.Clone(originalProject.QuerySchema)
	if len(originalProject.AnalysisExpressions) != 1 {
		t.Fatal("stored literal fixture did not capture its actual ancillary carrier", originalProject.AnalysisExpressions)
	}
	analysisGraph := make([]expr.Expr, len(originalProject.AnalysisExpressions))
	for i, expression := range originalProject.AnalysisExpressions {
		if _, ok := expression.(functions.ArrayRandom); !ok {
			t.Fatal("unexpected stored ancillary original", expression)
		}
		analysisGraph[i] = arrayStrictNullSnapshot(t, expression)
	}
	assertPrivateAnalysis := func(project *plan.ProjectNode) {
		t.Helper()
		if len(project.AnalysisExpressions) != len(analysisGraph) {
			t.Fatal("private preflight removed ancillary graph instead of planning it")
		}
		for i, expression := range project.AnalysisExpressions {
			arrayStrictNullResult(t, analysisGraph[i], expression)
		}
	}
	outer := randomPlanningUnitDependency(t, e, manifest, "array_sample")
	child := randomPlanningUnitDependency(t, e, manifest, "nextval")
	selected, err := exec.ExpressionRoutineDescriptor(original)
	if err != nil || selected == nil || selected.Address() != outer.Ref.Address || selected.Revision() != outer.Revision {
		t.Fatal("stored literal view lost selected original declaration", selected, err)
	}
	declaration := *selected
	for _, sql := range []string{
		"ALTER VIEW array_strict_literal_view OWNER TO array_strict_owner",
		"ALTER VIEW array_strict_runtime_view OWNER TO array_strict_owner",
		"ALTER VIEW array_strict_nonnull_view OWNER TO array_strict_owner",
		"ALTER VIEW array_strict_unused_window OWNER TO array_strict_owner",
		"GRANT SELECT ON array_strict_literal_view,array_strict_runtime_view,array_strict_nonnull_view,array_strict_unused_window TO array_strict_reader",
	} {
		enginetest.RoleExecute(t, e, op, sql)
	}
	if err := enginetest.SetObjectACL(e, context.Background(), child.Ref, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	reader, done := enginetest.RoleOperation(t, e, "array_strict_reader")
	defer done()
	readerScope, err := e.StartStatement(reader)
	if err != nil {
		t.Fatal(err)
	}
	defer readerScope.Finish(nil)
	arrayStrictNullAdmitScope(t, readerScope)
	auth := eMustJSONBRoutineAuthority(t, e, reader)
	consumer := auth.Catalog.MustRelation(literalRef.Address)
	definition := stored.ViewOf(consumer)
	if definition == nil || !reflect.DeepEqual(definition.Query().Root.(*plan.ProjectNode).Expressions[0], graph) || !reflect.DeepEqual(definition.Dependencies(), manifest) {
		t.Fatal("ALTER OWNER changed immutable stored literal graph/manifest")
	}
	evaluation := exec.Context{
		EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(readerScope), Catalog: auth.Catalog, Input: readerScope.Input, Output: readerScope.Output},
		EvalFrame: exec.EvalFrame{Scope: 1, Frame: originalProject.Frame},
		EvalAdmission: exec.EvalAdmission{RoutineAuthority: auth, RoutineRecipe: true, RoutineChecks: definition.Dependencies(), RoutineConsumer: consumer},
	}
	known, err := exec.ExecutionKnownNullArgument(&evaluation, call.Args[0])
	if err != nil || !known {
		t.Fatalf("stored/owner-COW NULL supplier is not a genuine literal proof: child=%#v info=%#v err=%v", call.Args[0], expr.InfoOf(call.Args[0]), err)
	}
	for _, uses := range [][]int{nil, {0}} {
		local := evaluation
		prepared, reads, err := view.PrepareStoredExecutionRoot(&local, definition, uses)
		if err != nil {
			t.Fatal("stored view projection preflight demanded eliminated child", err)
		}
		project, ok := prepared.(*plan.ProjectNode)
		if !ok || len(project.Expressions) != 1 {
			t.Fatal("stored preparation changed output layout", prepared)
		}
		arrayStrictNullResult(t, original, project.Expressions[0])
		assertPrivateAnalysis(project)
		if !reflect.DeepEqual(project.QuerySchema, schema) {
			t.Fatal("stored NULL erasure changed frozen output schema")
		}
		if err := view.ValidateMarkedQueryJoin(&local, prepared, auth.Catalog, nil, plan.RowMark{}, false, nil, reads); err != nil {
			t.Fatal("stored preflight result revived nested nextval demand", err)
		}
	}
	// Exercise the complete production source lane too: it constructs its own
	// context/authority and captures sources after metadata preflight/admission.
	principal := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("array_strict_reader")
	e.Mu.Lock()
	prepared, source, err := e.PrepareStoredQueryLocked(readerScope, literalRef, definition, principal, plan.RowMark{}, nil, []int{0})
	if source != nil {
		source.Close()
	}
	if err != nil {
		t.Fatal("real stored source lane revived erased nextval", err)
	}
	project, ok := prepared.(*plan.ProjectNode)
	if !ok || len(project.Expressions) != 1 {
		t.Fatal("real stored source returned wrong projection", prepared)
	}
	arrayStrictNullResult(t, original, project.Expressions[0])
	assertPrivateAnalysis(project)
	arrayStrictNullUncalled(t, e)

	// Surviving row/nonNULL calls and an unused named window still undergo
	// their established admission. Ancillary preflight is not a blanket skip.
	for _, name := range []string{"array_strict_runtime_view", "array_strict_nonnull_view", "array_strict_unused_window"} {
		controlRef := enginetest.CatalogRef(t, e, e.CatalogRead(), name)
		controlConsumer := e.CatalogRead().MustRelation(controlRef.Address)
		control := stored.ViewOf(controlConsumer)
		controlProject := control.Query().Root.(*plan.ProjectNode)
		controlOriginal := controlProject.Expressions[0]
		if name != "array_strict_unused_window" {
			if _, ok := controlOriginal.(functions.ArrayRandom); !ok || controlOriginal.Properties().RowDependent != (name == "array_strict_runtime_view") {
				t.Fatal("stored control lost actual provider/dependence", name, controlOriginal)
			}
		}
		controlGraph := arrayStrictNullSnapshot(t, controlOriginal)
		controlAnalysis := make([]expr.Expr, len(controlProject.AnalysisExpressions))
		for i, expression := range controlProject.AnalysisExpressions {
			controlAnalysis[i] = arrayStrictNullSnapshot(t, expression)
		}
		controlManifest := slices.Clone(control.Dependencies())
		local := evaluation
		local.RoutineConsumer, local.RoutineChecks = controlConsumer, control.Dependencies()
		surviving, _, err := view.PrepareStoredExecutionRoot(&local, control, []int{0})
		if err != nil {
			t.Fatal("nonimmutable stored control was evaluated during planning", name, err)
		}
		stillDemanded := false
		for _, expression := range surviving.(*plan.ProjectNode).AnalysisExpressions {
			plan.WalkExpression(expression, func(childExpr expr.Expr) {
				descriptor, err := exec.ExpressionRoutineDescriptor(childExpr)
				if err != nil {
					t.Fatal("surviving ancillary carrier", err)
				}
				if descriptor != nil && descriptor.Address() == child.Ref.Address {
					stillDemanded = true
				}
			})
		}
		if !stillDemanded {
			t.Fatal("ancillary preflight erased a surviving nextval control", name)
		}
		e.Mu.Lock()
		_, source, err = e.PrepareStoredQueryLocked(readerScope, controlRef, control, principal, plan.RowMark{}, nil, []int{0})
		if source != nil {
			source.Close()
		}
		enginetest.Diagnostic(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for function nextval"})
		if !reflect.DeepEqual(controlOriginal, controlGraph) || !reflect.DeepEqual(controlProject.AnalysisExpressions, controlAnalysis) || !reflect.DeepEqual(control.Dependencies(), controlManifest) {
			t.Fatal("stored control preflight/admission mutated original", name)
		}
	}
	if !reflect.DeepEqual(original, graph) || !reflect.DeepEqual(originalProject.AnalysisExpressions, analysisGraph) || !reflect.DeepEqual(definition.Dependencies(), manifest) || !reflect.DeepEqual(*selected, declaration) {
		t.Fatal("stored source preparation mutated original graph/declarations/manifests")
	}
	arrayStrictNullUncalled(t, e)
}

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

var arrayStrictNullPlanners = []struct {
	name string
	run  func(*exec.Context, expr.Expr, bool) (expr.Expr, bool, error)
}{{"preflight", exec.PreflightCoercionsWithPolicy}, {"conditional", exec.SimplifyConditional}}

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

// Bind actual supported providers under genuine catalog/statement authority.
// SQL creates incidental state; assertions exercise private planning and original
// typed frames, not public SQL response witnesses or synthetic probe providers.
func strictNullUnitFixture(t *testing.T) (*core.Engine, *session.Operation, *core.StatementScope) {
	t.Helper()
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	t.Cleanup(done)
	for _, query := range []string{
		"CREATE TABLE strict_null_rows(v integer)",
		"CREATE SEQUENCE strict_null_steps START 1",
		"CREATE DOMAIN strict_null_checked AS integer CONSTRAINT strict_null_present CHECK(VALUE IS NOT NULL)",
	} {
		enginetest.RoleExecute(t, e, op, query)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { scope.Finish(nil) })
	result, err := scope.Execute(enginetest.Statement(t, "SELECT 1"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != nil {
		result.Rows.Close()
	}
	return e, op, scope
}