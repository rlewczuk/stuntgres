package core_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
)

type concurrentUnitSink struct{ calls, bytes int }

func (w *concurrentUnitSink) Write(data []byte) (int, error) {
	w.calls++
	w.bytes += len(data)
	return len(data), nil
}

// All waits are observed through the actual manager queue. Register cancellation
// and joining after session cleanup so workers finish before their sessions end.
func indexMaintenanceAsync(t *testing.T, e *core.Engine, s *session.Session, q string) (context.CancelFunc, func() error) {
	t.Helper()
	stmt := enginetest.Statement(t, q)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	joined := false
	go func() {
		op, err := s.Acquire(ctx)
		if err == nil {
			result, executeErr := e.Execute(op, stmt, 0, nil)
			err = executeErr
			if result.Rows != nil {
				result.Rows.Close()
			}
			op.Done()
		}
		done <- err
	}()
	t.Cleanup(func() {
		cancel()
		if !joined {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("index utility did not terminate during cleanup")
			}
		}
	})
	return cancel, func() error {
		t.Helper()
		select {
		case err := <-done:
			joined = true
			return err
		case <-time.After(5 * time.Second):
			t.Fatal("index utility did not terminate")
			return nil
		}
	}
}

// Reduced storage fixtures still need real family members and genuine callable
// declarations. Share an immutable ordinary engine bootstrap, never a provider
// fallback or a test callback.
func operatorFamilyTestRead() catalog.Read { return operatorFamilyUnitSeed() }

var operatorFamilyUnitSeed = sync.OnceValue(func() catalog.Read { return engine.New().CatalogRead() })
