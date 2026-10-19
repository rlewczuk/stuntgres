package engine

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestTransactionIdentityAgeArithmetic(t *testing.T) {
	for _, tc := range []struct {
		now, xid uint32
		want     int32
	}{
		{3, 0, 2147483647}, {3, 1, 2147483647}, {3, 2, 2147483647}, {3, 3, 0},
		{3, 4294967295, 4}, {2147483651, 3, -2147483648}, {4294967295, 3, -4},
	} {
		if got := visibility.Age(tc.now, tc.xid); got != tc.want {
			t.Fatalf("age(%d,%d)=%d want %d", tc.now, tc.xid, got, tc.want)
		}
	}
	e := New()
	_, a := blockingUnitOwner(t, e)
	_, b := blockingUnitOwner(t, e)
	e.mu.Lock()
	defer e.mu.Unlock()
	anchor, err := e.transactionAgeLocked(a.Ref(), 3)
	if err != nil || anchor != 0 || e.visibility.LastAssignedXID() != 2 || e.visibility.Entries[a.Ref()].AssignedXID != 0 {
		t.Fatal("inquiry allocation", anchor, err)
	}
	if err := e.assignTransactionXIDLocked(b.Ref()); err != nil {
		t.Fatal(err)
	}
	if err := e.assignTransactionXIDLocked(a.Ref()); err != nil {
		t.Fatal(err)
	}
	if got, err := e.transactionAgeLocked(a.Ref(), 3); err != nil || got != 0 {
		t.Fatal("changed first-demand anchor", got, err)
	}
}

