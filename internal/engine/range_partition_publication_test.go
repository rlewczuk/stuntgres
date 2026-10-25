package engine

import (
	"context"
	"runtime"
	"strings"
	"sync/atomic"
)

// Observe a real family checkpoint, not whole-pipeline Err counts or a fake
// scalar provider. Cancellation and reset still use their actual authorities.
type rangePartitionPhaseContext struct {
	context.Context
	target  string
	effect  func()
	entered atomic.Bool
}

func (c *rangePartitionPhaseContext) Err() error {
	if !c.entered.Load() {
		var pcs [48]uintptr
		frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs[:])])
		for {
			frame, more := frames.Next()
			if strings.Contains(frame.Function, c.target) {
				if c.entered.CompareAndSwap(false, true) && c.effect != nil {
					c.effect()
				}
				break
			}
			if !more {
				break
			}
		}
	}
	return c.Context.Err()
}
