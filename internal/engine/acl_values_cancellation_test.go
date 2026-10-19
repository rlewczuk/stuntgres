package engine

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
	e, c := aclValuesUnitContext(t)
	view := e.CatalogRead()
	d, _ := view.LookupType(sqltype.ACLItem)
	zero, err := sqltype.ACLItemValue(d, &e.roles.aclAuthority, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	items := make([]sqltype.Value, 8192)
	for i := range items {
		items[i] = zero
	}
	array := mustACLValuesArray(t, view, items...)
	candidate, err := sqltype.ACLItemValue(d, &e.roles.aclAuthority, 10, 10, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	original := c.Context
	calibration := &aclValuesScanContext{Context: original}
	c.Context = calibration
	if err := checkACLValue(c, array); err != nil {
		t.Fatal(err)
	}
	validationChecks := calibration.calls
	c.Context = original
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
			c.Context = scanning
			defer func() { c.Context = original }()
			var failure error
			if phase == "containment" {
				_, failure = aclValueContains(c, array, candidate)
			} else {
				failure = checkACLValue(c, array)
			}
			if !errors.Is(failure, context.Canceled) || !errors.Is(ctx.Err(), context.Canceled) {
				t.Fatalf("scan did not observe in-progress cancellation: %v", failure)
			}
			c.Context = original
			if err := checkACLValue(c, array); err != nil {
				t.Fatal("same-owner validation recovery", err)
			}
			found, err := aclValueContains(c, array, candidate)
			if err != nil || found {
				t.Fatal("same-owner containment recovery", found, err)
			}
		})
	}
	t.Run("zero_rights_expansion", func(t *testing.T) {
		shape := aclValuesUnitShape(t, view)
		cursor := aclExplodeCursor{array: array}
		progress := -1
		ctx, cancel := context.WithCancel(original)
		defer cancel()
		scanning := &aclValuesScanContext{Context: ctx, cancel: cancel, after: 3, observe: func() { progress = cursor.index }}
		_, present, err := cursor.next(scanning, shape)
		if !errors.Is(err, context.Canceled) || present || !cursor.closed || cursor.array.Type() != nil {
			t.Fatal("in-progress expansion cancellation/cleanup", present, err)
		}
		if progress <= 0 || progress >= array.ArrayLen() {
			t.Fatal("cancellation did not occur inside zero-rights scan", progress)
		}
		restarted := aclExplodeCursor{array: array}
		_, present, err = restarted.next(original, shape)
		if err != nil || present || !restarted.closed {
			t.Fatal("fresh invocation recovery", present, err)
		}
	})
}
