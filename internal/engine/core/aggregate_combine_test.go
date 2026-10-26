package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Build a real admitted support host, then exercise private provider states and
// closed frames directly. SQL result scenarios remain in reference/aggregates.
func aggregateCombineUnit(t *testing.T) (*core.Engine, *exec.TransitionAggregate, *exec.Context, *plan.AggregateNode) {
	t.Helper()
	e := engine.New()
	enginetest.Run(t, e, "CREATE FUNCTION ac_unit_step(bigint,integer) RETURNS bigint LANGUAGE SQL AS $$SELECT CASE WHEN $2 IS NULL THEN $1 ELSE coalesce($1,0)+$2::bigint*$2::bigint END$$")
	enginetest.Run(t, e, "CREATE AGGREGATE ac_unit(integer) (SFUNC=ac_unit_step,STYPE=bigint,COMBINEFUNC=pg_catalog.int8pl,PARALLEL=SAFE)")
	return aggregateCombineBindUnit(t, e, "SELECT ac_unit(x) AS v FROM(VALUES(1),(2)) s(x)")
}

func aggregateCombineBindUnit(t *testing.T, e *core.Engine, sql string) (*core.Engine, *exec.TransitionAggregate, *exec.Context, *plan.AggregateNode) {
	t.Helper()
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(done)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := scope.Finish(nil); err != nil {
			t.Error(err)
		}
	})
	raw := enginetest.Statement(t, sql)
	if err := scope.Gate(raw); err != nil {
		t.Fatal(err)
	}
	if scope.TxRef.Epoch() == 0 {
		t.Fatal("private support owner lacks ordinary statement admission")
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	var bindingErr error
	var query *plan.Query
	if err == nil {
		b, failure := core.Describe(op.Context(), e.AnalysisHost(op), raw, auth, ids.ObjectRef{}, scope.Input, nil, nil)
		bindingErr = failure
		query = b.Query
		scope.RoutineAuth, scope.RoutineDependencies = auth, b.Dependencies
	}
	e.Mu.Unlock()
	if err != nil || bindingErr != nil {
		t.Fatal(err, bindingErr)
	}
	n := query.Root.(*plan.ProjectNode).Child.(*plan.AggregateNode)
	p := n.Calls[0].Provider.(*exec.TransitionAggregate)
	evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Host: core.HostOf(scope), Catalog: auth.Catalog, Input: scope.Input, Output: scope.Output}}
	return e, p, evaluation, n
}

func TestAggregateCombinePartitions(t *testing.T) {
	_, p, evaluation, _ := aggregateCombineUnit(t)
	ctx := evaluation.FunctionContext()
	var partials []any
	for i, inputs := range [][]*int64{nil, {nil}, {new(int64(1)), new(int64(2))}, {new(int64(3)), new(int64(4))}} {
		state, err := p.InitWithContext(ctx, evaluation)
		if err != nil {
			t.Fatal(err)
		}
		for _, input := range inputs {
			value := sqltype.Null(p.Inputs[1].Type)
			if input != nil {
				value, err = sqltype.Integer(p.Inputs[1].Type, *input)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := p.Advance(ctx, state, []sqltype.Value{value}, evaluation); err != nil {
				t.Fatal(err)
			}
		}
		v, err := p.Finalize(ctx, state)
		if err != nil {
			t.Fatal(err)
		}
		if i < 2 {
			if !v.IsNull() {
				t.Fatal("empty/NULL partial lost absence")
			}
		} else {
			want := int64(5)
			if i == 3 {
				want = 25
			}
			got, err := v.Integer()
			if err != nil || got != want {
				t.Fatal(got, err, want)
			}
		}
		partials = append(partials, state)
	}
	leader, err := p.InitWithContext(ctx, evaluation)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range partials {
		if err := p.Combine(ctx, leader, state, evaluation); err != nil {
			t.Fatal(err)
		}
	}
	value, err := p.Finalize(ctx, leader)
	got, integerErr := value.Integer()
	if err != nil || integerErr != nil || got != 30 {
		t.Fatal(got, err, integerErr)
	}
	// Adoption and combination never mutate the incoming actual states.
	for i, state := range partials[2:] {
		value, err := p.Finalize(ctx, state)
		got, integerErr := value.Integer()
		if err != nil || integerErr != nil || got != []int64{5, 25}[i] {
			t.Fatal("borrowed state mutated", got, err)
		}
	}
}

func TestAggregateCombineRemap(t *testing.T) {
	e, p, _, node := aggregateCombineUnit(t)
	read := e.CatalogRead()
	mapped, err := stored.RemapViewQuery(read, node, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ })
	if err != nil {
		t.Fatal(err)
	}
	next := mapped.(*plan.AggregateNode).Calls[0].Provider.(*exec.TransitionAggregate)
	if next == p || next.CombinePlan == p.CombinePlan || !reflect.DeepEqual(next.CombinePlan.Inputs, p.CombinePlan.Inputs) {
		t.Fatal("combine frame not independently remapped")
	}
	next.CombinePlan.Formals[0] = sqltype.Usage{}
	if p.CombinePlan.Formals[0].Type == nil {
		t.Fatal("source combine formals mutated")
	}
	forged := *node
	forged.Calls = append([]plan.AggregateCall(nil), node.Calls...)
	provider := *p
	combine := *p.CombinePlan
	combine.Formals = append([]sqltype.Usage(nil), combine.Formals...)
	combine.Formals[0] = p.Inputs[1]
	provider.CombinePlan = &combine
	forged.Calls[0].Provider = &provider
	if _, err := stored.RemapViewQuery(read, &forged, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }); err == nil {
		t.Fatal("foreign combine formal accepted")
	}
	t.Run("builtin_string_support", aggregateCombineStringSupportRemap)
}

