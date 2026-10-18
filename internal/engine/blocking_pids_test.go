package engine

import (
	"context"
	"errors"
	"math"
	"reflect"
	"sort"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func blockingUnitOwner(t *testing.T, e *Engine) (*Session, *relationTicket) {
	t.Helper()
	s := sessionStart(t, e, nil)
	transactionBegin(t, s)
	return s, s.transactions.ticket
}

func blockingUnitResource(e *Engine) lockResource {
	return lockResource{ObjectRef: e.domain.Ref(e.state.epoch, ids.ObjectAddress{ClassID: RelationClassID, ObjectID: 999999})}
}

func blockingUnitType(e *Engine) *sqltype.Descriptor {
	typ, _ := e.CatalogRead().LookupType(sqltype.Int4)
	return typ
}

func blockingUnitWant(t *testing.T, e *Engine, target SessionID, want ...int64) []sqltype.Value {
	t.Helper()
	values, err := e.locks.blockingPids(target, blockingUnitType(e))
	if err != nil {
		t.Fatal(err)
	}
	got := make([]int64, len(values))
	for i, v := range values {
		got[i], err = v.Integer()
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	if len(got) != len(want) || len(want) != 0 && !reflect.DeepEqual(got, want) {
		t.Fatal("blocker bag", got, want)
	}
	return values
}

func TestBlockingPidsBindingAndIdentity(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	query := domainUnitBindSelect(t, e, op, "SELECT pg_blocking_pids($1),pg_safe_snapshot_blocking_pids(NULL)")
	expressions := query.Root.(*projectNode).expressions
	for i, raw := range expressions {
		n, ok := raw.(blockingInquiryExpression)
		if !ok || n.kind != blockingInquiryKind(i) || n.arg.ResultUsage().Type != blockingUnitType(e) || n.ResultUsage().Type.ID() != 1007 || n.ResultUsage().Type.Element() != n.arg.ResultUsage().Type || n.Properties().Volatility != Volatile || n.validateStored() != nil {
			t.Fatal("typed inquiry", raw)
		}
		if i == 0 && !n.Properties().ParameterDependent {
			t.Fatal("lost parameter inference")
		}
		if i == 1 {
			value, err := n.Eval(&EvalContext{Context: context.Background()})
			if err != nil || !value.IsNull() || value.Type() != n.ResultUsage().Type {
				t.Fatal("strict typed NULL", value, err)
			}
		}
	}
	// Demand the same bound recipe across manager changes, not merely two
	// separately bound queries. The captured array remains independently owned.
	owner, holder := blockingUnitOwner(t, e)
	waiter, waiting := blockingUnitOwner(t, e)
	resource := blockingUnitResource(e)
	if err := e.locks.acquire(context.Background(), holder, resource, accessExclusive); err != nil {
		t.Fatal(err)
	}
	request := deadlockRequest(t, e, waiting, resource, accessExclusive)
	if request == nil {
		t.Fatal("missing live evaluation wait")
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(context.Canceled)
	inquiry := expressions[0].(blockingInquiryExpression)
	input, _ := sqltype.Integer(blockingUnitType(e), int64(waiter.token.ID()))
	inquiry.arg = literalExpression{expressionInfo: expressionInfo{usage: input.Type().Usage()}, value: input}
	inquiry.properties = ExprProperties{Volatility: Volatile}
	evaluation := &EvalContext{Context: context.Background(), statementScope: scope}
	before, err := inquiry.Eval(evaluation)
	if err != nil || before.IsNull() || before.ArrayLen() != 1 {
		t.Fatal("live inquiry", before, err)
	}
	pid, _ := before.ArrayElement(0).Integer()
	if pid != int64(owner.token.ID()) {
		t.Fatal("physical blocker mapping", pid)
	}
	e.endLockOwner(holder)
	if err := deadlockResult(t, request); err != nil {
		t.Fatal(err)
	}
	after, err := inquiry.Eval(evaluation)
	if err != nil || after.IsNull() || after.ArrayLen() != 0 || len(after.ArrayDimensions()) != 0 || before.ArrayLen() != 1 {
		t.Fatal("cached or aliased inquiry", before, after, err)
	}
	pruned := domainUnitBindSelect(t, e, op, "SELECT CASE WHEN false THEN pg_blocking_pids(1/0) ELSE '{}'::integer[] END").Root.(*projectNode).expressions[0]
	planned, _, err := preflightCoercions(evaluation, pruned)
	if err != nil {
		t.Fatal("undemanded branch preflight", err)
	}
	if value, err := planned.Eval(evaluation); err != nil || value.IsNull() || value.ArrayLen() != 0 {
		t.Fatal("undemanded branch evaluated", value, err)
	}
	if values, err := appendBlockingPid(nil, blockingUnitType(e), New().domain.Session(7), false, e.domain); err != nil || len(values) != 0 {
		t.Fatal("foreign owner exposed", values, err)
	}
	if values, err := appendBlockingPid(nil, blockingUnitType(e), e.domain.PrivateSession(s), false, e.domain); err != nil || len(values) != 0 {
		t.Fatal("private owner exposed", values, err)
	}
	if _, err := appendBlockingPid(nil, blockingUnitType(e), e.domain.Session(math.MaxInt32+1), false, e.domain); err == nil || err.Error() != "stuntgres: blocking session identity exceeds integer range" {
		t.Fatal("unchecked PID projection", err)
	}
	zeros, err := appendBlockingPid(nil, blockingUnitType(e), e.domain.Session(math.MaxInt32+1), true, e.domain)
	if err != nil || len(zeros) != 1 {
		t.Fatal("prepared projection used physical ID", zeros, err)
	}
	zero, _ := zeros[0].Integer()
	if zero != 0 {
		t.Fatal(zero)
	}
}

func TestBlockingPidsOrdinaryAuthority(t *testing.T) {
	t.Run("hard_soft_single_participant_and_owned_results", func(t *testing.T) {
		e := New()
		a, at := blockingUnitOwner(t, e)
		b, bt := blockingUnitOwner(t, e)
		c, ct := blockingUnitOwner(t, e)
		resource := blockingUnitResource(e)
		if err := e.locks.acquire(context.Background(), at, resource, accessShare); err != nil {
			t.Fatal(err)
		}
		if err := e.locks.acquire(context.Background(), bt, resource, accessShare); err != nil {
			t.Fatal(err)
		}
		// A is both a conflicting holder and an earlier queued upgrader for C.
		ar := deadlockRequest(t, e, at, resource, accessExclusive)
		cr := deadlockRequest(t, e, ct, resource, accessExclusive)
		if ar == nil || cr == nil {
			t.Fatal("missing actual upgrade queue")
		}
		before := blockingUnitWant(t, e, c.token.ID(), int64(a.token.ID()), int64(b.token.ID()))
		e.endLockOwner(bt)
		if err := deadlockResult(t, ar); err != nil {
			t.Fatal(err)
		}
		blockingUnitWant(t, e, c.token.ID(), int64(a.token.ID()))
		if len(before) != 2 {
			t.Fatal("snapshot aliases manager", before)
		}
		e.endLockOwner(at)
		if err := deadlockResult(t, cr); err != nil {
			t.Fatal(err)
		}
		blockingUnitWant(t, e, c.token.ID())
	})
	t.Run("compatible_holder_soft_predecessor", func(t *testing.T) {
		e := New()
		a, at := blockingUnitOwner(t, e)
		b, bt := blockingUnitOwner(t, e)
		c, ct := blockingUnitOwner(t, e)
		resource := blockingUnitResource(e)
		if err := e.locks.acquire(context.Background(), at, resource, accessShare); err != nil {
			t.Fatal(err)
		}
		br := deadlockRequest(t, e, bt, resource, accessExclusive)
		cr := deadlockRequest(t, e, ct, resource, accessShare)
		if br == nil || cr == nil {
			t.Fatal("missing soft queue")
		}
		blockingUnitWant(t, e, b.token.ID(), int64(a.token.ID()))
		blockingUnitWant(t, e, c.token.ID(), int64(b.token.ID()))
		e.endLockOwner(bt)
		if !errors.Is(deadlockResult(t, br), errRelationOwnerEnded) {
			t.Fatal("ended requester not terminated")
		}
		if err := deadlockResult(t, cr); err != nil {
			t.Fatal(err)
		}
		blockingUnitWant(t, e, c.token.ID())
	})
	t.Run("prepared_zeros_and_origin_reuse", func(t *testing.T) {
		e := New()
		a, at := blockingUnitOwner(t, e)
		_, bt := blockingUnitOwner(t, e)
		c, ct := blockingUnitOwner(t, e)
		resource := blockingUnitResource(e)
		for _, owner := range []*relationTicket{at, bt} {
			if err := e.locks.acquire(context.Background(), owner, resource, accessShare); err != nil {
				t.Fatal(err)
			}
			e.locks.detachPrepared(owner)
		}
		cr := deadlockRequest(t, e, ct, resource, accessExclusive)
		if cr == nil {
			t.Fatal("prepared owners did not block")
		}
		blockingUnitWant(t, e, c.token.ID(), 0, 0)
		blockingUnitWant(t, e, a.token.ID())
		e.endLockOwner(at)
		blockingUnitWant(t, e, c.token.ID(), 0)
		e.endLockOwner(bt)
		if err := deadlockResult(t, cr); err != nil {
			t.Fatal(err)
		}
		blockingUnitWant(t, e, c.token.ID())
	})
	t.Run("full_width_target_and_blocker_identity", func(t *testing.T) {
		e := New()
		low, lt := blockingUnitOwner(t, e)
		high, err := e.StartSession(context.Background(), SessionStart{ID: SessionID(uint64(1)<<32) + math.MaxInt32})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			high.RequestEnd(CleanClose)
			if err := high.WaitEnd(context.Background()); err != nil {
				t.Error(err)
			}
		})
		transactionBegin(t, high)
		ht := high.transactions.ticket
		resource := blockingUnitResource(e)
		if err := e.locks.acquire(context.Background(), lt, resource, accessExclusive); err != nil {
			t.Fatal(err)
		}
		request := deadlockRequest(t, e, ht, resource, accessExclusive)
		if request == nil {
			t.Fatal("high identity did not queue")
		}
		blockingUnitWant(t, e, math.MaxInt32) // must not truncate high target
		blockingUnitWant(t, e, high.token.ID(), int64(low.token.ID()))
		e.endLockOwner(lt)
		if err := deadlockResult(t, request); err != nil {
			t.Fatal(err)
		}
		// A high live blocker is a truthful ordinary range failure, not zero,
		// a wrapped alias or silent omission.
		_, next := blockingUnitOwner(t, e)
		request = deadlockRequest(t, e, next, resource, accessExclusive)
		if request == nil {
			t.Fatal("high holder did not block")
		}
		if _, err := e.locks.blockingPids(next.session.ID(), blockingUnitType(e)); err == nil || err.Error() != "stuntgres: blocking session identity exceeds integer range" {
			t.Fatal("high live blocker", err)
		}
		e.endLockOwner(ht)
		if err := deadlockResult(t, request); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("completion_and_row_eligibility", func(t *testing.T) {
		for _, completion := range []bool{false, true} {
			e := New()
			a, at := blockingUnitOwner(t, e)
			_, bt := blockingUnitOwner(t, e)
			c, ct := blockingUnitOwner(t, e)
			resource := blockingUnitResource(e)
			first, later := rowUpdate, noKeyUpdate
			if completion {
				resource = lockResource{completion: at}
				first, later = transactionCompletion, transactionCompletion
			} else {
				resource.row = 1
				if err := e.locks.acquire(context.Background(), at, resource, keyShare); err != nil {
					t.Fatal(err)
				}
			}
			br := deadlockRequest(t, e, bt, resource, first)
			cr := deadlockRequest(t, e, ct, resource, later)
			if br == nil {
				t.Fatal("missing completion/tuple waiter")
			}
			if completion {
				if cr == nil {
					t.Fatal("missing completion waiter")
				}
				blockingUnitWant(t, e, c.token.ID(), int64(a.token.ID()))
			} else {
				if cr != nil {
					t.Fatal("NO KEY UPDATE incorrectly soft blocked")
				}
				blockingUnitWant(t, e, c.token.ID())
			}
			e.endLockOwner(at)
			e.endLockOwner(bt)
			e.endLockOwner(ct)
		}
	})
}

func TestBlockingPidsLifecycle(t *testing.T) {
	for _, fence := range []bool{false, true} {
		e := New()
		a, at := blockingUnitOwner(t, e)
		b, bt := blockingUnitOwner(t, e)
		resource := blockingUnitResource(e)
		if err := e.locks.acquire(context.Background(), at, resource, accessExclusive); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		r, err := e.locks.startAcquire(ctx, bt, resource, accessExclusive, relationLockPolicy{})
		if err != nil || r == nil {
			t.Fatal(r, err)
		}
		blockingUnitWant(t, e, b.token.ID(), int64(a.token.ID()))
		// A pending pointer without current journal ownership is stale, not
		// an alternate source of wait state. Inquiry must not settle or edit it.
		e.locks.mu.Lock()
		delete(bt.resources, resource)
		e.locks.mu.Unlock()
		blockingUnitWant(t, e, b.token.ID())
		e.locks.mu.Lock()
		bt.resources[resource] = struct{}{}
		unchanged := !r.terminal && len(r.entry.pending) == 1 && r.entry.pending[0] == r
		e.locks.mu.Unlock()
		if !unchanged {
			t.Fatal("read-only inquiry mutated queue")
		}
		if fence {
			e.locks.fence(bt.ref.Epoch() + 1)
		} else {
			cancel()
		}
		err = deadlockResult(t, r)
		if fence && !errors.Is(err, ErrCatalogReplaced) || !fence && !errors.Is(err, context.Canceled) {
			t.Fatal("wrong wait terminal", err)
		}
		blockingUnitWant(t, e, b.token.ID())
		// A saved terminal request never becomes an observable wait again.
		e.locks.mu.Lock()
		entry := e.locks.entries[resource]
		if entry != nil {
			entry.pending = append(entry.pending, r)
		}
		e.locks.mu.Unlock()
		blockingUnitWant(t, e, b.token.ID())
	}
}
