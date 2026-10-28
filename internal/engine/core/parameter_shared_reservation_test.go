package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

func parameterSharedReservationPublication(t *testing.T) {
	for _, mode := range []string{"unchanged", "peer-allocation", "peer-registration", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			a, b := parameterSharedUnitFixture(t)
			source := a.RestoreStage()
			parameterSharedUnitGrant(t, source, "shared_unit.reserved", false)
			sourceRead := source.CatalogRead()
			original := parameterSharedUnitDescriptor(source, "shared_unit.reserved")
			source.Mu.Lock()
			indexes, err := core.EngineCaptureIndexSnapshot(source, context.Background(), sourceRead, source.Storage.Relations, source.Storage.IndexRoots, source.Visibility.CommitSeq())
			source.Mu.Unlock()
			if err != nil { t.Fatal(err) }
			peerSession := enginetest.StartSession(t, b, nil)
			a.ReplacementMu.Lock()
			defer a.ReplacementMu.Unlock()
			a.Mu.Lock()
			defer a.Mu.Unlock()
			oids := a.Catalog.OIDReservationLocked()
			next, procedures, err := core.EngineRemapPreparedCatalog(a, catalog.CloneState(sourceRead.State()), source.Roles(), a.RoleStore.PublishedRead(&a.Catalog.Owner, a.Catalog.Current()), source.ImportTargets)
			if err != nil { t.Fatal(err) }
			relations := map[ids.ObjectAddress]*storage.Relation{}
			sequences := map[ids.ObjectAddress]*storage.SequenceGenerator{}
			if err := core.EnginePreserveTemporaryLocked(a, context.Background(), next, relations, sequences, nil, indexes, procedures, &oids); err != nil { t.Fatal(err) }
			reserved := next.Get(next.NameAddress(catalog.DescriptorNameKey(original))).(*catalog.Parameter)
			if reserved.Address() == original.Address() || oids.Next <= reserved.Address().ObjectID || len(oids.PeerFrontiers) != len(a.Cluster.Targets)-1 { t.Fatal("missing genuine private shared reservation") }
			prepared, err := core.EnginePrepareForeignIndexStorage(a, context.Background(), next, relations, indexes)
			if err != nil { t.Fatal(err) }
			// The real remint/preparation is complete. Release the actual mutex
			// for an ordinary peer action, then use the genuine final install seam.
			a.Mu.Unlock()
			func() {
				defer a.Mu.Lock()
				switch mode {
				case "peer-allocation":
					b.Mu.Lock()
					oid, allocateErr := b.Catalog.AllocateOIDAtLeastLocked(reserved.Address().ObjectID)
					b.Mu.Unlock()
					if allocateErr != nil || oid != reserved.Address().ObjectID { t.Fatal("peer did not consume the actual reserved candidate", oid, allocateErr) }
				case "peer-registration":
					enginetest.SessionSQL(t, peerSession, "CREATE DATABASE shared_reservation_later")
				}
			}()
			rootA, rootB, frontierA, frontierB := a.Catalog.State, b.Catalog.State, a.Catalog.NextOID(), b.Catalog.NextOID()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var installContext context.Context = ctx
			if mode == "cancelled" { installContext = &controlReplacementContext{Context: ctx, cancel: cancel} }
			err = core.EngineInstallForeignCatalogLocked(a, installContext, next, relations, sequences, catalog.RevisionFrontier(a.Catalog.NextRevision(), next.Descriptors()), prepared, oids)
			if mode == "unchanged" {
				if err != nil || parameterSharedUnitCoherent(t, "shared_unit.reserved", a, b) != reserved || a.Catalog.NextOID() != oids.Next || b.Catalog.NextOID() < oids.Next { t.Fatal("unchanged reservation did not install atomically", err) }
			} else {
				want := ids.ErrCatalogReplaced
				if mode == "cancelled" { want = context.Canceled }
				if !errors.Is(err, want) { t.Fatal("stale/cancelled private reservation installed", mode, err) }
				if a.Catalog.State != rootA || b.Catalog.State != rootB || a.Catalog.NextOID() != frontierA || b.Catalog.NextOID() != frontierB || parameterSharedUnitDescriptor(a, "shared_unit.reserved") != nil { t.Fatal("failed prepared replacement exposed a prefix") }
			}
			if sourceRead.Get(original.Address()) != original { t.Fatal("remint mutated source backing") }
		})
	}
}
