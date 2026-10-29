package core_test

import (
	"bytes"
	"context"
	"errors"
	"math"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/host"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// This interceptor observes the genuine aggregate entry and forwards unchanged
// to the real statement host. It never implements a scalar or aggregate result.
type hostAggregateOwnerHost struct {
	*core.StatementHost
	t                           *testing.T
	selected                    *exec.TransitionAggregate
	transition, final           *proc.Frame
	transitionSlots, finalSlots *sqltype.Value
}

func (h *hostAggregateOwnerHost) InvokeAggregateRoutine(ctx context.Context, d *catalog.Procedure, frame *proc.Frame, usage sqltype.Usage, owner *catalog.Procedure, collation sqltype.CollationRef) (sqltype.Value, error) {
	h.t.Helper()
	p := h.selected
	if owner != p.Aggregate || owner.Aggregate == nil || d.Host == nil || d.SQL != nil || d.RoutineDefinition() != d.Host.Layout || d.Host.Layout.Kind != proc.HostBody {
		h.t.Fatal("aggregate frame borrowed a foreign owner or fake SQL layout")
	}
	formals := p.Formals
	previous, slots := &h.transition, &h.transitionSlots
	if d == p.Final {
		formals, previous, slots = p.FinalFormals, &h.final, &h.finalSlots
	} else if d != p.Support {
		h.t.Fatal("aggregate entered an unselected support")
	}
	if len(frame.Inputs) != len(formals) || len(frame.Parameters) != len(formals) || &frame.Inputs[0] != &formals[0] {
		h.t.Fatal("aggregate frame lost selected immutable formal backing")
	}
	for i, u := range formals {
		if frame.Inputs[i] != u || frame.Parameters[i].Type() != u.Type || u.Type != d.Host.Layout.Inputs[i].Type {
			h.t.Fatal("aggregate projected foreign typed frame slots")
		}
	}
	if *previous != nil && (*previous != frame || *slots != &frame.Parameters[0]) {
		h.t.Fatal("a group replaced its private support frame backing")
	}
	*previous, *slots = frame, &frame.Parameters[0]
	return h.StatementHost.InvokeAggregateRoutine(ctx, d, frame, usage, owner, collation)
}

func hostAggregateOwnerEngine(t *testing.T, retained *[]host.Object) (*core.Engine, *host.Runtime) {
	t.Helper()
	definition := host.LanguageDefinition{Provider: "aggregate-owner-host", ABIVersion: "int8-v1", Name: "aggregate_owner_host", HandlerName: host.Name{Schema: "public", Name: "aggregate_owner_handler"}, Handler: func(ctx context.Context, p host.ProgramFunc, args []host.Object) (host.Object, error) {
		return p(ctx, args)
	}}
	definition.Conversions = []host.ConversionDefinition{{ID: "int8", Type: host.Int8, Bootstrap: true, FromName: host.Name{Schema: "public", Name: "aggregate_owner_from"}, ToName: host.Name{Schema: "public", Name: "aggregate_owner_to"}, FromSQL: func(_ context.Context, value host.SQLValue) (host.Object, error) {
		if value.Type != host.Int8 {
			return host.Object{}, errors.New("aggregate owner: foreign SQL input")
		}
		if value.Null {
			return host.Object{Null: true}, nil
		}
		return host.Object{Value: value.Int64}, nil
	}, ToSQL: func(_ context.Context, value host.Object) (host.SQLValue, error) {
		if value.Null {
			return host.SQLValue{Type: host.Int8, Null: true}, nil
		}
		n, ok := value.Value.(int64)
		if !ok {
			return host.SQLValue{}, errors.New("aggregate owner: foreign Go result")
		}
		return host.SQLValue{Type: host.Int8, Int64: n}, nil
	}}}
	for _, arity := range []int{1, 2} {
		inputs, selectors := make([]host.Type, arity), make([]string, arity)
		for i := range inputs {
			inputs[i], selectors[i] = host.Int8, "int8"
		}
		id := "offset"
		if arity == 2 {
			id = "add"
		}
		definition.Programs = append(definition.Programs, host.ProgramDefinition{ID: id, Version: "1", Inputs: inputs, Result: host.Int8, InputConversions: selectors, ResultConversion: "int8", Execute: func(ctx context.Context, args []host.Object) (host.Object, error) {
			if err := ctx.Err(); err != nil {
				return host.Object{}, err
			}
			if len(args) != arity {
				return host.Object{}, errors.New("aggregate owner: wrong arity")
			}
			*retained = args
			var sum int64
			if arity == 1 {
				sum = 5
			}
			for _, arg := range args {
				if arg.Null {
					return host.Object{Null: true}, nil
				}
				n, ok := arg.Value.(int64)
				if !ok {
					return host.Object{}, errors.New("aggregate owner: foreign Go input")
				}
				if n > 0 && sum > math.MaxInt64-n || n < 0 && sum < math.MinInt64-n {
					return host.Object{}, errors.New("aggregate owner: int8 overflow")
				}
				sum += n
			}
			return host.Object{Value: sum}, nil
		}})
	}
	runtime, err := host.New(definition)
	if err != nil {
		t.Fatal(err)
	}
	e := engine.New()
	if err := e.InstallHostDefinitions([]*host.Runtime{runtime}); err != nil {
		t.Fatal(err)
	}
	// Installation clones application definitions into engine-owned capability
	// tokens. Compare selections against that actual immutable registry, never
	// against the preinstallation Runtime supplied by the application.
	if len(e.Catalog.HostRuntimes) != 1 {
		t.Fatal("aggregate fixture lacks its installed runtime registry")
	}
	installed := e.Catalog.HostRuntimes[0]
	if installed == runtime || installed.Fingerprint() != runtime.Fingerprint() ||
		installed.Program("add") == runtime.Program("add") ||
		installed.Program("offset") == runtime.Program("offset") {
		t.Fatal("installation failed to detach immutable runtime/program tokens")
	}
	return e, installed
}

func hostAggregateOwnerBind(t *testing.T, e *core.Engine) (*exec.TransitionAggregate, *exec.Context, *plan.AggregateNode, *core.StatementScope) {
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
	raw := enginetest.Statement(t, "SELECT aggregate_owner_sum(v) AS v FROM (VALUES(1::bigint)) s(v)")
	if err := scope.Gate(raw); err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	var description analyze.Binding
	if err == nil {
		description, err = core.Describe(op.Context(), e.AnalysisHost(op), raw, auth, ids.ObjectRef{}, scope.Input, nil, nil)
		scope.RoutineAuth, scope.RoutineDependencies = auth, description.Dependencies
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	node := description.Query.Root.(*plan.ProjectNode).Child.(*plan.AggregateNode)
	selected := node.Calls[0].Provider.(*exec.TransitionAggregate)
	evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Host: core.HostOf(scope), Catalog: auth.Catalog, Input: scope.Input, Output: scope.Output}}
	return selected, evaluation, node, scope
}

