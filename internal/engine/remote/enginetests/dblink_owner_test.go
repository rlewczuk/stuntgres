package remote_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/remote"
	"stuntgres/internal/engine/session"
)

// Negative-only provider: these units exercise reservation/cancellation, never
// claim a successful transport or fake physical handle. Real publication/close
// and independent PID proof belong to the shared reference scenarios.
type rejectingDblinkOpen func(context.Context) error

func (f rejectingDblinkOpen) Open(ctx context.Context, _ catalog.ExternalSQLConnectRequest) (catalog.ExternalSQLConnection, error) {
	return nil, f(ctx)
}

func TestDblinkConnectionOwner(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	owner := remote.NewOwner(op.Session)
	if _, err := op.Attach(session.ExternalSQL, owner); err != nil {
		t.Fatal(err)
	}
	request := catalog.ExternalSQLConnectRequest{ConninfoPresent: true, Conninfo: "dbname=not_opened"}
	provider := &catalog.ExternalSQLProviderDefinition{Name: "negative-only", Version: "1", Provider: rejectingDblinkOpen(func(context.Context) error { return errors.New("transport unavailable") })}
	name := strings.Repeat("n", 63)
	pending, err := owner.BeginDblink(op.Context(), op, provider, request, name+"first", true)
	if err != nil {
		t.Fatal(err)
	}
	finishPending := sync.OnceFunc(pending.Finish)
	defer finishPending()
	if len(owner.DblinkNames()) != 0 {
		t.Fatal("pending reservation was listed")
	}
	if duplicate, err := owner.BeginDblink(op.Context(), op, provider, request, name+"collision", true); err == nil || duplicate != nil {
		if duplicate != nil {
			duplicate.Finish()
		}
		t.Fatal("clipped pending duplicate accepted")
	}
	finishPending() // Deterministic parse rejection has no Open/physical resource.
	retry, err := owner.BeginDblink(op.Context(), op, provider, request, name, true)
	if err != nil {
		t.Fatal("failed parse reservation was not reusable", err)
	}
	retry.Finish()
	unnamed, err := owner.BeginDblink(op.Context(), op, provider, request, "", false)
	if err != nil {
		t.Fatal(err)
	}
	finishUnnamed := sync.OnceFunc(unnamed.Finish)
	defer finishUnnamed()
	emptyNamed, err := owner.BeginDblink(op.Context(), op, provider, request, "", true)
	if err != nil {
		t.Fatal("empty named conflated with default", err)
	}
	finishEmpty := sync.OnceFunc(emptyNamed.Finish)
	defer finishEmpty()
	if len(owner.DblinkNames()) != 0 {
		t.Fatal("pending default/empty name was listed")
	}
	finishUnnamed()
	finishEmpty()

	ctx, cancel := context.WithTimeout(op.Context(), 10*time.Second)
	defer cancel()
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	late := errors.New("negative provider late rejection")
	provider = &catalog.ExternalSQLProviderDefinition{Name: "negative-only", Version: "1", Provider: rejectingDblinkOpen(func(ctx context.Context) error {
		close(entered)
		<-ctx.Done()
		close(canceled)
		<-release
		return late
	})}
	waiting, err := owner.BeginDblink(ctx, op, provider, request, "generation", true)
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	joined := make(chan struct{})
	var cleanupJoined <-chan struct{}
	go func() { defer close(joined); _, err := waiting.Open(); waiting.Finish(); finished <- err }()
	defer func() {
		cancel()
		select {
		case <-release:
		default:
			close(release)
		}
		guard, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		select {
		case <-joined:
		case <-guard.Done():
			t.Error("negative Open worker did not join")
		}
		if cleanupJoined != nil {
			select {
			case <-cleanupJoined:
			case <-guard.Done():
				t.Error("detached cleanup worker did not join")
			}
		}
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	detached := owner.DetachDblink()
	closed := make(chan error, 1)
	cleanupDone := make(chan struct{})
	cleanupJoined = cleanupDone
	go func() { defer close(cleanupDone); closed <- detached.Close() }()
	select {
	case <-canceled:
	case <-ctx.Done():
		t.Fatal("detached generation did not cancel child", ctx.Err())
	}
	select {
	case err := <-closed:
		t.Fatal("cleanup did not join late Open", err)
	default:
	}
	if _, err := waiting.Publish(); err == nil {
		t.Fatal("detached generation published")
	}
	next, err := owner.BeginDblink(ctx, op, provider, request, "generation", true)
	if err != nil {
		t.Fatal("same owner next generation unavailable", err)
	}
	next.Finish()
	close(release)
	select {
	case err := <-finished:
		if !errors.Is(err, late) {
			t.Fatal("negative provider primary changed", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("detached cleanup did not finish", ctx.Err())
	}
	if len(owner.DblinkNames()) != 0 {
		t.Fatal("late failure left a published name")
	}
}
