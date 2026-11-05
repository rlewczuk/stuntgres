package remote_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/remote"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

func TestPostgresFDWOwner(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	owner := remote.NewOwner(op.Session)
	if _, err := op.Attach(session.ExternalSQL, owner); err != nil {
		t.Fatal(err)
	}
	read := e.CatalogRead()
	state := catalog.CloneState(read.State())
	server := &catalog.ForeignObject{ObjectAddress: ids.ObjectAddress{ClassID: ids.ForeignServerClassID, ObjectID: 64101}, ObjectRevision: 1, ObjectName: "owner-unit-server"}
	mapping := &catalog.ForeignObject{ObjectAddress: ids.ObjectAddress{ClassID: ids.ForeignMappingClassID, ObjectID: 64102}, ObjectRevision: 1, Server: server.Address()}
	state.SetDescriptor(server.Address(), server)
	state.SetDescriptor(mapping.Address(), mapping)
	read = read.WithState(state)
	bytea, ok := read.LookupType(sqltype.Bytea)
	if !ok {
		t.Fatal("missing builtin bytea")
	}
	ctx, cancel := context.WithTimeout(op.Context(), 10*time.Second)
	defer cancel()
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	free := func() { unblock.Do(func() { close(release) }) }
	defer free()
	failure := errors.New("negative-only late transport failure")
	provider := &catalog.ExternalSQLProviderDefinition{Name: "negative-only", Version: "1", Provider: rejectingDblinkOpen(func(ctx context.Context) error {
		close(entered)
		<-ctx.Done()
		close(canceled)
		<-release
		return failure
	})}
	identity := remote.PostgresIdentity{Key: remote.PostgresKey{Database: read.Owner(), Epoch: read.Epoch(), Provider: provider, Mapping: mapping.Address().ObjectID}, Server: server.Address().ObjectID, ServerRevision: 1, MappingRevision: 1, Keep: true}
	pending, lease, err := owner.BeginPostgres(ctx, op, identity, catalog.ExternalSQLConnectRequest{}, bytea)
	if err != nil || pending == nil || lease != nil {
		t.Fatal("pending reservation", pending, lease, err)
	}
	finished := make(chan error, 1)
	joined := make(chan struct{})
	go func() { defer close(joined); _, err := pending.Open(); pending.Finish(); finished <- err }()
	var cleanupJoined <-chan struct{}
	defer func() {
		cancel()
		free()
		guard, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		select {
		case <-joined:
		case <-guard.Done():
			t.Error("late negative Open did not join")
		}
		if cleanupJoined != nil {
			select {
			case <-cleanupJoined:
			case <-guard.Done():
				t.Error("invalidation cleanup did not join")
			}
		}
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, exists := owner.PostgresInfo(identity.Key); exists {
		t.Fatal("pending Open fabricated published connection identity")
	}
	otherEngine := engine.New()
	if unrelated := owner.InvalidatePostgres(otherEngine.CatalogRead(), true); !unrelated.Empty() {
		t.Fatal("replacement selected another database's pending ownership")
	}
	select {
	case <-canceled:
		t.Fatal("unrelated replacement canceled the admitted pending child")
	default:
	}
	// A genuine revision change invalidates a pending member of this Owner.
	next := catalog.CloneState(read.State())
	changed := *server
	changed.ObjectRevision = 2
	next.SetDescriptor(server.Address(), &changed)
	cleanup := owner.InvalidatePostgres(read.WithState(next), false)
	closed := make(chan error, 1)
	cleanupDone := make(chan struct{})
	cleanupJoined = cleanupDone
	go func() { defer close(cleanupDone); closed <- cleanup.Close() }()
	select {
	case <-canceled:
	case <-ctx.Done():
		t.Fatal("revision change did not cancel pending child", ctx.Err())
	}
	select {
	case err := <-closed:
		t.Fatal("cleanup failed to join pending Open", err)
	default:
	}
	if published, err := pending.Publish(); err == nil || published != nil {
		t.Fatal("invalidated generation published a lease")
	}
	free()
	select {
	case err := <-finished:
		if !errors.Is(err, failure) {
			t.Fatal("negative host error identity changed", err)
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
		t.Fatal(ctx.Err())
	}
	if _, exists := owner.PostgresInfo(identity.Key); exists {
		t.Fatal("failed late return became cached")
	}
	identity.ServerRevision = 2
	nextPending, nextLease, err := owner.BeginPostgres(ctx, op, identity, catalog.ExternalSQLConnectRequest{}, bytea)
	if err != nil || nextPending == nil || nextLease != nil {
		t.Fatal("invalidation retired the reusable session owner", err)
	}
	nextPending.Finish()
	other, otherDone := enginetest.RoleOperation(t, otherEngine, "postgres")
	defer otherDone()
	if pending, lease, err := owner.BeginPostgres(ctx, other, identity, catalog.ExternalSQLConnectRequest{}, bytea); err == nil || pending != nil || lease != nil {
		t.Fatal("foreign physical operation admitted")
	}
}
