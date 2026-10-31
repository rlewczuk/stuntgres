package core_test

import (
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
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type heapAttrsOperand struct {
	expr.Info
	order   *[]int
	index   int
	value   sqltype.Value
	failure error
}

func (n heapAttrsOperand) Eval(expr.Context) (sqltype.Value, error) {
	*n.order = append(*n.order, n.index)
	return n.value, n.failure
}

func TestPageinspectHeapAttrsCapture(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE EXTENSION pageinspect", "CREATE DOMAIN heap_dead AS bigint", "CREATE TABLE heap_capture(a integer,b heap_dead,c integer)", "ALTER TABLE heap_capture DROP COLUMN b", "DROP DOMAIN heap_dead"} {
		enginetest.SessionSQL(t, s, q)
	}
	// A bare synthetic scope attaches no transaction ticket. Real descriptor
	// capture must enter the normal transaction owner before acquiring locks.
	transaction := transactionBegin(t, s)
	t.Cleanup(func() { transactionEnd(t, s, transaction, false) })
	scope := pgrowlocksUnitScope(t, e, s)
	if scope.LockTicket == nil || scope.LockTicket != s.Transactions.Ticket || scope.TxRef != transaction {
		t.Fatal("capture fixture lacks admitted transaction ownership")
	}
	raw := enginetest.Statement(t, "SELECT tuple_data_split(do_detoast=>false,t_bits=>NULL,t_infomask2=>3,t_infomask=>0,t_data=>NULL::bytea,rel_oid=>'heap_capture'::regclass::oid)").(*parser.Select).Targets[0].(parser.FunctionExpr)
	e.Mu.Lock()
	auth, err := core.ScopeWriteAuthorizationLocked(scope, e)
	var bound expr.Expr
	if err == nil {
		b := core.NewExpressionBinder(scope.Op.Context(), e, scope.Input, scope.Op, nil)
		b.Query.Auth = &auth
		b.Clause.AggregateClause = "SELECT"
		bound, err = b.BindFunctionProvider(raw)
		if err == nil {
			scope.RoutineAuth, scope.RoutineDependencies = auth, b.Dependencies
		}
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	call := bound.(plan.InstalledPageinspectExpr)
	var target ids.ObjectAddress
	for address, relation := range auth.Catalog.Relations() {
		if relation.Name() == "heap_capture" {
			target = address
			break
		}
	}
	if target == (ids.ObjectAddress{}) {
		t.Fatal("missing genuine capture relation")
	}
	resource := lock.Resource{ObjectRef: e.Catalog.Domain.Ref(auth.Catalog.Epoch(), target)}
	priorAccessShare := e.Locks.HasRelationMode(scope.LockTicket, resource, lock.AccessShare)
	columns, err := scope.WriteHost().PageinspectHeap(call, uint32(target.ObjectID))
	want := []sqltype.HeapStoredColumn{{Length: 4, Alignment: 4}, {Length: 8, Alignment: 8}, {Length: 4, Alignment: 4}}
	if err != nil || !reflect.DeepEqual(columns, want) {
		t.Fatal("dropped physical descriptor", columns, err)
	}
	columns[1].Length = 1
	again, err := scope.WriteHost().PageinspectHeap(call, uint32(target.ObjectID))
	if err != nil || !reflect.DeepEqual(again, want) {
		t.Fatal("capture leaked mutable backing", again, err)
	}
	if e.Locks.HasRelationMode(scope.LockTicket, resource, lock.AccessShare) != priorAccessShare {
		t.Fatal("schema capture changed the owner's prior AccessShare contribution")
	}
	bad := call
	bad.Ref.Epoch++
	if _, err := scope.WriteHost().PageinspectHeap(bad, uint32(target.ObjectID)); err == nil {
		t.Fatal("stale identity admitted")
	}
	evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: scope.Op.Context(), Catalog: auth.Catalog, Host: core.HostOf(scope), Input: scope.Input, Output: scope.Output}}
	order := make([]int, 0, 6)
	children := make([]expr.Expr, len(call.Args))
	for i, child := range call.Args {
		value, err := child.Eval(evaluation)
		if err != nil {
			t.Fatal(err)
		}
		children[i] = heapAttrsOperand{Info: expr.Info{Usage: child.ResultUsage()}, order: &order, index: i, value: value}
	}
	rebuilt, err := call.Rebuild(call.Info, children)
	if err != nil {
		t.Fatal(err)
	}
	n := rebuilt.(plan.InstalledPageinspectExpr)
	value, err := n.Eval(evaluation)
	if err != nil || !value.IsNull() || !reflect.DeepEqual(order, []int{0, 1, 2, 3, 4, 5}) {
		t.Fatal("native written-order once-only before NULL", order, value, err)
	}
	failure := errors.New("independent operand failure")
	order = order[:0]
	last := children[5].(heapAttrsOperand)
	last.failure = failure
	children[5] = last
	rebuilt, err = call.Rebuild(call.Info, children)
	if err != nil {
		t.Fatal(err)
	}
	_, err = rebuilt.Eval(evaluation)
	if !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1, 2, 3, 4, 5}) {
		t.Fatal("NULL suppressed failing sibling", order, err)
	}
	if call.Args[0] == nil || call.Procedure != n.Procedure || call.Ref != n.Ref || call.Slots != n.Slots {
		t.Fatal("rebuild changed predecessor authority")
	}
}

