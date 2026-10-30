package core_test

import (
	"context"
	"errors"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"testing"
)

func TestReplicationOriginReplacementCollisions(t *testing.T) {
	t.Run("final_pending_fence", originReplacementPendingFence)
	donor, target := engine.New(), engine.New()
	enginetest.Run(t, donor, "CREATE SUBSCRIPTION imported_origin CONNECTION '' PUBLICATION p WITH (connect=false,slot_name=NONE)")
	enginetest.Run(t, target, "CREATE SUBSCRIPTION selected_origin CONNECTION '' PUBLICATION p WITH (connect=false,slot_name=NONE)")
	enginetest.Run(t, target, "CREATE DATABASE origin_unit_peer")
	peer, err := target.SelectDatabase(context.Background(), "origin_unit_peer")
	if err != nil {
		t.Fatal(err)
	}
	enginetest.Run(t, peer, "CREATE SUBSCRIPTION peer_origin CONNECTION '' PUBLICATION p WITH (connect=false,slot_name=NONE)")
	var retained *catalog.Subscription
	for _, d := range target.CatalogRead().Subscriptions() {
		if d.Name() == "peer_origin" {
			retained = d
		}
	}
	if retained == nil {
		t.Fatal("missing real peer descriptor")
	}
	session := enginetest.StartSession(t, peer, nil)
	ref := enginetest.TransactionBegin(t, session)
	pending, pendingStage, claim, done := originStage(t, peer, session, "peer_origin_pending")
	transferOriginStage(t, peer, pending, pendingStage)
	if err := pending.Finish(nil); err != nil {
		t.Fatal(err)
	}
	done()
	before := target.CatalogRead().State()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := target.ReplaceCatalog(ctx, donor, donor.CatalogRead()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if target.CatalogRead().State() != before || peer.CatalogRead().Get(retained.Address()) != retained || session.Transactions.Catalog.Descriptors[claim.Address()] != claim {
		t.Fatal("canceled replacement changed roots or pending journal")
	}
	if _, err := target.ReplaceCatalog(context.Background(), donor, donor.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	seen := make(map[ids.OID]bool)
	var imported *catalog.Subscription
	for _, d := range target.CatalogRead().Subscriptions() {
		if seen[d.ReplicationOriginID] || d.ReplicationOriginID == claim.ReplicationOriginID {
			t.Fatal("replacement origin collision", d)
		}
		seen[d.ReplicationOriginID] = true
		if d.Name() == "imported_origin" {
			imported = d
		}
	}
	if imported == nil || imported.ReplicationOriginID == 0 || target.CatalogRead().Get(retained.Address()) != retained || peer.CatalogRead().Get(retained.Address()) != retained {
		t.Fatal("replacement lost remint or peer identity")
	}
	if session.Transactions.Catalog.Descriptors[claim.Address()] != claim {
		t.Fatal("selected replacement retired peer journal")
	}
	enginetest.TransactionEnd(t, session, ref, true)
	if got := target.CatalogRead().Get(claim.Address()); got != claim {
		t.Fatal("peer pending registration failed to publish after replacement", got)
	}
	selectedSession := enginetest.StartSession(t, target, nil)
	enginetest.TransactionBegin(t, selectedSession)
	staleScope, staleStage, stale, staleDone := originStage(t, target, selectedSession, "selected_stale_pending")
	transferOriginStage(t, target, staleScope, staleStage)
	if err := staleScope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	staleDone()
	if _, err := target.ReplaceCatalog(context.Background(), donor, donor.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	checkSession := enginetest.StartSession(t, target, nil)
	checkScope, checkStage, released, checkDone := originStage(t, target, checkSession, "after_selected_replacement")
	if released.ReplicationOriginID != stale.ReplicationOriginID {
		t.Fatal("successful replacement retained selected stale exclusion")
	}
	target.DiscardCatalogStage(checkStage)
	checkScope.Finish(context.Canceled)
	checkDone()
}

func originReplacementPendingFence(t *testing.T) {
	target, donor := engine.New(), engine.New()
	enginetest.Run(t, target, "CREATE DATABASE origin_final_peer")
	peer, err := target.SelectDatabase(context.Background(), "origin_final_peer")
	if err != nil {
		t.Fatal(err)
	}
	enginetest.Run(t, donor, "CREATE SUBSCRIPTION final_import CONNECTION '' PUBLICATION p WITH (connect=false,slot_name=NONE)")
	sourceRead := donor.CatalogRead()
	donor.Mu.Lock()
	indexes, err := core.EngineCaptureIndexSnapshot(donor, context.Background(), sourceRead, donor.Storage.Relations, donor.Storage.IndexRoots, donor.Visibility.CommitSeq())
	donor.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	target.ReplacementMu.Lock()
	defer target.ReplacementMu.Unlock()
	target.Mu.Lock()
	oids := target.Catalog.OIDReservationLocked()
	next, procedures, err := core.EngineRemapPreparedCatalog(target, catalog.CloneState(sourceRead.State()), donor.Roles(), target.RoleStore.PublishedRead(&target.Catalog.Owner, target.Catalog.Current()), nil)
	if err != nil {
		target.Mu.Unlock()
		t.Fatal(err)
	}
	relations := map[ids.ObjectAddress]*storage.Relation{}
	sequences := map[ids.ObjectAddress]*storage.SequenceGenerator{}
	err = core.EnginePreserveTemporaryLocked(target, context.Background(), next, relations, sequences, nil, indexes, procedures, &oids)
	if err != nil {
		target.Mu.Unlock()
		t.Fatal(err)
	}
	prepared, err := core.EnginePrepareForeignIndexStorage(target, context.Background(), next, relations, indexes)
	target.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	// Pause a genuine peer stage immediately after its narrow reservation,
	// without advancing ordinary OID frontiers. This distinguishes the origin
	// fence from the pre-existing shared-OID predecessor fence.
	s := enginetest.StartSession(t, peer, nil)
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := peer.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(context.Canceled)
	if err := scope.Gate(enginetest.Statement(t, "CREATE SUBSCRIPTION final_peer_pending CONNECTION '' PUBLICATION p WITH (connect=false,slot_name=NONE)")); err != nil {
		t.Fatal(err)
	}
	peer.Mu.Lock()
	auth, err := peer.AuthorizationLocked(op)
	if err != nil {
		peer.Mu.Unlock()
		t.Fatal(err)
	}
	stage := peer.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, s)
	id, err := (core.DDLHost{E: peer, Op: op, CatalogStage: stage}).AllocateReplicationOrigin()
	peer.Mu.Unlock()
	defer peer.DiscardCatalogStage(stage)
	if err != nil {
		t.Fatal(err)
	}
	var imported *catalog.Subscription
	for _, d := range target.Catalog.Read(next).Subscriptions() {
		if d.Name() == "final_import" {
			imported = d
		}
	}
	if imported == nil || imported.ReplicationOriginID != id {
		t.Fatal("fixture did not collide with actual privately reminted ID")
	}
	target.Mu.Lock()
	before, peerBefore, frontier := target.Catalog.State, peer.Catalog.State, target.Catalog.NextOID()
	if frontier != oids.Base {
		target.Mu.Unlock()
		t.Fatal("fixture advanced ordinary OID fence")
	}
	err = core.EngineInstallForeignCatalogLocked(target, context.Background(), next, relations, sequences, catalog.RevisionFrontier(target.Catalog.NextRevision(), next.Descriptors()), prepared, oids)
	target.Mu.Unlock()
	if !errors.Is(err, ids.ErrCatalogReplaced) || target.Catalog.State != before || peer.Catalog.State != peerBefore || target.Catalog.NextOID() != frontier {
		t.Fatal("origin-only pending collision crossed final installation fence", err)
	}
}
