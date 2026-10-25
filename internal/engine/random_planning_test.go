package engine

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func randomPlanningUnitDraw(t *testing.T, op *session.Operation) float64 {
	t.Helper()
	rng, err := op.Session.Random.Borrow()
	if err != nil {
		t.Fatal(err)
	}
	defer op.Session.Random.Release()
	return rng.Float64()
}

func randomPlanningUnitDependency(t *testing.T, e *Engine, dependencies []catalog.ExpressionDependency, name string) catalog.ExpressionDependency {
	t.Helper()
	for _, dependency := range dependencies {
		if dependency.Ref.Address.ClassID == ids.ProcedureClassID && e.CatalogRead().MustProcedure(dependency.Ref.Address).Name() == name {
			return dependency
		}
	}
	t.Fatal("missing original routine dependency", name)
	return catalog.ExpressionDependency{}
}

func TestRandomBinderRetainsStrictNullChildren(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	op.Session.Random.SeedWords(1, 2)
	first := randomPlanningUnitDraw(t, op)
	op.Session.Random.SeedWords(1, 2)
	for _, tc := range []struct {
		expression, signature string
		oid                   ids.OID
		children              int
	}{
		{"random(NULL::integer,1/0)", "23,23", 6339, 2},
		{"random(NULL::bigint,(1/0)::bigint)", "20,20", 6340, 2},
		{"random(NULL::numeric,(1/0)::numeric)", "1700,1700", 6341, 2},
		{"random(NULL::integer,abs(-1::integer))", "23,23", 6339, 2},
		{"random(NULL::integer,random(1,10))", "23,23", 6339, 2},
		{"random(NULL::integer,abs($1::integer))", "23,23", 6339, 2},
		{"random(NULL::integer,1/v)", "23,23", 6339, 2},
		{"random_normal()", "701,701", 6212, 0},
		{"random_normal(NULL::double precision)", "701,701", 6212, 1},
		{"random_normal(NULL::double precision,random())", "701,701", 6212, 2},
	} {
		t.Run(tc.expression, func(t *testing.T) {
			node, binder, evaluation := strictNullUnitBind(t, e, op, scope, tc.expression)
			call, ok := node.(functions.Random)
			if !ok || len(call.Args) != tc.children || call.Properties().Volatility != expr.Volatile {
				t.Fatalf("binding discarded genuine selected Random/children: %#v", node)
			}
			if tc.expression == "random(NULL::integer,abs($1::integer))" && !call.Properties().ParameterDependent {
				t.Fatal("binding discarded parameter dependence")
			}
			if tc.expression == "random(NULL::integer,1/v)" && !call.Properties().RowDependent {
				t.Fatal("binding discarded row dependence")
			}
			captured, err := exec.ExpressionRoutineDescriptor(call)
			address := ids.ObjectAddress{ids.ProcedureClassID, tc.oid, 0}
			if err != nil || captured == nil || captured.Address() != address || string(captured.Signature) != tc.signature || captured.Builtin == nil || !captured.Strict || captured.Volatility != expr.Volatile {
				t.Fatal("full selected strict volatile declaration", captured, err)
			}
			if call.CallRoutineReference() == nil || evaluation.Catalog.Get(address) != captured {
				t.Fatal("lost selected reference/catalog ownership")
			}
			found := false
			for _, dependency := range binder.Dependencies {
				if dependency.Ref.Address == address {
					found = dependency.Ref.Domain() == e.catalog.Domain && dependency.Revision == captured.Revision()
				}
			}
			if !found {
				t.Fatal("missing original selected dependency")
			}
			if tc.expression == "random(NULL::integer,abs(-1::integer))" {
				randomPlanningUnitDependency(t, e, binder.Dependencies, "abs")
			}
		})
	}
	if got := randomPlanningUnitDraw(t, op); got != first {
		t.Fatal("binding consumed the session stream", got, first)
	}
}

