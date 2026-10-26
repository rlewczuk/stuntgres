package fixture

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"
)

func providerPollOwner(ctx context.Context) error {
	return ctx.Err()
}

func providerPollWorker(ctx context.Context, done chan<- error) {
	done <- providerPollOwner(ctx)
}

func TestReferenceFixtureProviderPollContext(t *testing.T) {
	type contextKey struct{}
	parent := context.WithValue(context.Background(), contextKey{}, "owned value")
	base, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	ctx := &nativeProviderPollContext{Context: base, provider: []byte("fixture.providerPollOwner"), worker: []byte("fixture.providerPollWorker")}
	deadline, ok := ctx.Deadline()
	wantDeadline, wantOK := base.Deadline()
	if deadline != wantDeadline || ok != wantOK || ctx.Done() != base.Done() || ctx.Value(contextKey{}) != "owned value" {
		t.Fatal("observation changed context delegation")
	}
	// An unrelated caller must neither admit nor wait, even with a live owner.
	if err := ctx.Err(); err != nil {
		t.Fatal("unrelated caller", err)
	}
	done := make(chan error, 1)
	joined := false
	defer func() {
		cancel()
		if !joined {
			<-done
		}
	}()
	go providerPollWorker(ctx, done)
	stacks := make([]byte, 64<<10)
	for {
		select {
		case err := <-done:
			joined = true
			t.Fatal("provider escaped its admission poll", err)
		default:
		}
		observed, next := nativeOperationStack(stacks, []byte("fixture.providerPollOwner"), []byte("fixture.providerPollWorker"), []byte("(*nativeProviderPollContext).Err"))
		stacks = next
		if observed {
			break
		}
		if base.Err() != nil {
			t.Fatal("provider admission", base.Err())
		}
		runtime.Gosched()
	}
	if err := ctx.Err(); err != nil {
		t.Fatal("parked provider blocked unrelated caller", err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal("underlying cancellation not returned", err)
	}
	joined = true
	if err := ctx.Err(); !errors.Is(err, context.Canceled) {
		t.Fatal("terminal context changed", err)
	}
}

func TestReferenceFixtureProviderPollAuthority(t *testing.T) {
	for _, tc := range []struct {
		name, provider, worker string
	}{
		{"wrong_provider", "fixture.absentProvider", "fixture.providerPollWorker"},
		{"wrong_worker", "fixture.providerPollOwner", "fixture.absentWorker"},
		{"creation_only_worker", "fixture.providerPollOwner", "fixture.TestReferenceFixtureProviderPollAuthority"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ctx := &nativeProviderPollContext{Context: base, provider: []byte(tc.provider), worker: []byte(tc.worker)}
			done := make(chan error, 1)
			// The test's name exists only in creation metadata, never as a live
			// frame in this worker. Neither half of the authority pair suffices.
			go providerPollWorker(ctx, done)
			if err := <-done; err != nil {
				t.Fatal("non-authoritative caller was held", err)
			}
		})
	}
}
