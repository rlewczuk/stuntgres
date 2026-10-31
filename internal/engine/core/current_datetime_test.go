package core_test

import (
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/parser"
)

// Function-level lifecycle seams, not another public SQL scenario. AST controls
// and the admission gate exercise the same owner birth functions as SQL/driver.
func TestCurrentDateTimeTransactionBirthQuantization(t *testing.T) {
	e := engine.New()
	session := enginetest.StartSession(t, e, nil)
	assertClock := func(stamp time.Time) {
		t.Helper()
		if stamp.IsZero() || stamp.Location() != time.UTC || stamp.Nanosecond()%1000 != 0 {
			t.Fatal("owner clock not quantized UTC", stamp)
		}
	}
	scopeFor := func() *core.StatementScope {
		t.Helper()
		op := enginetest.TransactionOperation(t, session)
		scope, err := e.StartStatement(op)
		if err != nil {
			op.Done()
			t.Fatal(err)
		}
		return scope
	}
	finish := func(scope *core.StatementScope, err error) {
		t.Helper()
		err = scope.Finish(err)
		scope.Op.Done()
		if err != nil {
			t.Fatal(err)
		}
	}
	advance := func(stamp time.Time) {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for !time.Now().After(stamp.Add(time.Microsecond)) {
			if time.Now().After(deadline) {
				t.Fatal("clock did not advance", stamp)
			}
			runtime.Gosched()
		}
	}
	implicit := scopeFor()
	if err := implicit.Gate(&parser.Select{}); err != nil {
		finish(implicit, err)
	}
	first := implicit.Input.TransactionStart
	assertClock(first)
	finish(implicit, nil)
	if !session.Transactions.Start.IsZero() {
		t.Fatal("implicit clock retained after end")
	}
	advance(first)
	ref := enginetest.TransactionBegin(t, session)
	owner := session.Transactions
	outer := owner.Start
	assertClock(outer)
	if !outer.After(first) {
		t.Fatal("driver birth did not refresh", first, outer)
	}
	owner.Mu.Lock()
	mark := core.CaptureSavepoint(owner, "clock")
	core.RestoreSavepoint(owner, mark, mark.Settings.Clone())
	restored := owner.Start
	owner.Mu.Unlock()
	if !restored.Equal(outer) {
		t.Fatal("savepoint rebirthed clock", outer, restored)
	}
	enginetest.TransactionEnd(t, session, ref, false)
	advance(outer)
	start := scopeFor()
	_, err := core.ScopeExecuteControl(start, &parser.TransactionControl{Kind: parser.KindStart}, 0)
	finish(start, err)
	sqlClock := owner.Start
	assertClock(sqlClock)
	if !sqlClock.After(outer) {
		t.Fatal("SQL birth did not refresh", sqlClock, outer)
	}
	advance(sqlClock)
	chain := scopeFor()
	_, err = core.ScopeExecuteControl(chain, &parser.TransactionControl{Kind: parser.KindCommit, Chain: true}, 0)
	finish(chain, err)
	assertClock(owner.Start)
	if !owner.Start.After(sqlClock) {
		t.Fatal("CHAIN birth did not refresh", owner.Start, sqlClock)
	}
	enginetest.TransactionEnd(t, session, owner.Ref, false)
}