func TestRandomStrictNullPlanning(t *testing.T) {
	e, op, scope := jsonbRecordRoutineUnitFixture(t)
	// The fixture deliberately retains statement admission for private hosts.
	// Finish it before issuing setup SQL on the same session, then borrow a
	// fresh statement host that includes the newly committed sequence.
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "CREATE SEQUENCE random_planning_steps START 101")
	var err error
	scope, err = e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := scope.Finish(nil); err != nil {
			t.Error(err)
		}
	})
	result, err := scope.Execute(statement(t, "SELECT 1"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != nil {
		if err := result.Rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	readerOp, done := roleOperation(t, e, "jsonb_authority_reader")
	defer done()
	op.Session.Random.SeedWords(1, 2)
	first := randomPlanningUnitDraw(t, op)
	op.Session.Random.SeedWords(1, 2)
	for _, expression := range []string{
		"random(NULL::integer,1/0)",
		"random(1/0,NULL::integer)",
		"random(NULL::bigint,(1/0)::bigint)",
		"random(NULL::numeric,(1/0)::numeric)",
		"random_normal(NULL::double precision,1/0)",
		"random_normal(1/0,NULL::double precision)",
	} {
		for _, planner := range []struct {
			name string
			run  func(*exec.Context, expr.Expr) (expr.Expr, bool, error)
		}{
			{"ordinary_preflight", exec.PreflightCoercions},
			{"decisive_preflight", func(c *exec.Context, node expr.Expr) (expr.Expr, bool, error) {
				return exec.PreflightCoercionsWithPolicy(c, node, true)
			}},
			{"conditional", func(c *exec.Context, node expr.Expr) (expr.Expr, bool, error) {
				return exec.SimplifyConditional(c, node, true)
			}},
		} {
			t.Run(expression+"/"+planner.name, func(t *testing.T) {
				node, _, evaluation := strictNullUnitBind(t, e, op, scope, expression)
				_, _, err := planner.run(&evaluation, node)
				nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "22012", Message: "division by zero"})
			})
		}
	}
	for _, expression := range []string{
		"random(NULL::integer,random(1,10))",
		"random(NULL::integer,abs($1::integer))",
		"random(NULL::integer,1/v)",
		"random(NULL::bigint,1::bigint)",
		"random(NULL::numeric,1::numeric)",
		"random_normal(NULL::double precision)",
		"random_normal(NULL::double precision,random())",
		"random(NULL::bigint,nextval('random_planning_steps'))",
	} {
		node, binder, evaluation := strictNullUnitBind(t, e, op, scope, expression)
		before := append([]catalog.ExpressionDependency(nil), binder.Dependencies...)
		if expression == "random(NULL::integer,abs($1::integer))" {
			input, err := sqltype.Integer(binder.Params.Parameters[1].Type, -1)
			if err != nil {
				t.Fatal(err)
			}
			evaluation.Parameters = []sqltype.Value{input}
		}
		preflight, preflightChanged, err := exec.PreflightCoercionsWithPolicy(&evaluation, node, true)
		if err != nil {
			t.Fatal("child preflight", expression, err)
		}
		if !preflightChanged || !exec.RandomLiteralNull(preflight) {
			t.Fatal("ordinary preflight retained a runtime sibling", expression, preflight)
		}
		value, err := preflight.Eval(&evaluation)
		if err != nil || !value.IsNull() {
			t.Fatal("preflight executable result", expression, value, err)
		}
		planned, changed, err := exec.SimplifyConditional(&evaluation, node, true)
		if err != nil || !changed {
			t.Fatal("missing post-child elimination", expression, changed, err)
		}
		literal, ok := planned.(plan.LiteralExpr)
		if !ok || !literal.Value.IsNull() || literal.Info != (expr.Info{Usage: node.ResultUsage(), Pos: node.SourcePosition()}) {
			t.Fatal("expected bare typed NULL with only usage/position", planned)
		}
		if descriptor, err := exec.ExpressionRoutineDescriptor(planned); err != nil || descriptor != nil {
			t.Fatal("erased invocation retained a callable carrier", descriptor, err)
		}
		if !reflect.DeepEqual(before, binder.Dependencies) || node.Properties().Volatility != expr.Volatile {
			t.Fatal("planning mutated analyzed manifest/properties")
		}
	}
	sequence := catalogRef(t, e, e.CatalogRead(), "random_planning_steps")
	e.mu.Lock()
	called := e.storage.Sequences[sequence.Address].Called
	e.mu.Unlock()
	if called {
		t.Fatal("post-preflight executable invoked discarded sequence sibling")
	}
	// Non-NULL volatile calls remain executable; planning must not sample them.
	for _, expression := range []string{"random()", "random(1,10)", "random_normal()", "setseed(0.25)"} {
		node, _, evaluation := strictNullUnitBind(t, e, op, scope, expression)
		planned, _, err := exec.SimplifyConditional(&evaluation, node, true)
		if _, ok := planned.(functions.Random); err != nil || !ok {
			t.Fatal("non-NULL volatile call was computed", expression, planned, err)
		}
	}
	if got := randomPlanningUnitDraw(t, op); got != first {
		t.Fatal("planning consumed or reseeded the session stream", got, first)
	}
	// Child permission uses the current actor even though the superuser bound it.
	node, binder, evaluation := strictNullUnitBind(t, e, op, scope, "random(NULL::integer,abs(-1::integer))")
	child := randomPlanningUnitDependency(t, e, binder.Dependencies, "abs")
	if err := e.SetObjectACL(context.Background(), child.Ref, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	evaluation.RoutineAuthority = eMustJSONBRoutineAuthority(t, e, readerOp)
	_, _, err = exec.SimplifyConditional(&evaluation, node, true)
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for function abs"})
	reader := e.roles.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("jsonb_authority_reader")
	owner := e.CatalogRead().MustProcedure(child.Ref.Address).Owner
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{objectACLUnitEntry(owner, reader, authorization.ExecutePrivilege, 0)}}
	if err := e.SetObjectACL(context.Background(), child.Ref, acl); err != nil {
		t.Fatal(err)
	}
	evaluation.RoutineAuthority = eMustJSONBRoutineAuthority(t, e, readerOp)
	planned, _, err := exec.SimplifyConditional(&evaluation, node, true)
	if err != nil || !exec.RandomLiteralNull(planned) {
		t.Fatal("current actor grant did not recover original tree", planned, err)
	}
	if err := e.SetObjectACL(context.Background(), child.Ref, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	evaluation.RoutineAuthority = eMustJSONBRoutineAuthority(t, e, readerOp)
	_, _, err = exec.SimplifyConditional(&evaluation, node, true)
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for function abs"})
}

func TestRandomStrictNullOriginalManifest(t *testing.T) {
	e, op, scope := jsonbRecordRoutineUnitFixture(t)
	readerOp, done := roleOperation(t, e, "jsonb_authority_reader")
	defer done()
	node, binder, evaluation := strictNullUnitBind(t, e, op, scope, "random(NULL::integer,1)")
	call := node.(functions.Random)
	before := call
	before.Args = append([]expr.Expr(nil), call.Args...)
	original := append([]catalog.ExpressionDependency(nil), binder.Dependencies...)
	captured, err := exec.ExpressionRoutineDescriptor(node)
	if err != nil || captured == nil {
		t.Fatal("missing original Random declaration", err)
	}
	outer := randomPlanningUnitDependency(t, e, original, "random")
	if err := e.SetObjectACL(context.Background(), outer.Ref, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	// Actual statement-host source lane, independent of command-private erasure.
	evaluation.RoutineAuthority = nil
	evaluation.RoutineRecipe = false
	evaluation.RoutineChecks = nil
	scope.routineAuth = *eMustJSONBRoutineAuthority(t, e, readerOp)
	scope.routineDependencies = original
	if err := exec.AdmitRoutineCall(&evaluation, node, false, false); err != nil {
		t.Fatal("erased strict call acquired EXECUTE demand", err)
	}
	planned, _, err := exec.SimplifyConditional(&evaluation, node, true)
	if err != nil || !exec.RandomLiteralNull(planned) || !reflect.DeepEqual(node, before) || !reflect.DeepEqual(binder.Dependencies, original) {
		t.Fatal("command erasure mutated original source/manifest", planned, err)
	}
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	target := e.roles.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("jsonb_authority_owner")
	if err := role.StageOwnership(stage, base.MustProcedure(captured.Address()), target); err != nil {
		t.Fatal(err)
	}
	ownershipUnitPublish(t, e, stage)
	e.DiscardCatalogStage(stage)
	if err := exec.AdmitRoutineCall(&evaluation, node, false, false); err != nil {
		t.Fatal("owner COW changed retained source snapshot", err)
	}
	scope.routineAuth = *eMustJSONBRoutineAuthority(t, e, readerOp)
	err = exec.AdmitRoutineCall(&evaluation, node, false, false)
	var conflict catalog.Conflict
	if !errors.As(err, &conflict) {
		t.Fatal("NULL erasure rescued stale direct source", err)
	}
	if base.Get(captured.Address()).Revision() != captured.Revision() || captured.Owner == target || !reflect.DeepEqual(node, before) || !reflect.DeepEqual(binder.Dependencies, original) {
		t.Fatal("owner COW mutated original descriptor/tree/manifest")
	}
}

func TestRandomPlanningConcurrentReuse(t *testing.T) {
	e, op, scope := jsonbRecordRoutineUnitFixture(t)
	readerOp, done := roleOperation(t, e, "jsonb_authority_reader")
	defer done()
	readerScope, err := e.StartStatement(readerOp)
	if err != nil {
		t.Fatal(err)
	}
	defer readerScope.Finish(nil)
	result, err := readerScope.Execute(statement(t, "SELECT 1"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != nil {
		result.Rows.Close()
	}
	node, binder, evaluation := strictNullUnitBind(t, e, op, scope, "random(NULL::integer,abs($1::integer))")
	call := node.(functions.Random)
	before := call
	before.Args = append([]expr.Expr(nil), call.Args...)
	original := append([]catalog.ExpressionDependency(nil), binder.Dependencies...)
	child := randomPlanningUnitDependency(t, e, original, "abs")
	if err := e.SetObjectACL(context.Background(), child.Ref, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	authorized := evaluation
	authorized.RoutineAuthority = eMustJSONBRoutineAuthority(t, e, op)
	denied := evaluation
	denied.Host = hostOf(readerScope)
	denied.RoutineAuthority = eMustJSONBRoutineAuthority(t, e, readerOp)
	inputType := binder.Params.Parameters[1].Type
	one, err := sqltype.Integer(inputType, -1)
	if err != nil {
		t.Fatal(err)
	}
	two, err := sqltype.Integer(inputType, -2)
	if err != nil {
		t.Fatal(err)
	}
	// Each worker owns a context, parameter slice and physical session. Swap
	// actors/frames between rounds rather than racing catalog publication.
	for round := range 2 {
		contexts := []exec.Context{authorized, denied}
		frames := [][]sqltype.Value{{one}, {two}}
		if round == 1 {
			contexts[0], contexts[1] = contexts[1], contexts[0]
			frames[0], frames[1] = frames[1], frames[0]
		}
		type outcome struct {
			worker int
			null   bool
			err    error
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
				preflight, _, err := exec.PreflightCoercionsWithPolicy(&local, node, true)
				if err != nil {
					results <- outcome{worker: i, err: err}
					return
				}
				planned, _, err := exec.SimplifyConditional(&local, preflight, true)
				if err != nil {
					results <- outcome{worker: i, err: err}
					return
				}
				value, err := planned.Eval(&local)
				results <- outcome{worker: i, null: value.IsNull() && value.Type() == node.ResultUsage().Type, err: err}
			}(i, contexts[i])
		}
		close(start)
		workers.Wait()
		close(results)
		for got := range results {
			authorizedWorker := got.worker == round
			if authorizedWorker {
				if got.err != nil || !got.null {
					t.Fatal("authorized command lost its typed NULL", round, got)
				}
			} else {
				nullCountUnitDiagnostic(t, got.err, sqlstate.Error{Code: "42501", Message: "permission denied for function abs"})
			}
		}
		if !reflect.DeepEqual(node, before) || !reflect.DeepEqual(binder.Dependencies, original) {
			t.Fatal("concurrent preparation mutated shared analyzed tree/manifest")
		}
	}
}

func TestRandomStrictNullTypedInputs(t *testing.T) {
	e, op, scope := jsonbRecordRoutineUnitFixture(t)
	readerOp, done := roleOperation(t, e, "jsonb_authority_reader")
	defer done()
	node, binder, evaluation := strictNullUnitBind(t, e, op, scope, "random(NULL::integer,abs($2::integer))")
	call := node.(functions.Random)
	before := call
	before.Args = append([]expr.Expr(nil), call.Args...)
	original := append([]catalog.ExpressionDependency(nil), binder.Dependencies...)
	child := randomPlanningUnitDependency(t, e, original, "abs")
	if err := e.SetObjectACL(context.Background(), child.Ref, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	evaluation.RoutineAuthority = eMustJSONBRoutineAuthority(t, e, readerOp)
	inputType := binder.Params.Parameters[2].Type
	valid, err := sqltype.Integer(inputType, -1)
	if err != nil {
		t.Fatal(err)
	}
	for _, planner := range []struct {
		name string
		run  func(*exec.Context, expr.Expr) (expr.Expr, bool, error)
	}{
		{"preflight", exec.PreflightCoercions},
		{"conditional", func(c *exec.Context, node expr.Expr) (expr.Expr, bool, error) {
			return exec.SimplifyConditional(c, node, false)
		}},
		{"parameter_wrapper", exec.FoldStrictParameterNull},
	} {
		t.Run(planner.name, func(t *testing.T) {
			for _, frame := range [][]sqltype.Value{nil, make([]sqltype.Value, 1), {valid}, {valid, {}}} {
				local := evaluation
				local.Parameters = frame
				planned, _, err := planner.run(&local, node)
				if _, ok := planned.(functions.Random); err != nil || !ok {
					t.Fatal("missing/partial import erased required typed child", frame, planned, err)
				}
			}
			local := evaluation
			local.Parameters = []sqltype.Value{valid, valid}
			_, _, err := planner.run(&local, node)
			nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for function abs"})
			// The same imported frame with a different current actor succeeds.
			local.RoutineAuthority = eMustJSONBRoutineAuthority(t, e, op)
			planned, _, err := planner.run(&local, node)
			if err != nil || !exec.RandomLiteralNull(planned) {
				t.Fatal("authorized typed child did not recover", planned, err)
			}
			// A proven strict child NULL is not an invocation of denied abs.
			local = evaluation
			local.Parameters = []sqltype.Value{valid, sqltype.Null(inputType)}
			planned, _, err = planner.run(&local, node)
			if err != nil || !exec.RandomLiteralNull(planned) {
				t.Fatal("strict typed child NULL acquired EXECUTE", planned, err)
			}
			if !reflect.DeepEqual(node, before) || !reflect.DeepEqual(binder.Dependencies, original) {
				t.Fatal("typed preparation mutated original source/manifest")
			}
		})
	}
}
