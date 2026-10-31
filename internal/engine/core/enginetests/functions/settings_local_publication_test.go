package functions_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestSettingsLocalNormalizerPublication(t *testing.T) {
	for _, mode := range []string{"cancel", "replace", "inspect"} {
		t.Run(mode, func(t *testing.T) {
			d, err := settings.Validate(settings.Definition{Name: "demo.normalizer", Type: settings.String, BootDefault: "b"})
			if err != nil {
				t.Fatal(err)
			}
			var e *core.Engine
			var s *session.Session
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			// Attach the witness before publication; defaults cannot trigger it.
			d.Normalize = func(input string) (string, *sqlstate.Error, error) {
				if input != "transient" {
					return input, nil, nil
				}
				switch mode {
				case "cancel":
					cancel()
				case "replace":
					if err := engine.Reset(context.Background(), e); err != nil {
						return "", nil, err
					}
				case "inspect":
					// Taking these locks proves staging did not retain publication locks.
					e.Mu.Lock()
					e.Mu.Unlock()
					s.Settings.Mu.Lock()
					s.Settings.Mu.Unlock()
					s.Mu.Lock()
					s.Mu.Unlock()
				}
				return input, nil, nil
			}
			e = engine.NewWithSettings([]settings.Definition{d}, "", false)
			s = enginetest.StartSession(t, e, nil)
			op, err := s.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			result, err := scope.Execute(enginetest.Statement(t, "SELECT set_config('demo.normalizer','transient',true)"), 0, nil)
			finish := scope.Finish(err)
			if mode == "cancel" {
				if !errors.Is(err, context.Canceled) || !errors.Is(finish, context.Canceled) {
					t.Fatal(err, finish)
				}
			} else if mode == "replace" {
				// Ordinary SELECT retains its transaction publication fence:
				// a reentrant Reset must not publish a result owned by the old epoch.
				if err != nil || !errors.Is(finish, ids.ErrCatalogReplaced) {
					t.Fatal(err, finish)
				}
				if result.Rows != nil {
					result.Rows.Close()
				}
			} else {
				if err != nil || finish != nil {
					t.Fatal(err, finish)
				}
				values := make([]sqltype.Value, 1)
				present, nextErr := result.Rows.Next(context.Background(), values)
				value, eerr := values[0].Text()
				if !present || nextErr != nil || eerr != nil || value != "transient" {
					t.Fatal(present, nextErr, value, eerr)
				}
				result.Rows.Close()
			}
			localOwnerValue(t, s, "demo.normalizer", "b")
		})
	}
}

func localOwnerValue(t *testing.T, s *session.Session, name, want string) {
	t.Helper()
	s.Settings.Mu.Lock()
	defer s.Settings.Mu.Unlock()
	if got := s.Settings.Value(name); got != want {
		t.Fatalf("%s got=%q want=%q", name, got, want)
	}
	if len(s.Settings.Actions) != 0 {
		t.Fatal("retained transactional actions", s.Settings.Actions)
	}
}