func TestTransactionIdentityAllocation(t *testing.T) {
	for _, tc := range []struct{ last, want uint64 }{{2, 3}, {4294967294, 4294967295}, {4294967295, 4294967299}, {8589934591, 8589934595}, {math.MaxUint64 - 1, math.MaxUint64}} {
		got, err := visibility.NextFullXID(tc.last)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	if _, err := visibility.NextFullXID(math.MaxUint64); err == nil {
		t.Fatal("full identity wrapped")
	}
	e := New()
	_, a := blockingUnitOwner(t, e)
	_, b := blockingUnitOwner(t, e)
	_, c := blockingUnitOwner(t, e)
	pgLocksUnitAssign(t, e, a)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.visibility.SetLastAssignedXIDLocked(4294967295)
	before := len(e.visibility.History)
	if err := e.assignTransactionXIDLocked(b.Ref()); err == nil || e.visibility.LastAssignedXID() != 4294967295 || len(e.visibility.History) != before || b.AssignedPublicXID() != 0 {
		t.Fatal("live low32 alias partially admitted", err)
	}
	e.visibility.RecordCommitTimestampLocked(a.Ref())
	if err := e.assignTransactionXIDLocked(b.Ref()); err == nil {
		t.Fatal("publisher-to-release gap reused live mirror")
	}
	e.mu.Unlock()
	e.endLockOwner(a)
	e.mu.Lock()
	if err := e.assignTransactionXIDLocked(b.Ref()); err != nil {
		t.Fatal(err)
	}
	if e.visibility.Entries[b.Ref()].FullXID != 4294967299 || e.visibility.Entries[b.Ref()].AssignedXID != 3 || b.AssignedPublicXID() != 3 || e.visibility.CommitTimestamps != nil {
		t.Fatal("full versus low mirror/OFF-lazy", e.visibility.Entries[b.Ref()])
	}
	previous := e.visibility.Entries[b.Ref()]
	if err := e.assignTransactionXIDLocked(b.Ref()); err != nil || e.visibility.Entries[b.Ref()] != previous {
		t.Fatal("non-idempotent assignment", err)
	}
	e.visibility.SetLastAssignedXIDLocked(math.MaxUint64)
	if err := e.assignTransactionXIDLocked(c.Ref()); err == nil || e.visibility.Entries[c.Ref()].FullXID != 0 || c.AssignedPublicXID() != 0 {
		t.Fatal("exhaustion published assignment", err)
	}
}

func identityUnitStatus(t *testing.T, e *Engine, id uint64, want string, present bool) {
	t.Helper()
	e.mu.Lock()
	got, ok, err := e.visibility.TransactionStatusLocked(id)
	e.mu.Unlock()
	if err != nil || got != want || ok != present {
		t.Fatalf("status %d = %q/%v (%v), want %q/%v", id, got, ok, err, want, present)
	}
}
func TestTransactionIdentityHistory(t *testing.T) {
	e := New()
	s, a := blockingUnitOwner(t, e)
	id := uint64(pgLocksUnitAssign(t, e, a))
	identityUnitStatus(t, e, id, "in progress", true)
	transactionEnd(t, s, a.Ref(), true)
	identityUnitStatus(t, e, id, "committed", true)
	s, b := blockingUnitOwner(t, e)
	aborted := uint64(pgLocksUnitAssign(t, e, b))
	transactionEnd(t, s, b.Ref(), false)
	identityUnitStatus(t, e, aborted, "aborted", true)
	if e.visibility.CommitTimestamps != nil {
		t.Fatal("trackingOFF allocated timestamp history")
	}
	for _, id := range []uint64{0, 4294967296, 18446744069414584320} {
		identityUnitStatus(t, e, id, "", false)
	}
	for _, id := range []uint64{1, 2, 4294967297, 4294967298, 18446744069414584321, 18446744069414584322} {
		identityUnitStatus(t, e, id, "committed", true)
	}
	e.mu.Lock()
	_, _, err := e.visibility.TransactionStatusLocked(math.MaxUint64)
	e.mu.Unlock()
	var state *sqlstate.Error
	if !errors.As(err, &state) || state.Code != "22023" || state.Message != "transaction ID 18446744073709551615 is in the future" {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.ReplaceCatalog(ctx, e, e.CatalogRead()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	identityUnitStatus(t, e, id, "committed", true)
	if _, err := e.ReplaceCatalog(context.Background(), e, e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	identityUnitStatus(t, e, id, "", false)
	identityUnitStatus(t, e, aborted, "", false)
	e.mu.Lock()
	gap := e.visibility.LastAssignedXID()
	e.visibility.SetLastAssignedXIDLocked(e.visibility.LastAssignedXID() + 2)
	e.mu.Unlock()
	identityUnitStatus(t, e, gap+1, "", false)
	identityUnitTimestampWrap(t)
}

func TestTransactionIdentityCompletion(t *testing.T) {
	e := New()
	_, a := blockingUnitOwner(t, e)
	id := uint64(pgLocksUnitAssign(t, e, a))
	e.mu.Lock()
	e.visibility.RecordCommitTimestampLocked(a.Ref())
	e.endSerializableLocked(a.Ref(), false)
	e.mu.Unlock()
	identityUnitStatus(t, e, id, "in progress", true)
	e.endLockOwner(a)
	identityUnitStatus(t, e, id, "committed", true)
	e.mu.Lock()
	e.endSerializableLocked(a.Ref(), false)
	e.mu.Unlock()
	identityUnitStatus(t, e, id, "committed", true)
	_, aborting := blockingUnitOwner(t, e)
	aborted := uint64(pgLocksUnitAssign(t, e, aborting))
	e.mu.Lock()
	e.endSerializableLocked(aborting.Ref(), false)
	e.mu.Unlock()
	identityUnitStatus(t, e, aborted, "in progress", true)
	e.endLockOwner(aborting)
	identityUnitStatus(t, e, aborted, "aborted", true)
	_, owner := blockingUnitOwner(t, e)
	id = uint64(pgLocksUnitAssign(t, e, owner))
	finisher := sessionStart(t, e, nil)
	op := transactionOperation(t, finisher)
	defer op.Done()
	entry := &preparedTransaction{gid: "identity_claimed", ref: owner.Ref(), ticket: owner, owner: finisher.authorization.state.CurrentUser, claimed: true}
	e.mu.Lock()
	e.prepared = map[string]*preparedTransaction{entry.gid: entry}
	before := e.visibility.Entries[owner.Ref()]
	e.mu.Unlock()
	err := e.completePrepared(op, entry.gid, true)
	var state *sqlstate.Error
	if !errors.As(err, &state) || state.Code != "55000" || state.Message != `prepared transaction with identifier "identity_claimed" is busy` {
		t.Fatal("busy claim", err)
	}
	e.mu.Lock()
	same := e.prepared[entry.gid] == entry && entry.claimed && e.visibility.Entries[owner.Ref()] == before
	delete(e.prepared, entry.gid)
	e.mu.Unlock()
	if !same {
		t.Fatal("busy completion changed actual owner")
	}
	identityUnitStatus(t, e, id, "in progress", true)
	t.Run("exact_phase_full_identity", func(t *testing.T) {
		phaseEngine := New()
		_, ticket := blockingUnitOwner(t, phaseEngine)
		pgLocksUnitAssign(t, phaseEngine, ticket)
		phaseEngine.mu.Lock()
		defer phaseEngine.mu.Unlock()
		phaseEngine.visibility.RecordCommitTimestampLocked(ticket.Ref())
		phaseEngine.visibility.ResetPhaseLocked(ticket.Ref())
		phaseEngine.visibility.SetLastAssignedXIDLocked(4294967295)
		if err := phaseEngine.assignTransactionXIDLocked(ticket.Ref()); err != nil {
			t.Fatal(err)
		}
		old, oldPresent, err := phaseEngine.visibility.TransactionStatusLocked(3)
		if err != nil || !oldPresent || old != "committed" {
			t.Fatal("low32 phase alias changed old completion", old, oldPresent, err)
		}
		current, present, err := phaseEngine.visibility.TransactionStatusLocked(4294967299)
		if err != nil || !present || current != "in progress" {
			t.Fatal("new full phase not active", current, present, err)
		}
	})
}

func TestTransactionIdentityBinding(t *testing.T) {
	ctx := context.Background()
	e := New()
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	for _, tc := range []struct {
		kind   transactionIdentityKind
		oid    uint32
		result sqltype.ID
	}{{identityAge, 1181, sqltype.Int4}, {identityMultiAge, 3939, sqltype.Int4}, {identityCurrent, 5059, sqltype.Xid8}, {identityAssigned, 5060, sqltype.Xid8}, {identityStatus, 5066, sqltype.Text}, {identityMembers, 3819, sqltype.ID(2249)}} {
		raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: tc.kind.name()}}}}
		if tc.kind != identityCurrent && tc.kind != identityAssigned {
			raw.Args = []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}}
		}
		var bound expr.Expr
		var err error
		if tc.kind == identityAge {
			raw.Args = []parser.Expr{ageFiniteTyped("xid", "0")}
			bound, err = b.bindFunctionProvider(raw)
		} else {
			bound, err = b.bindTransactionIdentity(raw)
		}
		if err != nil {
			t.Fatal(tc.kind, err)
		}
		n := bound.(transactionIdentityExpression)
		if err := n.validate(); err != nil {
			t.Fatal(err)
		}
		reference := n.CallRoutineReference()
		if reference == nil || reference.RoutineOID() != tc.oid {
			t.Fatal("original formal identity", tc.kind, reference)
		}
		if tc.kind != identityMembers && n.ResultUsage().Type.ID() != tc.result {
			t.Fatal("result usage", tc.kind)
		}
		intrinsic := expr.Stable
		if tc.kind == identityStatus || tc.kind == identityMembers {
			intrinsic = expr.Volatile
		}
		if plannedVolatility(n) != intrinsic || conditionalConstantInputs(n) || generationImmutable(n) {
			t.Fatal("unsafe constant/volatility admission", tc.kind)
		}
		count := 0
		boundExpressionChildren(n, func(expr.Expr) { count++ })
		if count != len(raw.Args) {
			t.Fatal("closed traversal", tc.kind, count)
		}
		recipe, err := (scalarRenderer{}).scalarFunctionSQL(ctx, e.CatalogRead(), n)
		if err != nil || !strings.Contains(recipe, "pg_catalog."+tc.kind.name()+"(") {
			t.Fatal("symbolic recipe", recipe, err)
		}
		persisted, err := remapBoundExpression(n, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
		if err != nil || !sameGroupExpression(n, persisted) {
			t.Fatal("symbolic remapping", err)
		}
		if err := validateGenerationPlan(e.CatalogRead(), nil, n); err != nil {
			t.Fatal("original provider", err)
		}
		captured, err := expressionRoutineDescriptor(n)
		if err != nil || captured == nil {
			t.Fatal("missing captured original routine", err)
		}
		e.mu.Lock()
		auth, err := e.authorizationLocked(b.op)
		e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if err := admitRoutineDescriptor(auth, b.dependencies, captured, nil); err != nil {
			t.Fatal("original routine manifest rejected", err)
		}
		if err := admitRoutineDescriptor(auth, nil, captured, nil); err == nil {
			t.Fatal("current OID repaired missing original routine manifest")
		}
		if n.arg != nil && tc.kind != identityAge {
			value, err := n.Eval(&EvalContext{Context: ctx})
			if err != nil || !value.IsNull() {
				t.Fatal("typed strict null", err)
			}
		}
		fresh := New()
		fb := newTestExpressionBinder(t, ctx, fresh, sqltype.InputEnvironment{}, nil)
		mapped, _, err := retagBoundTypes(fresh.CatalogRead(), n, func(d *sqltype.Descriptor) *sqltype.Descriptor {
			if d.IsComposite() {
				return d
			}
			return fb.typ(d.ID())
		})
		if err != nil || mapped.(transactionIdentityExpression).validate() != nil {
			t.Fatal("retag provider", err)
		}
		if !reflect.DeepEqual(functionCallArguments(n), functionCallArguments(persisted)) {
			t.Fatal("source argument reconstruction")
		}
	}
}