func TestAggregateCombineSignatures(t *testing.T) {
	e, p, _, _ := aggregateCombineUnit(t)
	read := e.CatalogRead()
	if err := routine.ValidateAggregateDefinition(read, p.Aggregate); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*catalog.OwnedAggregate){
		func(a *catalog.OwnedAggregate) { a.Parallel = 'x' },
		func(a *catalog.OwnedAggregate) { a.Definition.Functions[2] = 177 },
		func(a *catalog.OwnedAggregate) { a.Definition.Functions[3] = 6300 },
		func(a *catalog.OwnedAggregate) { a.Definition.SortOperator = 98 },
	} {
		procedure, payload := *p.Aggregate, *p.Aggregate.Aggregate
		procedure.Aggregate = &payload
		mutate(&payload)
		if routine.ValidateAggregateDefinition(read, &procedure) == nil {
			t.Fatal("invalid aggregate declaration accepted")
		}
		if exec.SameRoutineDeclaration(p.Aggregate, &procedure) {
			t.Fatal("changed partial declaration considered equal")
		}
	}
	internal := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 91001}, ObjectName: "ac_unit_internal", RoutineKind: catalog.AggregateRoutine, Signature: "2281", Result: 2281, Aggregate: &catalog.OwnedAggregate{Parallel: 's', Definition: catalog.AggregateDefinition{Function: 91001, Kind: "n", TransitionType: 2281, Functions: [8]ids.OID{6299, 0, 6299, 6300, 6301}, FinalModify: "r", MovingFinalModify: "r"}}}
	if err := routine.ValidateAggregateDefinition(read, internal); err != nil {
		t.Fatal("real opaque support declaration rejected", err)
	}
	carrier, ok := read.LookupType(2281)
	if !ok || carrier.Concrete() {
		t.Fatal("internal became a public concrete datum")
	}
	// Exercise the actual declared-arity selector, not only detached signature
	// validation. An opaque support probe must never construct sqltype.Null.
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(done)
	bytea, ok := read.LookupType(sqltype.Bytea)
	if !ok {
		t.Fatal("missing bytea")
	}
	probe := func(name parser.ObjectName, inputs []sqltype.Usage) (analyze.SQLCallable, error) {
		e.Mu.Lock()
		defer e.Mu.Unlock()
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			return analyze.SQLCallable{}, err
		}
		path, err := e.LookupNamespaces(auth, name, analyze.FunctionLookup)
		if err != nil {
			return analyze.SQLCallable{}, err
		}
		binder := core.NewExpressionBinder(op.Context(), e, sqltype.InputEnvironment{}, op, nil)
		authority := core.AuthorityOf(auth)
		binder.Query.Auth = authority
		return binder.SelectAggregateSupport(auth.Catalog, nil, path, name, inputs)
	}
	for _, tc := range []struct {
		name   string
		id     ids.OID
		inputs []sqltype.Usage
	}{
		{"string_agg_combine", 6299, []sqltype.Usage{carrier.Usage(), carrier.Usage()}},
		{"string_agg_serialize", 6300, []sqltype.Usage{carrier.Usage()}},
		{"string_agg_deserialize", 6301, []sqltype.Usage{bytea.Usage(), carrier.Usage()}},
	} {
		name := parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_catalog"}, {Text: tc.name}}}
		selected, err := probe(name, tc.inputs)
		if err != nil || selected.Procedure == nil || selected.Procedure.Address().ObjectID != tc.id || len(selected.Inputs) != len(tc.inputs) {
			t.Fatal("opaque support selection", tc.name, selected, err)
		}
		for i, input := range selected.Inputs {
			if input.Type != tc.inputs[i].Type {
				t.Fatal("opaque support probe changed its declared type", tc.name, i)
			}
		}
	}
	support, ok := read.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 6299})
	if !ok {
		t.Fatal("missing real combine identity")
	}
	formals, result, err := routine.AggregateSupportTypes(read, nil, support, internal.Aggregate, []sqltype.Usage{carrier.Usage(), carrier.Usage()})
	if err != nil || len(formals) != 2 || formals[0].Type != carrier || result.Type != carrier {
		t.Fatal("fixed internal incorrectly polymorphic", formals, result, err)
	}
	deserializer, ok := read.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 6301})
	if !ok {
		t.Fatal("missing real deserializer")
	}
	bad := *deserializer
	bad.Result = 25
	state := catalog.CloneState(read.State())
	state.SetDescriptor(bad.Address(), &bad)
	if routine.ValidateAggregateDefinition(catalog.NewRead(read.Owner(), state), internal) == nil {
		t.Fatal("wrong deserializer return accepted")
	}
	// The actual partial-stage admission must demand combine EXECUTE before
	// any support invocation, unlike ordinary serial transition admission.
	stageEngine := engine.New()
	for _, sql := range []string{
		"CREATE ROLE ac_stage_owner",
		"GRANT USAGE,CREATE ON SCHEMA public TO ac_stage_owner",
		"CREATE FUNCTION ac_stage_step(bigint,integer) RETURNS bigint LANGUAGE SQL AS $$SELECT coalesce($1,0)+$2$$",
		"CREATE AGGREGATE ac_stage(integer) (SFUNC=ac_stage_step,STYPE=bigint,COMBINEFUNC=pg_catalog.int8pl,PARALLEL=SAFE)",
		"ALTER FUNCTION ac_stage(integer) OWNER TO ac_stage_owner",
		"REVOKE EXECUTE ON FUNCTION pg_catalog.int8pl(bigint,bigint) FROM PUBLIC",
	} {
		enginetest.Run(t, stageEngine, sql)
	}
	_, stageProvider, stageContext, _ := aggregateCombineBindUnit(t, stageEngine, "SELECT ac_stage(x) AS v FROM(VALUES(1),(2)) s(x)")
	stageCall := plan.AggregateCall{Provider: stageProvider, Procedure: stageProvider.Aggregate}
	if err := exec.AdmitAggregateSupport(stageContext, &stageCall); err != nil {
		t.Fatal("unused combine ACL leaked into serial stage", err)
	}
	err = exec.AdmitAggregatePartialSupport(stageContext, &stageCall, false)
	var denied *sqlstate.Error
	if !errors.As(err, &denied) || denied.Code != "42501" || denied.Message != "permission denied for function int8pl" {
		t.Fatal("actual combine stage lacked owner admission", err)
	}
	if core.ScopeActiveSQLRoutineDepth(core.ScopeOf(stageContext.Host)) != 0 {
		t.Fatal("support body executed during combine permission admission")
	}
}

