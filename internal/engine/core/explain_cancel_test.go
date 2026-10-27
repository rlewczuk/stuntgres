package core_test

import (
	"context"
	"runtime"
	"strings"
)

// Select the existing cooperative context boundary inside the real renderer,
// without sleeps, goroutines, production hooks or a fabricated plan provider.
type explainRenderCancelContext struct {
	context.Context
	cancel   context.CancelFunc
	observed bool
}

func (c *explainRenderCancelContext) Err() error {
	if !c.observed {
		var pcs [32]uintptr
		frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs[:])])
		for {
			frame, more := frames.Next()
			if strings.HasSuffix(frame.Function, ".(*explainRenderer).node") {
				c.observed = true
				c.cancel()
				break
			}
			if !more {
				break
			}
		}
	}
	return c.Context.Err()
}