func TestPageinspectHeapAttrsHelperIdentity(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE EXTENSION pageinspect")
	scope := pgrowlocksUnitScope(t, e, s)
	raw := enginetest.Statement(t, "SELECT heap_page_item_attrs(NULL::bytea,0::oid::regclass,false)").(*parser.Select).Targets[0].(parser.FunctionExpr)
	e.Mu.Lock()
	auth, err := core.ScopeWriteAuthorizationLocked(scope, e)
	var bound expr.Expr
	if err == nil {
		b := core.NewExpressionBinder(scope.Op.Context(), e, scope.Input, scope.Op, nil)
		b.Query.Auth = &auth
		b.Clause.AggregateClause = "SELECT"
		bound, err = b.BindFunctionProvider(raw)
		if err == nil {
			scope.RoutineAuth, scope.RoutineDependencies = auth, b.Dependencies
		}
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	call := bound.(plan.InstalledPageinspectExpr)
	evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: scope.Op.Context(), Catalog: auth.Catalog, Host: core.HostOf(scope), Input: scope.Input, Output: scope.Output}}
	// NULL page returns zero rows only after genuine helper startup admission.
	cursor, err := evaluation.OpenPageinspect(call)
	if err != nil {
		t.Fatal(err)
	}
	if _, more, err := cursor.Next(scope.Op.Context()); more || err != nil {
		t.Fatal("NULL wrapper cardinality", more, err)
	}
	cursor.Close()
	for _, slot := range []int{15, 18} {
		address := call.Procedure.Origin.Installation.Members[slot]
		member := auth.Catalog.MustProcedure(address)
		for _, corrupt := range []func(*catalog.State){
			func(state *catalog.State) { state.RemoveDescriptor(address) },
			func(state *catalog.State) {
				bad := *member
				bad.Strict = !bad.Strict
				state.SetDescriptor(address, &bad)
			},
			func(state *catalog.State) {
				bad := *member
				origin := *bad.Origin
				origin.Declaration = 0
				bad.Origin = &origin
				state.SetDescriptor(address, &bad)
			},
		} {
			state := catalog.CloneState(auth.Catalog.State())
			corrupt(state)
			changed := auth
			changed.Catalog = auth.Catalog.WithState(state)
			scope.RoutineAuth = changed
			evaluation.Catalog = changed.Catalog
			if _, err := evaluation.OpenPageinspect(call); err == nil {
				t.Fatal("NULL wrapper bypassed missing/forged helper identity", slot)
			}
		}
	}
	scope.RoutineAuth = auth
	evaluation.Catalog = auth.Catalog
	cursor, err = evaluation.OpenPageinspect(call)
	if err != nil {
		t.Fatal("helper rejection contaminated original authority", err)
	}
	cursor.Close()
}
