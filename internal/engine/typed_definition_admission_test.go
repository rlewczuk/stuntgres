package engine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
)

// A typed-source grant alone permits callback replacement. A genuine retained
// DEFAULT provider instead owns the existing unpublished-definition reservation.
// Isolate failures so a broken reservation cannot strand the parent suite.
func TestTypedPublicationDefinitionProviderReservation(t *testing.T) {
	if os.Getenv("STUNTGRES_TYPED_DEFINITION_HOLD_CHILD") != "1" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, "-test.run=^TestTypedPublicationDefinitionProviderReservation$", "-test.count=1", "-test.timeout=10s")
		cmd.Env = append(os.Environ(), "STUNTGRES_TYPED_DEFINITION_HOLD_CHILD=1")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("typed genuine provider reservation failed: %v\n%s", err, output)
		}
		return
	}
	for _, temporary := range []bool{false, true} {
		t.Run(map[bool]string{false: "persistent", true: "temporary"}[temporary], func(t *testing.T) {
			e, _, _ := typedUnitCatalog(t, false)
			run(t, e, "CREATE SEQUENCE q")
			before := e.CatalogRead().state
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reset := make(chan error, 1)
			original := e.catalog.providers[ids.RelationClassID]
			called := false
			e.catalog.providers[ids.RelationClassID] = alterDropProvider{original, func() {
				if called {
					return
				}
				called = true
				e.catalog.providers[ids.RelationClassID] = original
				go func() { reset <- e.Reset(context.Background()) }()
				deadline := time.Now().Add(3 * time.Second)
				for {
					e.replacementMu.mu.Lock()
					queued := e.replacementMu.readers == 1 && e.replacementMu.writers == 1 && !e.replacementMu.writing
					e.replacementMu.mu.Unlock()
					if queued {
						break
					}
					if time.Now().After(deadline) {
						cancel()
						t.Fatal("replacement did not queue behind genuine typed DEFAULT provider reservation")
					}
					runtime.Gosched()
				}
				if e.CatalogRead().state != before {
					cancel()
					t.Fatal("queued replacement changed unpublished typed definition")
				}
				cancel()
			}}
			query := "CREATE TABLE late OF pair(id WITH OPTIONS DEFAULT nextval('q'))"
			if temporary {
				query = "CREATE TEMP TABLE late OF pair(id WITH OPTIONS DEFAULT nextval('q'))"
			}
			_, err := testExecute(e, ctx, statement(t, query), nil)
			if !called || !errors.Is(err, context.Canceled) {
				t.Fatal("genuine provider publication cancellation", called, err)
			}
			select {
			case err := <-reset:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("cancellation did not release genuine provider reservation")
			}
			if e.CatalogRead().Epoch() == before.epoch {
				t.Fatal("released replacement did not win catalog epoch")
			}
			for _, descriptor := range e.CatalogRead().Descriptors() {
				if relation, ok := descriptor.(*relationDescriptor); ok && relation.name == "late" {
					t.Fatal("canceled typed definition published")
				}
			}
			lockEmpty(t, e)
			deleteReplacementAdmissionEmpty(t, e)
		})
	}
}