func TestAggregateCombineParallelSafety(t *testing.T) {
	ctx := exec.ParallelWorkerContext(context.Background())
	evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}
	_, err := evaluation.Sequence(functions.SequenceRequest{Name: "nextval"})
	var diagnostic *sqlstate.Error
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "25000", Message: "cannot execute nextval() during a parallel operation"}
	if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
		t.Fatal(err, diagnostic)
	}
	if exec.InParallelWorker(context.Background()) {
		t.Fatal("worker marker leaked")
	}
	e := engine.New()
	enginetest.Run(t, e, "CREATE SEQUENCE ac_unit_worker_seq")
	// Multiple statements make this helper genuinely non-inlineable. The
	// oracle's earlier one-frame inlinable discovery is not its Context golden.
	enginetest.Run(t, e, "CREATE FUNCTION ac_unit_nested() RETURNS bigint LANGUAGE SQL AS $$SELECT 1; SELECT nextval('ac_unit_worker_seq')$$")
	enginetest.Run(t, e, "CREATE FUNCTION ac_unit_worker_step(bigint,integer) RETURNS bigint LANGUAGE SQL AS $$SELECT coalesce($1,0)+ac_unit_nested()$$")
	enginetest.Run(t, e, "CREATE AGGREGATE ac_unit_worker(integer) (SFUNC=ac_unit_worker_step,STYPE=bigint,COMBINEFUNC=pg_catalog.int8pl,PARALLEL=SAFE)")
	_, p, actual, _ := aggregateCombineBindUnit(t, e, "SELECT ac_unit_worker(x) AS v FROM(VALUES(1),(2)) s(x)")
	worker := exec.ParallelWorkerContext(actual.Context)
	local := *actual
	local.Context = worker
	state, err := p.InitWithContext(worker, &local)
	if err != nil {
		t.Fatal(err)
	}
	one, _ := sqltype.Integer(p.Inputs[1].Type, 1)
	err = p.Advance(worker, state, []sqltype.Value{one}, &local)
	var full interface{ SQLState() string }
	if !errors.As(err, &full) || full.SQLState() != "25000" {
		t.Fatal("nested worker marker/context lost", err)
	}
	// The same borrowed owner is healthy afterwards; a worker marker cannot
	// leak into the root operation or advance the sequence before rejection.
	healthy, err := p.InitWithContext(actual.Context, actual)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Advance(actual.Context, healthy, []sqltype.Value{one}, actual); err != nil {
		t.Fatal(err)
	}
	v, err := p.Finalize(actual.Context, healthy)
	n, integerErr := v.Integer()
	if err != nil || integerErr != nil || n != 1 {
		t.Fatal("prohibited sequence effect escaped or marker leaked", n, err, integerErr)
	}
}
