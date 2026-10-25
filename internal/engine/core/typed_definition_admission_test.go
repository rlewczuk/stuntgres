package core_test

import (
	"context"
	"errors"
	"os"
	osexec "os/exec"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
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
		cmd := osexec.CommandContext(ctx, binary, "-test.run=^TestTypedPublicationDefinitionProviderReservation$", "-test.count=1", "-test.timeout=10s")
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
			enginetest.Run(t, e, "CREATE SEQUENCE q")
			before := e.CatalogRead().State()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reset := make(chan error, 1)
			original := e.Catalog.Providers[ids.RelationClassID]
			called := false
			e.Catalog.Providers[ids.RelationClassID] = alterDropProvider{original, func() {
				if called {
					return
				}
				called = true
				e.Catalog.Providers[ids.RelationClassID] = original
				go func() { reset <- engine.Reset(context.Background(), e) }()
				deadline := time.Now().Add(3 * time.Second)
				for {
					core.ReplacementAdmissionMuRef(&e.ReplacementMu).Lock()
					queued := core.ReplacementAdmissionReaders(&e.ReplacementMu) == 1 && core.ReplacementAdmissionWriters(&e.ReplacementMu) == 1 && !core.ReplacementAdmissionWriting(&e.ReplacementMu)
					core.ReplacementAdmissionMuRef(&e.ReplacementMu).Unlock()
					if queued {
						break
					}
					if time.Now().After(deadline) {
						cancel()
						t.Fatal("replacement did not queue behind genuine typed DEFAULT provider reservation")
					}
					runtime.Gosched()
				}
				if e.CatalogRead().State() != before {
					cancel()
					t.Fatal("queued replacement changed unpublished typed definition")
				}
				cancel()
			}}
			query := "CREATE TABLE late OF pair(id WITH OPTIONS DEFAULT nextval('q'))"
			if temporary {
				query = "CREATE TEMP TABLE late OF pair(id WITH OPTIONS DEFAULT nextval('q'))"
			}
			_, err := enginetest.Execute(e, ctx, enginetest.Statement(t, query), nil)
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
			if e.CatalogRead().Epoch() == before.Epoch() {
				t.Fatal("released replacement did not win catalog epoch")
			}
			for _, descriptor := range e.CatalogRead().Descriptors() {
				if relation, ok := descriptor.(*catalog.Relation); ok && relation.Name() == "late" {
					t.Fatal("canceled typed definition published")
				}
			}
			enginetest.LockEmpty(t, e)
			deleteReplacementAdmissionEmpty(t, e)
		})
	}
}
