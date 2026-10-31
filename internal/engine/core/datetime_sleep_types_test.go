package core_test

import (
	"context"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestDatetimeSleepClockOwner(t *testing.T) {
	e := engine.New()
	stamp, _ := e.CatalogRead().LookupType(sqltype.Timestamptz)
	oldTransaction := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	c := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Input: sqltype.InputEnvironment{TransactionStart: oldTransaction}}}
	before := time.Now().UTC().Truncate(time.Microsecond)
	current, err := c.FunctionSleepClock(stamp.Usage())
	after := time.Now().UTC()
	if err != nil {
		t.Fatal(err)
	}
	instant, err := current.TimestampTime()
	if err != nil || instant.Before(before) || instant.After(after) || c.Input.TransactionStart != oldTransaction {
		t.Fatal("clock used/mutated transaction start", instant, err)
	}
}

func TestDatetimeSleepStored(t *testing.T) {
	e := engine.New()
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	node, err := b.BindFunction(datetimeMakeRaw("pg_sleep", datetimeMakeInteger(0)))
	if err != nil {
		t.Fatal(err)
	}
	n := node.(functions.DatetimeSleep)
	if !analyze.ClosedTransactionTimeDefault(n) {
		t.Fatal("closed sleep default rejected")
	}
	target := engine.New()
	rebound, _, err := stored.RetagTypes(target.CatalogRead(), n, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		next, _ := target.CatalogRead().LookupType(d.ID())
		return next
	})
	if err != nil {
		t.Fatal("retag", err)
	}
	copy := rebound.(functions.DatetimeSleep)
	value, err := copy.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
	if err != nil || value.IsNull() || value.Type().ID() != sqltype.Void {
		t.Fatal("reminted zero delay", value, err)
	}
	if _, err := (stored.ScalarRenderer{}).SQL(context.Background(), e.CatalogRead(), copy); err == nil {
		t.Fatal("foreign generation accepted")
	}
	bad := n
	bad.Property.ParameterDependent = true
	if analyze.ClosedTransactionTimeDefault(bad) {
		t.Fatal("dependent default accepted")
	}
	interval, err := b.BindFunction(datetimeMakeRaw("pg_sleep_for", ageFiniteTyped("interval", "0")))
	if err != nil {
		t.Fatal(err)
	}
	rebound, _, err = stored.RetagTypes(target.CatalogRead(), interval, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		next, _ := target.CatalogRead().LookupType(d.ID())
		return next
	})
	if err != nil {
		t.Fatal("interval retag", err)
	}
	value, err = rebound.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
	if err != nil || value.IsNull() || value.Type().ID() != sqltype.Void {
		t.Fatal("reminted calendar zero delay", value, err)
	}
}
