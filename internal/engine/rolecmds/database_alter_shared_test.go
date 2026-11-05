package rolecmds_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
)

// Both publishers must install the same shared identity, not independently
// recomputed ACLs, while leaving each destination's local storage/epoch intact.
func databaseAlterSharedPublication(t *testing.T) {
	for _, route := range []string{"direct", "commit", "rollback", "commit-prepared", "rollback-prepared"} {
		t.Run(route, func(t *testing.T) {
			e, seed := engine.New(), engine.New()
			peer := databaseCreateUnitInstall(t, e, seed, "peer")
			address := ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: catalog.DefaultTablespaceOID}
			old := e.Catalog.State.Get(address).(*catalog.Tablespace)
			if peer.Catalog.State.Get(address) != old {
				t.Fatal("constructor lost shared builtin identity")
			}
			local, epoch, store, targets := peer.Catalog.DatabaseAddress(), peer.Catalog.State.Epoch(), &peer.Storage, e.Cluster.Targets
			peer.Mu.Lock()
			_, reserveErr := peer.Catalog.RevisionLocked()
			reservedFrontier := peer.Catalog.NextRevision()
			peer.Mu.Unlock()
			if reserveErr != nil {
				t.Fatal(reserveErr)
			}
			s := enginetest.StartSession(t, e, nil)
			if route != "direct" {
				enginetest.SessionSQL(t, s, "BEGIN")
			}
			enginetest.SessionSQL(t, s, "GRANT CREATE ON TABLESPACE pg_default TO PUBLIC")
			if route != "direct" {
				if e.Catalog.State.Get(address) != old || peer.Catalog.State.Get(address) != old {
					t.Fatal("private builtin ACL escaped")
				}
				op, err := s.Acquire(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				auth, err := core.OperationAuthorization(op)
				if err != nil {
					op.Done()
					t.Fatal(err)
				}
				private := auth.Catalog.Get(address).(*catalog.Tablespace)
				if private == old || !private.ACL.Explicit {
					op.Done()
					t.Fatal("issuer lost private builtin ACL")
				}
				op.Done()
				switch route {
				case "commit":
					enginetest.SessionSQL(t, s, "COMMIT")
				case "rollback":
					enginetest.SessionSQL(t, s, "ROLLBACK")
				default:
					enginetest.SessionSQL(t, s, "PREPARE TRANSACTION 'shared_builtin'")
					if e.Catalog.State.Get(address) != old || peer.Catalog.State.Get(address) != old {
						t.Fatal("PREPARE published builtin ACL")
					}
					completion := "COMMIT PREPARED 'shared_builtin'"
					if route == "rollback-prepared" {
						completion = "ROLLBACK PREPARED 'shared_builtin'"
					}
					enginetest.SessionSQL(t, s, completion)
				}
			}
			current := e.Catalog.State.Get(address).(*catalog.Tablespace)
			rolledBack := route == "rollback" || route == "rollback-prepared"
			if rolledBack && current != old || !rolledBack && (current == old || !current.ACL.Explicit) {
				t.Fatal("wrong builtin ACL publication", route)
			}
			if peer.Catalog.State.Get(address) != current || old.ACL.Explicit {
				t.Fatal("divergent shared roots or mutated predecessor")
			}
			if peer.Catalog.DatabaseAddress() != local || peer.Catalog.State.Epoch() != epoch || &peer.Storage != store {
				t.Fatal("shared publication replaced local identity")
			}
			if peer.Catalog.NextRevision() < reservedFrontier || peer.Catalog.NextRevision() <= current.Revision() {
				t.Fatal("shared projection reused or decreased a reserved revision")
			}
			if !rolledBack {
				writer := enginetest.StartSession(t, peer, nil)
				enginetest.SessionSQL(t, writer, "REVOKE CREATE ON TABLESPACE pg_default FROM PUBLIC")
				successor := peer.Catalog.State.Get(address).(*catalog.Tablespace)
				if successor.Revision() <= current.Revision() || e.Catalog.State.Get(address) != successor {
					t.Fatalf("sequential peer writer reused shared revision or diverged: previous=%p rev=%d successor=%p rev=%d default=%p peer_frontier=%d", current, current.Revision(), successor, successor.Revision(), e.Catalog.State.Get(address), peer.Catalog.NextRevision())
				}
			}
			e.Mu.Lock()
			targets["identity_probe"] = peer
			same := e.Cluster.Targets["identity_probe"] == peer
			delete(targets, "identity_probe")
			delete(e.Cluster.Targets, "identity_probe")
			e.Mu.Unlock()
			if !same {
				t.Fatal("builtin publication copied database registry")
			}
		})
	}
	t.Run("cross-target-predecessor", func(t *testing.T) {
		e, seed := engine.New(), engine.New()
		peer := databaseCreateUnitInstall(t, e, seed, "peer")
		address := ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: catalog.DefaultTablespaceOID}
		op, done := enginetest.RoleOperation(t, peer, "postgres")
		defer done()
		auth, err := core.OperationAuthorization(op)
		if err != nil {
			t.Fatal(err)
		}
		old := auth.Catalog.Get(address).(*catalog.Tablespace)
		stage := core.OwnershipStage(peer, auth, op)
		defer peer.DiscardCatalogStage(stage)
		stale := *old
		stale.ObjectRevision++
		if err := stage.Replace(&stale); err != nil {
			t.Fatal(err)
		}
		prepared, err := peer.ValidateCatalogStage(context.Background(), stage)
		if err != nil {
			t.Fatal(err)
		}
		writer := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, writer, "GRANT CREATE ON TABLESPACE pg_default TO PUBLIC")
		committed := e.Catalog.State.Get(address)
		unchanged := databaseCreateUnitUnchanged(t, e)
		if _, err := enginetest.PublishCatalog(peer, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()}); err == nil {
			t.Fatal("stale peer ACL overwrote shared authority")
		}
		unchanged()
		if peer.Catalog.State.Get(address) != committed || e.Catalog.State.Get(address) != committed {
			t.Fatal("failed stale publication changed shared roots")
		}
	})
}
