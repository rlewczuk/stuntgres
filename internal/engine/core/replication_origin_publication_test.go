package core_test

import (
	"errors"
	"fmt"
	"maps"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/rolecmds"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Borrow the real database executor with a typed command, not public SQL or
// manually fabricated attachment state.
func originPublicationPeer(t *testing.T, e *core.Engine) *core.Engine {
	t.Helper()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	command := &parser.CreateDatabaseStmt{Name: "origin_publication_peer"}
	if err := scope.Gate(command); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	_, err = rolecmds.ExecuteCreateDatabase(e, scope, command, 0)
	if finishErr := scope.Finish(err); err == nil {
		err = finishErr
	}
	if err != nil {
		t.Fatal(err)
	}
	peer, err := e.SelectDatabase(t.Context(), command.Name)
	if err != nil {
		t.Fatal(err)
	}
	return peer
}

func TestReplicationOriginStateReplacementPublication(t *testing.T) {
	t.Run("final_latest_pending", originPublicationLatestPending)
	for _, retained := range []int{9, 8} {
		t.Run(fmt.Sprint(retained), func(t *testing.T) {
			source, target := engine.New(), engine.New()
			for i, name := range []string{"publication_high", "publication_zero"} {
				h, finish := originStateUnitHost(t, source)
				if _, err := originStateUnitCall(t, source, h, functions.ReplicationOriginCreate, name, 0, false); err != nil {
					t.Fatal(err)
				}
				position := uint64(0)
				if i == 0 {
					position = 0x8000000000000004
				}
				if _, err := originStateUnitCall(t, source, h, functions.ReplicationOriginAdvance, name, position, false); err != nil {
					t.Fatal(err)
				}
				finish(nil)
			}
			peer := originPublicationPeer(t, target)
			s := enginetest.StartSession(t, peer, nil)
			ref := enginetest.TransactionBegin(t, s)
			positions := make(map[ids.OID]uint64)
			for i := range retained {
				scope, stage, sub, done := originStage(t, peer, s, fmt.Sprintf("publication_pending_%d", i))
				transferOriginStage(t, peer, scope, stage)
				position := uint64(i)
				if _, err := originStateUnitCall(t, peer, core.HostOf(scope), functions.ReplicationOriginAdvance, fmt.Sprint("pg_", sub.Address().ObjectID), position, false); err != nil {
					t.Fatal(err)
				}
				if err := scope.Finish(nil); err != nil {
					t.Fatal(err)
				}
				done()
				positions[sub.ReplicationOriginID] = position
			}
			root, peerRoot := target.Catalog.State, peer.Catalog.State
			oid, row, version, commit := target.Catalog.NextOID(), target.Storage.RowFrontier(), target.Storage.VersionFrontier(), target.Visibility.CommitSeq()
			journal := s.Transactions.Catalog
			before := target.CopyReplicationOriginProgressLocked()
			_, err := target.ReplaceCatalog(t.Context(), source, source.CatalogRead())
			if retained == 9 {
				var diagnostic *sqlstate.Error
				want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "53400", Message: `could not replace replication origin progress: replacement requires 11 active replication state slots, but "max_active_replication_origins" is 10`, Hint: `Increase "max_active_replication_origins" and try again.`}
				if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
					t.Fatal(err)
				}
				if target.Catalog.State != root || peer.Catalog.State != peerRoot || oid != target.Catalog.NextOID() || row != target.Storage.RowFrontier() || version != target.Storage.VersionFrontier() || commit != target.Visibility.CommitSeq() || !maps.Equal(before, target.CopyReplicationOriginProgressLocked()) {
					t.Fatal("rejection changed roots/storage/frontiers/progress")
				}
			} else {
				if err != nil || target.Catalog.State == root || len(target.CopyReplicationOriginProgressLocked()) != 10 {
					t.Fatal("exact capacity publication", err)
				}
			}
			if s.Transactions.Catalog != journal {
				t.Fatal("replacement changed foreign journal backing")
			}
			for id, position := range positions {
				if value, ok := target.CopyReplicationOriginProgressLocked()[id]; !ok || value != position {
					t.Fatal("pending slot lost", id, value, ok)
				}
			}
			enginetest.TransactionEnd(t, s, ref, true)
			for id, position := range positions {
				if value, ok := target.CopyReplicationOriginProgressLocked()[id]; !ok || value != position {
					t.Fatal("commit changed pending slot", id)
				}
			}
		})
	}
	// Both final branches must reject before publication, even at capacity zero.
	for _, foreign := range []bool{false, true} {
		t.Run(fmt.Sprint("zero_", foreign), func(t *testing.T) {
			target := engine.New()
			source := target
			if foreign {
				source = engine.New()
			}
			h, finish := originStateUnitHost(t, source)
			if _, err := originStateUnitCall(t, source, h, functions.ReplicationOriginCreate, "publication_zero_capacity", 0, false); err != nil {
				t.Fatal(err)
			}
			if _, err := originStateUnitCall(t, source, h, functions.ReplicationOriginAdvance, "publication_zero_capacity", 0, false); err != nil {
				t.Fatal(err)
			}
			finish(nil)
			target.Settings = settings.NewRegistry([]settings.Definition{{Name: "max_active_replication_origins", Type: settings.Integer, BootDefault: "0"}}, false)
			root, oid, commit := target.Catalog.State, target.Catalog.NextOID(), target.Visibility.CommitSeq()
			before := target.CopyReplicationOriginProgressLocked()
			_, err := target.ReplaceCatalog(t.Context(), source, source.CatalogRead())
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "53400" || diagnostic.Message != `could not replace replication origin progress: replacement requires 1 active replication state slots, but "max_active_replication_origins" is 0` || root != target.Catalog.State || oid != target.Catalog.NextOID() || commit != target.Visibility.CommitSeq() || !maps.Equal(before, target.CopyReplicationOriginProgressLocked()) {
				t.Fatal("capacity zero crossed publication", err)
			}
			h, finish = originStateUnitHost(t, source)
			if _, err := originStateUnitCall(t, source, h, functions.ReplicationOriginDrop, "publication_zero_capacity", 0, false); err != nil {
				t.Fatal(err)
			}
			finish(nil)
			if _, err := target.ReplaceCatalog(t.Context(), source, source.CatalogRead()); err != nil || len(target.CopyReplicationOriginProgressLocked()) != 0 {
				t.Fatal("empty replacement at zero capacity rejected", err)
			}
		})
	}
}

