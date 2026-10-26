package core_test

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type amcheckOperand struct {
	expr.Expr
	evaluate func(expr.Context) (sqltype.Value, error)
}

func (n amcheckOperand) Eval(ctx expr.Context) (sqltype.Value, error) { return n.evaluate(ctx) }

func TestAmcheckScalarEvaluation(t *testing.T) {
	e, _, _ := amcheckUnitRoot(t, false)
	n := amcheckUnitExpression(t, e, true, true, true, true)
	reader, ticket := blockingUnitOwner(t, e)
	scope := pgrowlocksUnitScope(t, e, reader)
	scope.RoutineDependencies = []catalog.ExpressionDependency{{Ref: n.Ref, Revision: n.Procedure.Revision()}}
	e.Mu.Lock()
	auth, err := core.ScopeWriteAuthorizationLocked(scope, e)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	scope.RoutineAuth = auth
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(scope), Catalog: auth.Catalog, Input: scope.Input, Output: scope.Output}}
	var order []int
	call := n
	call.Args = append([]expr.Expr(nil), n.Args...)
	for i, child := range n.Args {
		call.Args[i] = amcheckOperand{Expr: child, evaluate: func(ctx expr.Context) (sqltype.Value, error) { order = append(order, i); return child.Eval(ctx) }}
	}
	before := e.Locks.Inspect(ticket)
	value, err := call.Eval(&evaluation)
	if err != nil || value.IsNull() || value.Type().ID() != sqltype.Void || !reflect.DeepEqual(order, []int{0, 1, 2, 3}) {
		t.Fatal("actual installed scalar evaluation", value, order, err)
	}
	if !reflect.DeepEqual(before, e.Locks.Inspect(ticket)) {
		t.Fatal("successful scalar retained contributions")
	}
	// Every argument is still evaluated once when a STRICT call has a NULL.
	order = nil
	call.Args[1] = amcheckOperand{Expr: n.Args[1], evaluate: func(expr.Context) (sqltype.Value, error) {
		order = append(order, 1)
		return sqltype.Null(n.Args[1].ResultUsage().Type), nil
	}}
	value, err = call.Eval(&evaluation)
	if err != nil || !value.IsNull() || value.Type().ID() != sqltype.Void || !reflect.DeepEqual(order, []int{0, 1, 2, 3}) {
		t.Fatal("strict evaluation", value, order, err)
	}
	if !reflect.DeepEqual(before, e.Locks.Inspect(ticket)) {
		t.Fatal("strict NULL acquired locks")
	}
	primary := errors.New("argument failed")
	call.Args[2] = amcheckOperand{Expr: n.Args[2], evaluate: func(expr.Context) (sqltype.Value, error) { return sqltype.Value{}, primary }}
	if _, err := call.Eval(&evaluation); !errors.Is(err, primary) {
		t.Fatal("STRICT hid argument failure", err)
	}
	// A NULL cannot turn a foreign or stale selected lifetime into authority.
	foreign := engine.New()
	beforeInvalid := len(order)
	bad := call
	bad.Ref = foreign.Catalog.Domain.Ref(n.Ref.Epoch, n.Ref.Address)
	if _, err := bad.Eval(&evaluation); err == nil || len(order) != beforeInvalid {
		t.Fatal("NULL admitted foreign installed identity or evaluated children", err)
	}
	isolated := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: e.CatalogRead()}}
	state := catalog.CloneState(e.Catalog.State)
	state.RemoveDescriptor(n.Ref.Address)
	isolated.Catalog = e.Catalog.Read(state)
	if _, err := call.Eval(&isolated); err == nil || len(order) != beforeInvalid {
		t.Fatal("NULL admitted removed installed lifetime or evaluated children", err)
	}
}

func TestAmcheckAdmittedCatalogIdentity(t *testing.T) {
	e, index, root := amcheckUnitRoot(t, false)
	n := amcheckUnitExpression(t, e, true, true, true, true)
	retained := e.CatalogRead()
	// A retained preparation and a current recipe admission are separate
	// authorities. Only the latter may supply the host invocation generation.
	next := catalog.CloneState(retained.State())
	next.SetEpoch(retained.Epoch() + 1)
	current := *n.Procedure
	current.ObjectRevision++
	next.SetDescriptor(current.Address(), &current)
	e.Mu.Lock()
	e.Catalog.PublishLocked(next)
	e.Mu.Unlock()
	reader, ticket := blockingUnitOwner(t, e)
	scope := pgrowlocksUnitScope(t, e, reader)
	e.Mu.Lock()
	auth, err := core.ScopeWriteAuthorizationLocked(scope, e)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	evaluation := exec.Context{
		EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(scope), Catalog: retained, Input: scope.Input, Output: scope.Output},
		EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineRecipe: true, RoutineChecks: []catalog.ExpressionDependency{{Ref: e.Catalog.Domain.Ref(next.Epoch(), n.Ref.Address), Revision: n.Procedure.Revision()}}},
	}
	before := e.Locks.Inspect(ticket)
	value, err := n.Eval(&evaluation)
	if err != nil || value.IsNull() || value.Type().ID() != sqltype.Void {
		t.Fatal("admitted current identity", value, err)
	}
	if n.Ref.Epoch != retained.Epoch() || n.Procedure != retained.Get(n.Ref.Address) || n.CallRoutineReference() != n.Procedure {
		t.Fatal("invocation mutated retained selection", n)
	}
	if e.Storage.IndexRoots[index.Address()] != root || !reflect.DeepEqual(before, e.Locks.Inspect(ticket)) {
		t.Fatal("admission replaced root or retained locks")
	}
	// The same old preparation without a current lifetime proof is rejected;
	// a matching address/declaration is not permission to refresh its epoch.
	stale := evaluation
	stale.RoutineChecks = []catalog.ExpressionDependency{{Ref: n.Ref, Revision: n.Procedure.Revision()}}
	if _, err := n.Eval(&stale); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("unadmitted stale selection", err)
	}
	// Execution still checks the actual retained root, not a replacement built
	// from the heap merely because the routine crossed an admitted generation.
	damaged := amcheckUnitClone(root)
	damaged.Entries[0], damaged.Entries[1] = damaged.Entries[1], damaged.Entries[0]
	e.Storage.IndexRoots[index.Address()] = damaged
	_, err = n.Eval(&evaluation)
	amcheckUnitDiagnostic(t, err, sqlstate.Error{Code: "XX002", Message: `item order invariant violated for index "i"`})
	if e.Storage.IndexRoots[index.Address()] != damaged || !reflect.DeepEqual(before, e.Locks.Inspect(ticket)) {
		t.Fatal("checker repaired root or retained locks")
	}
}