func TestHostAggregateFrameOwnership(t *testing.T) {
	var retained []host.Object
	source, runtime := hostAggregateOwnerEngine(t, &retained)
	for _, q := range []string{
		"CREATE FUNCTION aggregate_owner_step(s bigint,v bigint) RETURNS bigint LANGUAGE aggregate_owner_host STRICT AS 'add'",
		"CREATE FUNCTION aggregate_owner_final(s bigint) RETURNS bigint LANGUAGE aggregate_owner_host STRICT AS 'offset'",
		"CREATE AGGREGATE aggregate_owner_sum(bigint) (SFUNC=aggregate_owner_step,STYPE=bigint,FINALFUNC=aggregate_owner_final,INITCOND='0')",
	} {
		enginetest.Run(t, source, q)
	}
	var stream bytes.Buffer
	if err := engine.Dump(context.Background(), source, &stream); err != nil {
		t.Fatal(err)
	}
	var destinationRetained []host.Object
	destination, destinationRuntime := hostAggregateOwnerEngine(t, &destinationRetained)
	if err := engine.Restore(context.Background(), destination, bytes.NewReader(stream.Bytes())); err != nil {
		t.Fatal(err)
	}
	p, evaluation, node, scope := hostAggregateOwnerBind(t, source)
	read := source.CatalogRead()
	if err := p.ValidateSelection(); err != nil {
		t.Fatal(err)
	}
	if p.Call != nil || p.FinalCall != nil || p.ContextSupport != nil || p.Support.Host == nil || p.Final.Host == nil || p.Support.Host.Program != runtime.Program("add") || p.Final.Host.Program != runtime.Program("offset") {
		t.Fatal("accepted host aggregate lost concrete program/frame selection")
	}
	for _, support := range []*catalog.Procedure{p.Support, p.Final} {
		if read.Get(support.Address()) != support || support.RoutineDefinition() != support.Host.Layout || support.Revision() == 0 || support.Host.Program.Runtime() != runtime {
			t.Fatal("accepted support borrowed foreign descriptor/layout/program authority")
		}
		if _, err := exec.ValidateStoredRoutineDescriptor(read, support); err != nil {
			t.Fatal(err)
		}
	}
	mapped, err := stored.RemapViewQuery(read, node, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ })
	if err != nil {
		t.Fatal(err)
	}
	next := mapped.(*plan.AggregateNode).Calls[0].Provider.(*exec.TransitionAggregate)
	if next == p || next.Support != p.Support || next.Final != p.Final || &next.Formals[0] == &p.Formals[0] || &next.FinalFormals[0] == &p.FinalFormals[0] || &next.Conversions[0] == &p.Conversions[0] {
		t.Fatal("remap failed to detach vectors while retaining accepted support ownership")
	}
	forged := *p
	absent := *p.Support
	absent.Host = nil
	forged.Support = &absent
	forgedNode := *node
	forgedNode.Calls = append([]plan.AggregateCall(nil), node.Calls...)
	forgedNode.Calls[0].Provider = &forged
	if _, err := stored.RemapViewQuery(read, &forgedNode, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }); err == nil {
		t.Fatal("nil scalar call accepted without an actual frame-backed implementation")
	}
	dest, destEvaluation, _, _ := hostAggregateOwnerBind(t, destination)
	if dest.Support == p.Support || dest.Final.Host.Layout == p.Final.Host.Layout || dest.Support.Host.Program == p.Support.Host.Program || dest.Support.Host.Program != destinationRuntime.Program("add") || dest.Final.Host.Program != destinationRuntime.Program("offset") || dest.Formals[0].Type != dest.Support.Host.Layout.Inputs[0].Type {
		t.Fatal("restored aggregate did not acquire destination-owned program/layout/type authority")
	}
	if p.Support.Host.Program != runtime.Program("add") || p.Final.Host.Layout != p.Final.RoutineDefinition() || read.Get(p.Support.Address()) != p.Support {
		t.Fatal("destination replay mutated retained accepted source backing")
	}
	if _, err := exec.ValidateStoredRoutineDescriptor(read, dest.Support); err == nil {
		t.Fatal("destination support principal/program acquired source admission")
	}
	foreign := *p.Support
	foreign.Host = dest.Support.Host
	stateWithForeign := catalog.CloneState(read.State())
	stateWithForeign.SetDescriptor(foreign.Address(), &foreign)
	if _, err := exec.ValidateStoredRoutineDescriptor(source.Catalog.Read(stateWithForeign), &foreign); err == nil {
		t.Fatal("same-address support borrowed a foreign runtime/layout generation")
	}
	observer := &hostAggregateOwnerHost{StatementHost: evaluation.LiveHost().(*core.StatementHost), t: t, selected: p}
	evaluation.Host = observer
	state, err := p.InitWithContext(context.Background(), evaluation)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int64{1, 2, 3} {
		v, err := sqltype.Integer(p.Inputs[1].Type, n)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.Advance(context.Background(), state, []sqltype.Value{v}, evaluation); err != nil {
			t.Fatal(err)
		}
		assertHostAggregateSlotsCleared(t, retained)
	}
	value, err := p.FinalizeWithContext(context.Background(), state, nil, evaluation)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := value.Integer(); err != nil || n != 11 || value.Type() != p.Result.Type {
		t.Fatal("real final frame result", n, err)
	}
	assertHostAggregateSlotsCleared(t, retained)
	if observer.transition == nil || observer.final == nil || observer.transition == observer.final || observer.transitionSlots == observer.finalSlots || core.ScopeActiveSQLRoutineDepth(scope) != 0 {
		t.Fatal("transition/final shared private frame slots or leaked routine depth")
	}
	// A canceled final cannot mutate the accepted provider or the ending state.
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.FinalizeWithContext(canceled, state, nil, evaluation); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled final", err)
	}
	value, err = p.FinalizeWithContext(context.Background(), state, nil, evaluation)
	if n, integerErr := value.Integer(); err != nil || integerErr != nil || n != 11 {
		t.Fatal("same state after canceled final", n, err, integerErr)
	}
	// A real supplied computation fails after admission, not a mocked host gate.
	overflow, err := p.InitWithContext(context.Background(), evaluation)
	if err != nil {
		t.Fatal(err)
	}
	observer.transition, observer.final = nil, nil
	max, err := sqltype.Integer(p.Inputs[1].Type, math.MaxInt64)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Advance(context.Background(), overflow, []sqltype.Value{max}, evaluation); err != nil {
		t.Fatal(err)
	}
	if _, err := p.FinalizeWithContext(context.Background(), overflow, nil, evaluation); err == nil || err.Error() != "aggregate owner: int8 overflow" {
		t.Fatal("real admitted overflow", err)
	}
	assertHostAggregateSlotsCleared(t, retained)
	if core.ScopeActiveSQLRoutineDepth(scope) != 0 || p.Support.Host.Program != runtime.Program("add") || p.Final.Host.Program != runtime.Program("offset") {
		t.Fatal("failed support leaked scope or replaced accepted tokens")
	}
	// Destination execution uses the restored selected provider and its own host.
	destState, err := dest.InitWithContext(context.Background(), destEvaluation)
	if err != nil {
		t.Fatal(err)
	}
	destValue, err := dest.FinalizeWithContext(context.Background(), destState, nil, destEvaluation)
	if n, integerErr := destValue.Integer(); err != nil || integerErr != nil || n != 5 {
		t.Fatal("destination selected final", n, err, integerErr)
	}
	assertHostAggregateSlotsCleared(t, destinationRetained)
}

func assertHostAggregateSlotsCleared(t *testing.T, values []host.Object) {
	t.Helper()
	for _, value := range values {
		if value.Null || value.Value != nil {
			t.Fatal("completed host support retained live program object slots")
		}
	}
}
