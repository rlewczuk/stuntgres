package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/sepgsql"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

// These are real catalog/SQL ACL/MAC grants from retained analyzed expressions.
// In particular no test installs receipts, observations, or fabricated routines.
func sepgsqlInitializationEngine(t *testing.T) *core.Engine {
	t.Helper()
	e := engine.New()
	enginetest.Run(t, e, "CREATE EXTENSION hstore")
	runtime, err := sepgsql.Compile(sepgsql.SimulationConfig{InstallSQL: true, Policy: sepgsql.Policy{
		ID: "initialization-owner", Version: "1", Contexts: []string{"A", "P", "D"}, InitialContext: "A", UnlabeledContext: "P",
		Rules: []sepgsql.Rule{{Source: "A", Target: "*", Class: "*", Allow: []string{"*"}}, {Source: "A", Target: "D", Class: "db_procedure", Deny: []string{"execute"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	err = e.InstallSepgsqlSimulation(runtime, true)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	enginetest.Run(t, e, "CREATE TABLE init_owner_rows(v integer)")
	enginetest.Run(t, e, "CREATE VIEW init_owner_recipe AS SELECT abs(v) AS v FROM init_owner_rows")
	return e
}

func sepgsqlInitializationBinding(t *testing.T, e *core.Engine, sql string, setupSQL ...string) (*session.Operation, *core.StatementScope, *plan.ProjectNode, *exec.Context) {
	t.Helper()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	t.Cleanup(done)
	for _, setup := range setupSQL {
		enginetest.RoleExecute(t, e, op, setup)
	}
	return sepgsqlInitializationBindOperation(t, e, op, sql)
}

func sepgsqlInitializationBindOperation(t *testing.T, e *core.Engine, op *session.Operation, sql string) (*session.Operation, *core.StatementScope, *plan.ProjectNode, *exec.Context) {
	t.Helper()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { scope.Finish(nil) })
	raw := enginetest.Statement(t, sql)
	if err = scope.Gate(raw); err != nil {
		t.Fatal(err)
	}
	// Borrow the real statement's locked literal-input boundary, as execution
	// does before binding. Type input may unlock and readmit this same owner.
	scope.Input.LockedDomainInput, scope.Input.DomainChecks = scope, scope
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	var query *plan.Query
	if err == nil {
		binding, failure := core.Describe(op.Context(), e.AnalysisHost(op), raw, auth, ids.ObjectRef{}, scope.Input, nil, nil)
		err = failure
		query = binding.Query
		scope.RoutineAuth, scope.RoutineDependencies = auth, binding.Dependencies
		if err == nil {
			// Query-source preparation captures this same immutable catalog/role
			// pair before any retained inquiry kernel can run.
			err = e.CaptureCommentSnapshotLocked(scope)
		}
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Host: core.HostOf(scope), Catalog: auth.Catalog, Input: scope.Input, Output: scope.Output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: query.Root.(*plan.ProjectNode).Frame}}
	if err = exec.BeginRoutineInitialization(evaluation); err != nil {
		t.Fatal(err)
	}
	return op, scope, query.Root.(*plan.ProjectNode), evaluation
}

func sepgsqlInitializationSite(t *testing.T, evaluation *exec.Context, node expr.Expr) exec.RoutineInitializationSite {
	t.Helper()
	p, err := exec.ExpressionRoutineDescriptor(node)
	if err != nil || p == nil {
		t.Fatal("missing genuinely selected routine", err)
	}
	info := expr.InfoOf(node)
	return exec.RoutineInitializationSite{Reference: evaluation.Catalog.Domain().Ref(evaluation.Catalog.Epoch(), p.Address()), Routine: p, Purpose: catalog.DefinitionDependency, Phase: evaluation.InitializationPhase, Occurrence: info.Occurrence, Source: info.Source}
}

func TestSepgsqlSimulationInitialization(t *testing.T) {
	t.Run("exact_source_and_same_command_nonce", func(t *testing.T) {
		e := sepgsqlInitializationEngine(t)
		_, scope, project, evaluation := sepgsqlInitializationBinding(t, e, "SELECT abs(v),abs(v),abs(v) FROM init_owner_rows")
		node := project.Expressions[0]
		if err := exec.AdmitExpression(evaluation, node, evaluation.Catalog); err != nil {
			t.Fatal(err)
		}
		host := core.HostOf(scope)
		owner := evaluation.RoutineInitialization
		site := sepgsqlInitializationSite(t, evaluation, node)
		if _, err := host.ValidateRoutineInitialization(owner, site); err != nil {
			t.Fatal("real initialized source rejected", err)
		}
		other, err := host.NewRoutineInitialization()
		if err != nil {
			t.Fatal(err)
		}
		command := ids.CommandIdentity{Transaction: scope.TxRef, Command: scope.CommandID}
		if command.Command != 0 || !owner.Active() {
			t.Fatal("genuine first command lost initialization ownership", command.Command, owner.Active())
		}
		if owner == other || owner.Instance() == other.Instance() || !owner.Matches(command) || !other.Matches(command) {
			t.Fatal("same numeric command did not mint distinct actual frame instances")
		}
		for name, candidate := range map[string]exec.RoutineInitialization{"zero": {}, "other_actual_frame": other, "same_numbers_without_nonce": exec.NewRoutineInitialization(command, 0)} {
			if _, err := host.ValidateRoutineInitialization(candidate, site); err == nil {
				t.Fatal(name, "adopted another frame receipt")
			}
		}
		wrongOccurrence := sepgsqlInitializationSite(t, evaluation, project.Expressions[1])
		wrongRef := site
		wrongRef.Reference = evaluation.Catalog.Domain().Ref(evaluation.Catalog.Epoch(), ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 1258})
		wrongPhase := site
		wrongPhase.Phase = exec.TriggerWhenInitialization
		wrongBody := site
		wrongBody.Body = exec.RoutineBodyContext{Outer: site.Routine, Statement: true}
		relation := evaluation.Catalog.MustRelation(enginetest.CatalogRef(t, e, evaluation.Catalog, "init_owner_recipe").Address)
		wrongRecipe := site
		wrongRecipe.Recipe = relation
		wrongConsumer := site
		wrongConsumer.Consumer = relation
		wrongPurpose := site
		wrongPurpose.Purpose = catalog.AggregateSupportDependency
		for name, candidate := range map[string]exec.RoutineInitializationSite{"occurrence": wrongOccurrence, "reference": wrongRef, "phase": wrongPhase, "body": wrongBody, "recipe": wrongRecipe, "consumer": wrongConsumer, "purpose": wrongPurpose} {
			if _, err := host.ValidateRoutineInitialization(owner, candidate); err == nil {
				t.Fatal(name, "adopted another selected source receipt")
			}
		}
		foreignEngine := sepgsqlInitializationEngine(t)
		_, foreignScope, _, foreign := sepgsqlInitializationBinding(t, foreignEngine, "SELECT abs(v) FROM init_owner_rows")
		if _, err := core.HostOf(foreignScope).ValidateRoutineInitialization(owner, site); err == nil {
			t.Fatal("foreign engine accepted same numeric SQL identity")
		}
		if _, err := host.ValidateRoutineInitialization(foreign.RoutineInitialization, site); err == nil {
			t.Fatal("foreign owner accepted by original scope")
		}
		value, err := sqltype.Integer(node.ResultUsage().Type, -9)
		if err != nil {
			t.Fatal(err)
		}
		evaluation.Row = []sqltype.Value{value}
		for range 3 {
			result, err := node.Eval(evaluation)
			if err != nil {
				t.Fatal("same ExprState row reuse", err)
			}
			got, err := result.Integer()
			if err != nil || got != 9 {
				t.Fatal("real repeated kernel", got, err)
			}
		}
		lost := *evaluation
		lost.RoutineInitialization = exec.RoutineInitialization{}
		if _, err := node.Eval(&lost); err == nil {
			t.Fatal("copied context silently minted a missing owner")
		}
		lost = *evaluation
		lost.Catalog = catalog.Read{}
		if _, err := node.Eval(&lost); err == nil {
			t.Fatal("copied context silently adopted missing catalog authority")
		}
		for name, host := range map[string]exec.Host{
			"missing_host":        nil,
			"foreign_host":        core.HostOf(foreignScope),
			"inactive_style_host": core.HostOf(&core.StatementScope{RoutineAuth: scope.RoutineAuth}),
		} {
			local := *evaluation
			local.Host = host
			local.RoutineAuthority = &scope.RoutineAuth
			if _, err := node.Eval(&local); err == nil {
				t.Fatal(name, "rescued an active invocation without its borrowed owner")
			}
			if _, _, err := exec.RoutineInvocationAuthority(&local); err == nil {
				t.Fatal(name, "supplied active invocation authority")
			}
		}
	})
	t.Run("real_deduplicated_grants_nested_rollback", func(t *testing.T) {
		e := sepgsqlInitializationEngine(t)
		_, scope, project, evaluation := sepgsqlInitializationBinding(t, e, "SELECT abs(v),abs(v),abs(v) FROM init_owner_rows")
		host := core.HostOf(scope)
		owner := evaluation.RoutineInitialization
		initialize := func(i int) {
			t.Helper()
			if err := exec.AdmitExpression(evaluation, project.Expressions[i], evaluation.Catalog); err != nil {
				t.Fatal(err)
			}
		}
		initialize(0)
		a := sepgsqlInitializationSite(t, evaluation, project.Expressions[0])
		outer := scope.MarkSepgsqlObservations()
		if outer.Observations == 0 || outer.Initializations == 0 {
			t.Fatal("genuine grant produced no command evidence", outer)
		}
		initialize(1)
		b := sepgsqlInitializationSite(t, evaluation, project.Expressions[1])
		inner := scope.MarkSepgsqlObservations()
		if inner.Observations != outer.Observations || inner.Initializations <= outer.Initializations {
			t.Fatal("equal genuine observation did not distinguish completion age", outer, inner)
		}
		initialize(0)
		if scope.MarkSepgsqlObservations() != inner {
			t.Fatal("row reuse minted another grant")
		}
		initialize(2)
		c := sepgsqlInitializationSite(t, evaluation, project.Expressions[2])
		newest := scope.MarkSepgsqlObservations()
		scope.RestoreSepgsqlObservations(inner)
		if _, err := host.ValidateRoutineInitialization(owner, b); err != nil {
			t.Fatal("inner rollback removed older B", err)
		}
		if _, err := host.ValidateRoutineInitialization(owner, c); err == nil {
			t.Fatal("inner rollback retained C")
		}
		scope.RestoreSepgsqlObservations(outer)
		if _, err := host.ValidateRoutineInitialization(owner, a); err != nil {
			t.Fatal("dedup rollback removed earlier reused A", err)
		}
		if _, err := host.ValidateRoutineInitialization(owner, b); err == nil {
			t.Fatal("dedup rollback retained failed-action B")
		}
		initialize(2)
		if scope.MarkSepgsqlObservations().Initializations <= newest.Initializations {
			t.Fatal("rollback rewound genuine completion sequence")
		}
		if _, err := host.ValidateRoutineInitialization(owner, b); err == nil {
			t.Fatal("later actual C resurrected B")
		}
	})
	t.Run("generated_earth_check_occurrences", func(t *testing.T) {
		e := sepgsqlInitializationEngine(t)
		enginetest.Run(t, e, "CREATE EXTENSION cube")
		enginetest.Run(t, e, "CREATE EXTENSION earthdistance")
		enginetest.Run(t, e, "CREATE TABLE init_earth_check(v earth)")
		enginetest.Run(t, e, "INSERT INTO init_earth_check VALUES(ll_to_earth(0,0))")
		found := false
		for _, descriptor := range e.CatalogRead().Descriptors() {
			d, ok := descriptor.(*catalog.Type)
			if !ok || d.Name() != "earth" {
				continue
			}
			found = true
			if len(d.DomainChecks) != 3 {
				t.Fatal("lost actual installed domain CHECKs")
			}
			for _, check := range d.DomainChecks {
				info := expr.InfoOf(check.Expression)
				if info.Occurrence == 0 || info.Source.Frame == 0 || info.RoutineReference == nil {
					t.Fatal("generated CHECK lost original selected source", check.Name, info)
				}
				if node, ok := check.Expression.(plan.EarthCheckExpr); !ok || !node.Valid() {
					t.Fatal("generated CHECK lost immutable operands", check.Name)
				} else if node.Kind == sqltype.EarthOnSurface && !node.Origin.Value.IsEarthCubeOrigin(node.Value.Usage.Type) {
					t.Fatal("surface CHECK lost its actual origin literal")
				}
			}
		}
		if !found {
			t.Fatal("missing actual installed earth domain")
		}
	})
	t.Run("derived_body_phase_and_original_receipt", func(t *testing.T) {
		e := sepgsqlInitializationEngine(t)
		enginetest.Run(t, e, "CREATE EXTENSION cube")
		enginetest.Run(t, e, "CREATE EXTENSION earthdistance")
		_, scope, project, evaluation := sepgsqlInitializationBinding(t, e, "SELECT earth_box(NULL::earth,v::double precision),abs(v) FROM init_owner_rows")
		node := project.Expressions[0]
		if err := exec.AdmitExpression(evaluation, node, evaluation.Catalog); err != nil {
			t.Fatal(err)
		}
		site := sepgsqlInitializationSite(t, evaluation, node)
		call := exec.RoutineInitializationCall{Owner: evaluation.RoutineInitialization, Site: site}
		host := core.HostOf(scope)
		entered, err := host.NativeBodyRoutineInitialization(call, exec.NativeBodyInitialization)
		if err != nil {
			t.Fatal(err)
		}
		expanded, err := host.NativeBodyRoutineInitialization(call, exec.ExpandedNativeDeclarationInitialization)
		if err != nil {
			t.Fatal(err)
		}
		if entered == expanded || entered == call.Owner || expanded == call.Owner {
			t.Fatal("derived body phases alias original or each other")
		}
		again, err := host.NativeBodyRoutineInitialization(call, exec.NativeBodyInitialization)
		if err != nil || again != entered {
			t.Fatal("body rows did not retain actual phase owner", err)
		}
		if _, err = host.NativeBodyRoutineInitialization(call, exec.DomainCheckInitialization); err == nil {
			t.Fatal("unrelated phase derived a native body")
		}
		body, err := exec.BeginExpandedNativeDeclaration(evaluation, site.Routine, expr.InfoOf(node))
		if err != nil {
			t.Fatal("actual expanded declaration preorder", err)
		}
		if body.RoutineInitialization != expanded || body.InitializationPhase != exec.ExpandedNativeDeclarationInitialization {
			t.Fatal("expanded declaration lost its distinct retained owner")
		}
		if scope.Op.Session.Sepgsql.Current() != "A" {
			t.Fatal("expanded initialization entered an outer subject")
		}
		recipe, ok := proc.RoutineDeclarationRecipe(site.Routine.DeclarationBody)
		if !ok || recipe.CallCount() == 0 {
			t.Fatal("missing genuine expanded CallPart recipe")
		}
		for ordinal := range recipe.CallCount() {
			inner, info, err := exec.NativeDeclarationCall(&body, site.Routine, expr.InfoOf(node), ordinal)
			if err != nil {
				t.Fatal(err)
			}
			selected := exec.RoutineInitializationSite{Reference: body.Catalog.Domain().Ref(body.Catalog.Epoch(), inner.Address()), Routine: inner, Body: body.RoutineBodyContext, Purpose: catalog.DefinitionDependency, Phase: body.InitializationPhase, Occurrence: info.Occurrence, Source: info.Source}
			if _, err = host.ValidateRoutineInitialization(expanded, selected); err != nil {
				t.Fatal("expanded CallPart was not genuinely initialized", ordinal, err)
			}
			if _, err = host.ValidateRoutineInitialization(entered, selected); err == nil {
				t.Fatal("expanded CallPart adopted entered-body owner")
			}
		}
		unrelated, err := exec.ExpressionRoutineDescriptor(project.Expressions[1])
		if err != nil || unrelated == nil {
			t.Fatal(err)
		}
		if err = exec.InitializeNativeBodyCall(&body, unrelated, expr.InfoOf(project.Expressions[1])); err == nil {
			t.Fatal("native child borrowed unrelated caller manifest member")
		}
		inner, innerInfo, err := exec.NativeDeclarationCall(&body, site.Routine, expr.InfoOf(node), 0)
		if err != nil {
			t.Fatal(err)
		}
		wrongInfo := innerInfo
		wrongInfo.Occurrence = 0
		if err = exec.InitializeNativeBodyCall(&body, inner, wrongInfo); err == nil {
			t.Fatal("canonical child identity replaced its genuine CallPart ordinal")
		}
		lostBody := body
		lostBody.RoutineInitialization = exec.RoutineInitialization{}
		if err = exec.InitializeNativeBodyCall(&lostBody, inner, innerInfo); err == nil {
			t.Fatal("copied native body reminted a lost derived owner")
		}
		var consumed exec.RoutineProgram
		body.RoutineProgram = &consumed
		if err = exec.InitializeNativeBodyCall(&body, inner, innerInfo); err != nil {
			t.Fatal("genuine consumed child source", err)
		}
		if len(consumed.Calls) != 1 {
			t.Fatal("missing exact consumed native source", len(consumed.Calls))
		}
		source := reflect.ValueOf(consumed.Calls[0]).FieldByName("nativeSource")
		for _, field := range []string{"owner", "parent", "defaultCall"} {
			if source.FieldByName(field).IsValid() {
				t.Fatal("consumed native source retained runtime authority", field)
			}
		}
		assertInertInitializationPayload(t, consumed)
		auth, _, err := exec.RoutineInvocationAuthority(&body)
		if err != nil {
			t.Fatal(err)
		}
		mark := scope.MarkSepgsqlObservations()
		if err = consumed.Readmit(auth, exec.ConsumedGraphRevalidation); err != nil {
			t.Fatal("genuine native source final lifetime/SQL ACL readmission", err)
		}
		if scope.MarkSepgsqlObservations() != mark {
			t.Fatal("consumed native source replayed execute initialization")
		}
		missing := *evaluation
		missing.RoutineInitialization = exec.RoutineInitialization{}
		if _, err = exec.BeginExpandedNativeDeclaration(&missing, site.Routine, expr.InfoOf(node)); err == nil {
			t.Fatal("expanded declaration minted missing original receipt")
		}
		missing = *evaluation
		missing.Catalog = catalog.Read{}
		before := scope.MarkSepgsqlObservations()
		if _, err = exec.BeginExpandedNativeDeclaration(&missing, site.Routine, expr.InfoOf(node)); err == nil {
			t.Fatal("expanded declaration adopted lost caller catalog authority")
		}
		if scope.MarkSepgsqlObservations() != before {
			t.Fatal("rejected copied expanded context minted initialization")
		}
		call.Site.Phase = exec.ExpandedNativeDeclarationInitialization
		if _, err = host.NativeBodyRoutineInitialization(call, exec.NativeBodyInitialization); err == nil {
			t.Fatal("derived phase forged an original receipt")
		}
	})
	t.Run("cancellation_and_finish_retire_authority", func(t *testing.T) {
		e := sepgsqlInitializationEngine(t)
		op, scope, project, evaluation := sepgsqlInitializationBinding(t, e, "SELECT abs(v) FROM init_owner_rows")
		if err := exec.AdmitExpression(evaluation, project.Expressions[0], evaluation.Catalog); err != nil {
			t.Fatal(err)
		}
		site := sepgsqlInitializationSite(t, evaluation, project.Expressions[0])
		host := core.HostOf(scope)
		canceled, cancel := context.WithCancel(op.Context())
		cancel()
		saved := op.Ctx
		op.Ctx = canceled
		_, err := host.ValidateRoutineInitialization(evaluation.RoutineInitialization, site)
		op.Ctx = saved
		if !errors.Is(err, context.Canceled) {
			t.Fatal("canceled receipt remained invocable", err)
		}
		if err = scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
		if _, err = host.ValidateRoutineInitialization(evaluation.RoutineInitialization, site); err == nil {
			t.Fatal("finished command retained receipt authority")
		}
	})
}

func TestSepgsqlSimulationInitializationClosed(t *testing.T) {
	e := sepgsqlInitializationEngine(t)
	_, scope, project, evaluation := sepgsqlInitializationBinding(t, e, "SELECT abs(v),abs(v) FROM init_owner_rows")
	node := project.Expressions[0]
	if err := exec.AdmitExpression(evaluation, node, evaluation.Catalog); err != nil {
		t.Fatal(err)
	}
	site := sepgsqlInitializationSite(t, evaluation, node)
	owner := evaluation.RoutineInitialization
	proof, err := core.HostOf(scope).SealRoutineInitialization(owner, evaluation.Catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err = proof.Check(owner, site, evaluation.Catalog); err != nil {
		t.Fatal("genuine sealed coordinator proof", err)
	}
	other, err := core.HostOf(scope).NewRoutineInitialization()
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []exec.RoutineInitialization{{}, other} {
		if err = proof.Check(candidate, site, evaluation.Catalog); err == nil {
			t.Fatal("closed proof adopted unrelated owner")
		}
	}
	wrong := sepgsqlInitializationSite(t, evaluation, project.Expressions[1])
	if err = proof.Check(owner, wrong, evaluation.Catalog); err == nil {
		t.Fatal("closed proof adopted uninitialized occurrence")
	}
	if err = (exec.ClosedRoutineInitialization{}).Check(owner, site, evaluation.Catalog); err == nil {
		t.Fatal("empty closed proof gained current live authority")
	}
	assertInertInitializationPayload(t, owner)
	assertInertInitializationPayload(t, proof)
	closed := *evaluation
	closed.Host = nil
	closed.ClosedEvaluation = &exec.EvaluationCapture{Routines: proof}
	value, err := sqltype.Integer(node.ResultUsage().Type, -4)
	if err != nil {
		t.Fatal(err)
	}
	closed.Row = []sqltype.Value{value}
	for range 3 {
		result, err := node.Eval(&closed)
		if err != nil {
			t.Fatal("closed worker borrowed live Host", err)
		}
		got, err := result.Integer()
		if err != nil || got != 4 {
			t.Fatal(got, err)
		}
	}
	// A genuine sealed proof remains an active source even if its local owner
	// and catalog are both lost. It cannot degrade to an inactive callback lane.
	foreignEngine := sepgsqlInitializationEngine(t)
	for name, candidate := range map[string]exec.State{
		"lost_owner":   {Outer: &closed, ClosedEvaluation: closed.ClosedEvaluation},
		"lost_catalog": {RoutineInitialization: owner, ClosedEvaluation: closed.ClosedEvaluation},
		"lost_both":    {ClosedEvaluation: closed.ClosedEvaluation},
		"foreign_catalog": {
			RoutineInitialization: owner, ClosedEvaluation: closed.ClosedEvaluation,
			Outer: &exec.Context{EvalStatement: exec.EvalStatement{Catalog: foreignEngine.CatalogRead()}},
		},
	} {
		frame := candidate.Evaluation(evaluation.Context)
		frame.Row = []sqltype.Value{value}
		if _, err := node.Eval(&frame); err == nil {
			t.Fatal(name, "closed source adopted lost or foreign authority")
		}
	}
	inactive := exec.State{ClosedEvaluation: &exec.EvaluationCapture{}}
	inactiveFrame := inactive.Evaluation(evaluation.Context)
	if inactiveFrame.FunctionInput().IndexSupport != nil {
		t.Fatal("inactive empty capture acquired a simulation callback boundary")
	}
	// Worker state copies only the explicit inert owner. Repeated pulls cannot
	// initialize or recover a missing owner from its live lexical source.
	source := &exec.Source{Host: core.HostOf(scope), Authority: &exec.Authority{RoutineInitialization: owner}}
	state := exec.State{Source: source, Outer: evaluation}
	for range 3 {
		copy := state.Evaluation(evaluation.Context)
		if copy.RoutineInitialization != owner {
			t.Fatal("state pull lost explicit owner")
		}
	}
	source.Authority = nil
	copy := state.Evaluation(evaluation.Context)
	if copy.RoutineInitialization.Active() {
		t.Fatal("state pull adopted caller owner without transfer")
	}
	if err = exec.BeginRoutineInitialization(&copy); err == nil {
		t.Fatal("row pull minted missing retained initializer")
	}
}

// Inspect actual nonzero payloads, including private fields, without unsafe or
// test-only exports. Catalog/role immutable roots are allowed; live owner
// attachments and scopes are not. Zero TEMP tokens in immutable metadata do not
// retain any attachment and are deliberately skipped.
func assertInertInitializationPayload(t *testing.T, payload any) {
	t.Helper()
	seen := make(map[struct {
		typ reflect.Type
		ptr uintptr
	}]bool)
	var visit func(reflect.Value)
	visit = func(value reflect.Value) {
		if !value.IsValid() {
			return
		}
		switch value.Kind() {
		case reflect.Interface:
			if !value.IsNil() {
				if value.Type() == reflect.TypeFor[ids.TxOwner]() || value.Type() == reflect.TypeFor[ids.SessionOwner]() {
					t.Fatalf("closed proof retains live attachment %s", value.Type())
				}
				visit(value.Elem())
			}
		case reflect.Pointer:
			if value.IsNil() {
				return
			}
			typ := value.Type()
			if typ == reflect.TypeOf((*core.StatementScope)(nil)) || typ == reflect.TypeOf((*session.Session)(nil)) {
				t.Fatalf("closed proof retains live attachment %s", typ)
			}
			key := struct {
				typ reflect.Type
				ptr uintptr
			}{typ, value.Pointer()}
			if seen[key] {
				return
			}
			seen[key] = true
			visit(value.Elem())
		case reflect.Struct:
			for i := range value.NumField() {
				visit(value.Field(i))
			}
		case reflect.Array, reflect.Slice:
			for i := range value.Len() {
				visit(value.Index(i))
			}
		case reflect.Map:
			if value.IsNil() {
				return
			}
			iterator := value.MapRange()
			for iterator.Next() {
				visit(iterator.Key())
				visit(iterator.Value())
			}
		}
	}
	visit(reflect.ValueOf(payload))
}
