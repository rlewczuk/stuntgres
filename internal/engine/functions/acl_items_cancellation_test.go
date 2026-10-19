package functions

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/sqltype"
)

// Cancel a real context after an initially successful progress observation.
// No goroutines, sleeps, fabricated Err result or production hook is needed.
// Calibration places cancellation within a scan, not at its entry boundary;
// assertions concern cancellation/recovery, never an exact polling count.
type aclValuesScanContext struct {
	context.Context
	cancel       context.CancelFunc
	calls, after int
	observe      func()
}

func (c *aclValuesScanContext) Err() error {
	err := c.Context.Err()
	c.calls++
	if c.cancel != nil && c.calls == c.after && err == nil {
		if c.observe != nil {
			c.observe()
		}
		c.cancel()
	}
	return err
}

func TestACLValuesScanCancellation(t *testing.T) {
	runtime := newACLTestRuntime()
	zero := aclTestItem(t, runtime.authority, 0, 0, 0, 0)
	items := make([]sqltype.Value, 8192)
	for i := range items {
		items[i] = zero
	}
	array := aclTestArray(t, items...)
	candidate := aclTestItem(t, runtime.authority, 10, 10, 2, 0)
	original := context.Background()
	calibration := &aclValuesScanContext{Context: original}
	if err := checkACLValue(calibration, runtime, array); err != nil {
		t.Fatal(err)
	}
	validationChecks := calibration.calls
	for _, phase := range []string{"null_validation", "authority_validation", "containment"} {
		t.Run(phase, func(t *testing.T) {
			after := max(2, validationChecks/4)
			if phase == "authority_validation" {
				after = max(2, validationChecks*3/4)
			}
			if phase == "containment" {
				after = validationChecks + 2
			}
			ctx, cancel := context.WithCancel(original)
			defer cancel()
			scanning := &aclValuesScanContext{Context: ctx, cancel: cancel, after: after}
			var failure error
			if phase == "containment" {
				_, failure = aclContains(scanning, runtime, array, candidate)
			} else {
				failure = checkACLValue(scanning, runtime, array)
			}
			if !errors.Is(failure, context.Canceled) || !errors.Is(ctx.Err(), context.Canceled) {
				t.Fatalf("scan did not observe in-progress cancellation: %v", failure)
			}
			if err := checkACLValue(original, runtime, array); err != nil {
				t.Fatal("same-owner validation recovery", err)
			}
			found, err := aclContains(original, runtime, array, candidate)
			if err != nil || found {
				t.Fatal("same-owner containment recovery", found, err)
			}
		})
	}
	t.Run("zero_rights_expansion", func(t *testing.T) {
		shape := aclTestShape(t)
		cursor := ACLExplodeCursor{array: array}
		progress := -1
		ctx, cancel := context.WithCancel(original)
		defer cancel()
		scanning := &aclValuesScanContext{Context: ctx, cancel: cancel, after: 3, observe: func() { progress = cursor.index }}
		_, present, err := cursor.Next(scanning, shape)
		if !errors.Is(err, context.Canceled) || present || !cursor.closed || cursor.array.Type() != nil {
			t.Fatal("in-progress expansion cancellation/cleanup", present, err)
		}
		if progress <= 0 || progress >= array.ArrayLen() {
			t.Fatal("cancellation did not occur inside zero-rights scan", progress)
		}
		restarted := ACLExplodeCursor{array: array}
		_, present, err = restarted.Next(original, shape)
		if err != nil || present || !restarted.closed {
			t.Fatal("fresh invocation recovery", present, err)
		}
	})
}
