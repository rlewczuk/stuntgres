package core_test

import (
	"context"
	"errors"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
)

func TestAmcheckRestrictedCallbacks(t *testing.T) {
	for _, mode := range []string{"success", "error", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			d, err := settings.Validate(settings.Definition{Name: "demo.amcheck_callback", Type: settings.String, BootDefault: "boot"})
			if err != nil {
				t.Fatal(err)
			}
			var e *core.Engine
			var s *session.Session
			var op *session.Operation
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			primary := errors.New("normalizer rejected value")
			entered := false
			d.Normalize = func(input string) (string, *sqlstate.Error, error) {
				if input != "transient" {
					return input, nil, nil
				}
				if op == nil || op.Restricted == nil {
					return "", nil, errors.New("missing invocation environment")
				}
				// These acquisitions prove callbacks do not borrow publication,
				// session, live-settings or detached-image mutexes.
				e.Mu.Lock()
				e.Mu.Unlock()
				s.Mu.Lock()
				s.Mu.Unlock()
				s.Settings.Mu.Lock()
				s.Settings.Mu.Unlock()
				image := op.EffectiveSettings()
				image.Mu.Lock()
				image.Mu.Unlock()
				entered = true
				if mode == "error" {
					return "", nil, primary
				}
				if mode == "cancel" {
					cancel()
				}
				return "normalized", nil, nil
			}
			e = engine.NewWithSettings([]settings.Definition{d}, "", false)
			enginetest.Run(t, e, "CREATE TABLE callback_parent(n INT)")
			parent := e.Catalog.State.MustRelation(enginetest.RoleTableRef(t, e, "callback_parent").Address)
			s = sessionStart(t, e, nil)
			op, err = s.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(nil)
			if err := scope.Gate(enginetest.Statement(t, "SELECT 1")); err != nil {
				t.Fatal(err)
			}
			err = scope.WithAmcheckOwner(parent, func(*exec.Context) error {
				result, err := core.EngineExecuteStatement(e, scope, enginetest.Statement(t, "SET demo.amcheck_callback=transient"), 0, nil)
				if result.Rows != nil {
					result.Rows.Close()
				}
				value, _ := op.Setting(d.Name)
				if mode == "success" && (err != nil || value != "normalized") {
					t.Fatal("normalizer output not installed locally", value, err)
				}
				return err
			})
			if !entered {
				t.Fatal("normalizer was not invoked")
			}
			if mode == "success" && err != nil || mode == "error" && !errors.Is(err, primary) || mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if value, _ := s.Setting(d.Name); value != "boot" || op.Restricted != nil {
				t.Fatal("normalizer leaked invocation state", value)
			}
		})
	}
}