func originPublicationLatestPending(t *testing.T) {
	source, target := engine.New(), engine.New()
	peer := originPublicationPeer(t, target)
	s := enginetest.StartSession(t, peer, nil)
	ref := enginetest.TransactionBegin(t, s)
	scope, stage, sub, done := originStage(t, peer, s, "publication_latest")
	transferOriginStage(t, peer, scope, stage)
	name := sub.ReplicationOriginName()
	if _, err := originStateUnitCall(t, peer, core.HostOf(scope), functions.ReplicationOriginAdvance, name, 1, false); err != nil {
		t.Fatal(err)
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	done()
	read := source.CatalogRead()
	source.Mu.Lock()
	indexes, err := core.EngineCaptureIndexSnapshot(source, t.Context(), read, source.Storage.Relations, source.Storage.IndexRoots, source.Visibility.CommitSeq())
	source.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	target.ReplacementMu.Lock()
	defer target.ReplacementMu.Unlock()
	target.Mu.Lock()
	oids := target.Catalog.OIDReservationLocked()
	next, procedures, err := core.EngineRemapPreparedCatalog(target, catalog.CloneState(read.State()), source.Roles(), target.RoleStore.PublishedRead(&target.Catalog.Owner, target.Catalog.Current()), nil)
	if err != nil {
		target.Mu.Unlock()
		t.Fatal(err)
	}
	relations := map[ids.ObjectAddress]*storage.Relation{}
	sequences := map[ids.ObjectAddress]*storage.SequenceGenerator{}
	err = core.EnginePreserveTemporaryLocked(target, t.Context(), next, relations, sequences, nil, indexes, procedures, &oids)
	if err != nil {
		target.Mu.Unlock()
		t.Fatal(err)
	}
	prepared, err := core.EnginePrepareForeignIndexStorage(target, t.Context(), next, relations, indexes)
	target.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	// Distinguish final preparation from an earlier snapshot without a fake
	// metadata callback: mutate after genuine preparation releases the mutex.
	op, err := s.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	update, err := peer.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	if err := update.Gate(&parser.Select{}); err != nil {
		update.Finish(err)
		op.Done()
		t.Fatal(err)
	}
	_, err = originStateUnitCall(t, peer, core.HostOf(update), functions.ReplicationOriginAdvance, name, 0x8000000000000008, false)
	if finishErr := update.Finish(err); err == nil {
		err = finishErr
	}
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
	target.Mu.Lock()
	err = core.EngineInstallForeignCatalogLocked(target, t.Context(), next, relations, sequences, catalog.RevisionFrontier(target.Catalog.NextRevision(), next.Descriptors()), prepared, oids)
	value, active := target.CopyReplicationOriginProgressLocked()[sub.ReplicationOriginID]
	target.Mu.Unlock()
	if err != nil || !active || value != 0x8000000000000008 {
		t.Fatal("final merge used stale pending progress", value, active, err)
	}
	enginetest.TransactionEnd(t, s, ref, true)
	if value, active := target.CopyReplicationOriginProgressLocked()[sub.ReplicationOriginID]; !active || value != 0x8000000000000008 {
		t.Fatal("commit changed final pending progress", value, active)
	}
}
