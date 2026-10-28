package core_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"sync"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
)

type parameterSharedGatedReader struct {
	entered chan struct{}
	release chan struct{}
	once sync.Once
	source io.Reader
}

func (r *parameterSharedGatedReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.entered); <-r.release })
	return r.source.Read(p)
}

func TestParameterACLSharedReplacementFences(t *testing.T) {
	t.Run("private-reservation", parameterSharedReservationPublication)
	t.Run("reader-unrelated-publication", parameterSharedUnrelatedReplacement)
	for _, operation := range []string{"reset", "restore"} {
		t.Run("absent/"+operation, func(t *testing.T) {
			a, b := parameterSharedUnitFixture(t)
			scope, _, done := parameterSharedUnitPending(t, b, "shared_unit.pending")
			defer done()
			journal := scope.PendingCatalog()
			oldRoot, oldIncarnation := b.Catalog.State, b.Catalog.State.ParameterIncarnation()
			var err error
			if operation == "reset" { err = engine.Reset(context.Background(), a) } else { err = engine.Restore(context.Background(), a, bytes.NewReader(nil)) }
			if err != nil { scope.Finish(err); t.Fatal(err) }
			if b.Catalog.State == oldRoot || b.Catalog.State.ParameterIncarnation() != oldIncarnation+1 || a.Catalog.State.ParameterIncarnation() != b.Catalog.State.ParameterIncarnation() { scope.Finish(context.Canceled); t.Fatal("empty replacement did not publish a shared incarnation") }
			if journal.ParameterIncarnation != oldIncarnation || oldRoot.ParameterIncarnation() != oldIncarnation { scope.Finish(context.Canceled); t.Fatal("replacement rewrote captured original") }
			if err := scope.Finish(nil); !errors.Is(err, ids.ErrCatalogReplaced) { t.Fatal("stale first grant resurrected authority", err) }
			if parameterSharedUnitCoherent(t, "shared_unit.pending", a, b) != nil { t.Fatal("rejected Finish exposed shared prefix") }
		})
	}
	t.Run("transaction/savepoint", func(t *testing.T) {
		a, b := parameterSharedUnitFixture(t)
		s := enginetest.StartSession(t, b, nil)
		enginetest.SessionSQL(t, s, "BEGIN")
		enginetest.SessionSQL(t, s, "GRANT SET ON PARAMETER shared_unit.pending TO grant_reader")
		original := s.Transactions.Catalog
		enginetest.SessionSQL(t, s, "SAVEPOINT original")
		enginetest.SessionSQL(t, s, "GRANT SET ON PARAMETER shared_unit.suffix TO grant_reader")
		if err := engine.Restore(context.Background(), a, bytes.NewReader(nil)); err != nil { t.Fatal(err) }
		// Private refresh must preserve the original scalar, not bless the new
		// committed incarnation merely because the parameter was absent.
		read := original.PrivateRead(&b.Catalog.Owner, b.Catalog.State)
		if read.State().ParameterIncarnation() != original.ParameterIncarnation || original.ParameterIncarnation == b.Catalog.State.ParameterIncarnation() { t.Fatal("private refresh recaptured replacement authority") }
		b.Mu.Lock()
		err := b.CatalogPreflightLocked(original.Clone())
		b.Mu.Unlock()
		if !errors.Is(err, ids.ErrCatalogReplaced) { t.Fatal("cloned journal lost replacement fence", err) }
		enginetest.SessionSQL(t, s, "ROLLBACK")
	})
	t.Run("existing-stage", func(t *testing.T) {
		a, b := parameterSharedUnitFixture(t)
		parameterSharedUnitGrant(t, a, "shared_unit.existing", false)
		base := b.CatalogRead()
		old := parameterSharedUnitDescriptor(b, "shared_unit.existing")
		stage := b.BeginCatalogStage(base)
		defer b.DiscardCatalogStage(stage)
		next := *old
		next.ObjectRevision++
		if err := stage.Replace(&next); err != nil { t.Fatal(err) }
		prepared, err := b.ValidateCatalogStage(context.Background(), stage)
		if err != nil { t.Fatal(err) }
		op, done := enginetest.RoleOperation(t, b, "postgres")
		defer done()
		scope, err := b.StartStatement(op)
		if err != nil { t.Fatal(err) }
		defer func() { if !scope.Completed { scope.Finish(context.Canceled) } }()
		revoke := parameterSpaceRequest("parameter", "shared_unit.existing")
		revoke.Revoke = true
		if _, err := scope.Execute(revoke, 0, nil); err != nil { scope.Finish(err); t.Fatal(err) }
		journal := scope.PendingCatalog()
		if journal == nil || journal.Originals[old.Address()] != old { scope.Finish(context.Canceled); t.Fatal("pending revoke lost exact predecessor") }
		if err := engine.Reset(context.Background(), a); err != nil { t.Fatal(err) }
		if err := scope.Finish(nil); !errors.Is(err, ids.ErrCatalogReplaced) { t.Fatal("pending existing revoke crossed replacement", err) }
		if _, err := enginetest.PublishCatalog(b, prepared, core.PublicationToken{Epoch: base.Epoch()}); !errors.Is(err, ids.ErrCatalogReplaced) { t.Fatal("accepted old stage crossed replacement", err) }
		if base.Get(old.Address()) != old || old.Name() != "shared_unit.existing" { t.Fatal("replacement mutated accepted predecessor") }
	})
	t.Run("reader-peer-publication", func(t *testing.T) {
		a, b := parameterSharedUnitFixture(t)
		reader := &parameterSharedGatedReader{entered: make(chan struct{}), release: make(chan struct{}), source: bytes.NewReader(nil)}
		finished := make(chan error, 1)
		go func() { finished <- engine.Restore(context.Background(), a, reader) }()
		released, joined := false, false
		defer func() {
			if !released { close(reader.release) }
			if !joined { <-finished }
		}()
		<-reader.entered
		parameterSharedUnitGrant(t, b, "shared_unit.during_input", false)
		winner := parameterSharedUnitCoherent(t, "shared_unit.during_input", a, b)
		frontierA, frontierB, incarnation := a.Catalog.NextOID(), b.Catalog.NextOID(), a.Catalog.State.ParameterIncarnation()
		close(reader.release)
		released = true
		err := <-finished
		joined = true
		if !errors.Is(err, ids.ErrCatalogReplaced) { t.Fatal("stale reader overwrote a peer publication", err) }
		if parameterSharedUnitCoherent(t, "shared_unit.during_input", a, b) != winner || a.Catalog.NextOID() != frontierA || b.Catalog.NextOID() != frontierB || a.Catalog.State.ParameterIncarnation() != incarnation { t.Fatal("failed restore installed shared state/frontier") }
	})
	t.Run("roundtrip-atomicity", func(t *testing.T) {
		a, b := parameterSharedUnitFixture(t)
		parameterSharedUnitGrant(t, a, "shared_unit.roundtrip", false)
		old := parameterSharedUnitDescriptor(a, "shared_unit.roundtrip")
		entries := append(old.ACL.Entries[:0:0], old.ACL.Entries...)
		oldRoot := a.Catalog.State
		var dump bytes.Buffer
		if err := engine.Dump(context.Background(), a, &dump); err != nil { t.Fatal(err) }
		for _, kind := range []string{"invalid", "cancelled"} {
			rootA, rootB, frontierA, frontierB := a.Catalog.State, b.Catalog.State, a.Catalog.NextOID(), b.Catalog.NextOID()
			ctx, cancel := context.WithCancel(context.Background())
			input := dump.Bytes()
			if kind == "invalid" { input = []byte("SELECT no_such_restore_function();") } else { cancel() }
			err := engine.Restore(ctx, a, bytes.NewReader(input))
			cancel()
			if err == nil || a.Catalog.State != rootA || b.Catalog.State != rootB || a.Catalog.NextOID() != frontierA || b.Catalog.NextOID() != frontierB { t.Fatal("failed replacement published", kind, err) }
		}
		if err := engine.Reset(context.Background(), a); err != nil { t.Fatal(err) }
		if err := engine.Restore(context.Background(), a, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
		current := parameterSharedUnitCoherent(t, "shared_unit.roundtrip", a, b)
		if current == nil || current == old || current.Address() == old.Address() || !reflect.DeepEqual(current.ACL, old.ACL) { t.Fatal("replacement did not remint exact canonical authority") }
		if oldRoot.Get(old.Address()) != old || !reflect.DeepEqual(old.ACL.Entries, entries) { t.Fatal("replacement mutated retained ACL backing") }
		var redump bytes.Buffer
		if err := engine.Dump(context.Background(), a, &redump); err != nil { t.Fatal(err) }
		if !bytes.Equal(dump.Bytes(), redump.Bytes()) { t.Fatal("canonical replay changed bytes") }
	})
}

func parameterSharedUnrelatedReplacement(t *testing.T) {
	a, b := parameterSharedUnitFixture(t)
	parameterSharedUnitGrant(t, a, "shared_unit.input_unchanged", false)
	oldRoot := a.Catalog.State
	old := parameterSharedUnitCoherent(t, "shared_unit.input_unchanged", a, b)
	key := catalog.DescriptorNameKey(old)
	// Canonical binding changes are authority changes even if a malformed
	// detached candidate retains the exact descriptor and incarnation.
	unbound := catalog.CloneState(oldRoot)
	unbound.UnbindName(key)
	if unbound.ParametersUnchanged(oldRoot) || oldRoot.ParametersUnchanged(unbound) {
		t.Fatal("parameter predecessor ignored a canonical name binding")
	}
	sa, sb := enginetest.StartSession(t, a, nil), enginetest.StartSession(t, b, nil)
	var dump bytes.Buffer
	if err := engine.Dump(context.Background(), a, &dump); err != nil { t.Fatal(err) }
	reader := &parameterSharedGatedReader{entered: make(chan struct{}), release: make(chan struct{}), source: bytes.NewReader(dump.Bytes())}
	finished := make(chan error, 1)
	go func() { finished <- engine.Restore(context.Background(), a, reader) }()
	released, joined := false, false
	defer func() {
		if !released { close(reader.release) }
		if !joined { <-finished }
	}()
	<-reader.entered
	enginetest.SessionSQL(t, sa, "CREATE TABLE parameter_local_suffix(id INT)")
	enginetest.SessionSQL(t, sb, "CREATE TABLE parameter_peer_retained(id INT)")
	local := enginetest.CatalogRef(t, a, a.CatalogRead(), "parameter_local_suffix")
	peer := enginetest.CatalogRef(t, b, b.CatalogRead(), "parameter_peer_retained")
	localRoot, peerRoot := a.Catalog.State, b.Catalog.State
	peerDescriptor, peerStorage := peerRoot.Get(peer.Address), b.Storage.Relations[peer.Address]
	if localRoot == oldRoot || !localRoot.ParametersUnchanged(oldRoot) || !peerRoot.ParametersUnchanged(oldRoot) {
		t.Fatal("unrelated descriptor publications changed parameter predecessor authority")
	}
	close(reader.release)
	released = true
	err := <-finished
	joined = true
	if err != nil { t.Fatal("unrelated reader-time publication incorrectly fenced replacement", err) }
	current := parameterSharedUnitCoherent(t, "shared_unit.input_unchanged", a, b)
	if current == nil || current == old || !reflect.DeepEqual(current.ACL, old.ACL) {
		t.Fatal("replacement lost unchanged canonical parameter ACL")
	}
	if a.Catalog.State.Get(local.Address) != nil || b.Catalog.State.Get(peer.Address) != peerDescriptor || b.Storage.Relations[peer.Address] != peerStorage {
		t.Fatal("replacement did not preserve target-local versus peer-local ownership")
	}
	if oldRoot.Get(old.Address()) != old || oldRoot.NameAddress(key) != old.Address() || localRoot.Get(local.Address) == nil || peerRoot.Get(peer.Address) != peerDescriptor {
		t.Fatal("replacement mutated actual retained predecessor roots")
	}
}
