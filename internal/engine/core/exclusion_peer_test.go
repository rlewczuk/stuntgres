package core_test

import (
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqlstate"
)

func TestExclusionCompletionDeadlock(t *testing.T) {
	e := engine.New()
	a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE TABLE t(k INT,CONSTRAINT ex EXCLUDE(k WITH =) DEFERRABLE INITIALLY DEFERRED)")
	ar, br := enginetest.TransactionBegin(t, a), enginetest.TransactionBegin(t, b)
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
	enginetest.SessionSQL(t, b, "INSERT INTO t VALUES(2)")
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(2)")
	enginetest.SessionSQL(t, b, "INSERT INTO t VALUES(1)")
	at, bt := a.Transactions.Ticket, b.Transactions.Ticket
	if len(core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(a.Transactions)).Events) != 1 || len(core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(b.Transactions)).Events) != 1 {
		t.Fatal("crossed admissions must independently enlist both owners")
	}
	first := exclusionCommitWorker(t, a, ar)
	exclusionCompletionQueued(t, e, bt, at, first)
	second := exclusionCommitWorker(t, b, br)
	var left, right error
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for first != nil || second != nil {
		select {
		case left = <-first:
			first = nil
		case right = <-second:
			second = nil
		case <-timer.C:
			t.Fatal("closed completion cycle did not resolve")
		}
	}
	if (left == nil) == (right == nil) {
		t.Fatal("want exactly one actual deadlock victim", left, right)
	}
	victim := left
	if victim == nil {
		victim = right
	}
	var diagnostic *sqlstate.Error
	if !errors.As(victim, &diagnostic) || diagnostic.Code != "40P01" {
		t.Fatal("completion cycle did not use existing deadlock authority", victim)
	}
	if e.Exclusions.CountLocked() != 0 {
		t.Fatal("resolved deadlock retained dirty owners")
	}
	btreeWantKeys(t, e, "ex", "1", "2")
	uniqueClaimsEmpty(t, e)
}
