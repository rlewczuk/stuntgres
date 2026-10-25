package engine

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/visibility"
)

// Err is an actual callback demanded by the typed comparison provider. It
// observes lock ownership at that boundary, and can return the original sentinel
// there. It supplies neither rows, comparison answers nor branch selections.
type pruningProviderContext struct {
	context.Context
	engine        *core.Engine
	failure       error
	cancel        context.CancelFunc
	providerCalls int
	locked        bool
}

func (c *pruningProviderContext) Err() error {
	if c.engine.Mu.TryLock() {
		c.engine.Mu.Unlock()
	} else {
		c.locked = true
	}
	var pcs [24]uintptr
	n := runtime.Callers(2, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		if strings.Contains(frame.Function, "sqltype.(*IndexComparison).Compare") {
			c.providerCalls++
			if c.cancel != nil {
				c.cancel()
			}
			if c.failure != nil {
				return c.failure
			}
			break
		}
		if !more {
			break
		}
	}
	return c.Context.Err()
}

func TestPartitionPruningSafety(t *testing.T) {
	for _, mode := range []string{"success", "provider_error", "provider_cancel"} {
		t.Run(mode, func(t *testing.T) {
			e := New()
			for _, q := range []string{
				"CREATE TABLE pp(k INT) PARTITION BY RANGE(k)",
				"CREATE TABLE pp_a PARTITION OF pp FOR VALUES FROM(1) TO(3)",
				"CREATE TABLE pp_b PARTITION OF pp FOR VALUES FROM(3) TO(5)",
				"INSERT INTO pp VALUES(1),(1),(2),(3),(4)",
			} {
				run(t, e, q)
			}
			query, source, release := hashImplicitQuery(t, e, "SELECT k FROM pp WHERE k=1")
			defer release()
			views := []*visibility.View{source.View}
			for _, segment := range source.Inheritance {
				views = append(views, segment.Prefix.View)
			}
			project := query.Root.(*plan.ProjectNode)
			state := &exec.State{Source: source, Frame: project.Frame}
			if scope := core.ScopeOf(source.LiveHost()); scope != nil {
				state.Input, state.Output = scope.Input, scope.Output
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			provider := &pruningProviderContext{Context: ctx, engine: e}
			sentinel := errors.New("original pruning comparison callback error")
			if mode == "provider_error" {
				provider.failure = sentinel
			}
			if mode == "provider_cancel" {
				provider.cancel = cancel
			}
			stage, err := exec.OpenNode(provider, state, project.Child)
			if provider.locked || provider.providerCalls == 0 {
				stage.Close()
				t.Fatalf("provider did not run unlocked: calls=%d locked=%v", provider.providerCalls, provider.locked)
			}
			switch mode {
			case "success":
				if err != nil {
					t.Fatal(err)
				}
				if err := stage.Drain(ctx); err != nil || stage.Count() != 2 {
					t.Fatal("retained duplicate/residual result", stage.Count(), err)
				}
			case "provider_error":
				if !errors.Is(err, sentinel) {
					t.Fatal("provider error was replaced", err)
				}
			case "provider_cancel":
				if !errors.Is(err, context.Canceled) {
					t.Fatal("provider cancellation was replaced", err)
				}
			}
			stage.Close()
			// Failed proof opening does not steal admission ownership. Source
			// release closes every actual captured visibility handle, idempotently.
			for _, view := range views {
				if view == nil || view.Load() == nil {
					t.Fatal("proof stole the captured source lifetime")
				}
			}
			release()
			for _, view := range views {
				if view.Load() != nil {
					t.Fatal("captured visibility pin survived release")
				}
			}
			source.Close()
		})
	}
}
