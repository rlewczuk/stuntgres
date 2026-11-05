package rolecmds_test

import (
	"context"
	"errors"
	"maps"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

func databaseAlterExplicitPublication(t *testing.T) {
	t.Run("stage-name-admission", databaseAlterStageNamePublication)
	for _, route := range []string{"owner", "acl", "both", "cancel", "stale", "foreign-principal", "exhaustion", "copied-operation"} {
		t.Run(route, func(t *testing.T) {
			e, seed := engine.New(), engine.New()
			peer := databaseCreateUnitInstall(t, e, seed, "peer")
			address := ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: catalog.GlobalTablespaceOID}
			original := e.Catalog.State.Get(address).(*catalog.Tablespace)
			peerEpoch, peerStore := peer.Catalog.State.Epoch(), &peer.Storage
			stage := e.RestoreStage()
			repairs := stage.RestoreTablespaceRepairs()
			maintenance, err := stage.StartRestoreSession(context.Background(), stage.Roles().LookupRef("postgres"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				maintenance.RequestEnd(session.CleanClose)
				if err := maintenance.WaitEnd(context.Background()); err != nil {
					t.Error(err)
				}
			}()
			op, err := maintenance.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			replay := core.OperationReplay(op)
			replay.StagingEffects, replay.TablespaceRepairs = true, &repairs
			value := *stage.Catalog.State.Get(address).(*catalog.Tablespace)
			value.Owner = stage.Roles().LookupRef("pg_read_all_data")
			value.ACL = authorization.ACLValue{Explicit: true}
			value.ObjectRevision++
			if route == "foreign-principal" {
				foreign := engine.New()
				value.Owner = foreign.Roles().LookupRef("postgres")
			}
			next := catalog.CloneState(stage.Catalog.State)
			next.SetDescriptor(address, &value)
			stage.Catalog.PublishLocked(next)
			if route != "acl" {
				if err := repairs.Touch(address, true); err != nil {
					t.Fatal(err)
				}
			}
			if route != "owner" {
				if err := repairs.Touch(address, false); err != nil {
					t.Fatal(err)
				}
			}
			if route == "stale" {
				// A real shared committed successor, without an unrelated role-record
				// revision change, isolates the exact descriptor predecessor fence.
				e.Mu.Lock()
				committed := catalog.CloneState(e.Catalog.State)
				changed := *original
				changed.ObjectRevision++
				changed.ACL = authorization.ACLValue{Explicit: true}
				committed.SetDescriptor(address, &changed)
				projection := e.Catalog.ProjectSharedLocked(committed, e.Cluster.Catalogs)
				e.Catalog.PublishLocked(committed)
				projection.InstallLocked()
				e.Mu.Unlock()
			}
			if route == "exhaustion" {
				e.Mu.Lock()
				e.Catalog.InstallRevisionFrontierLocked(ids.DefinitionRevision(^uint64(0)))
				e.Mu.Unlock()
			}
			type clocks struct {
				revision     ids.DefinitionRevision
				oid          ids.OID
				row, version uint64
			}
			frontiers := make(map[*core.Engine]clocks)
			for _, runtime := range e.Cluster.Targets {
				frontiers[runtime] = clocks{runtime.Catalog.NextRevision(), runtime.Catalog.NextOID(), runtime.Storage.RowFrontier(), runtime.Storage.VersionFrontier()}
			}
			roles := e.RoleStore.Published
			unchanged := databaseCreateUnitUnchanged(t, e)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if route == "cancel" {
				cancel()
			}
			publicationOp := op
			if route == "copied-operation" {
				copied := *op
				publicationOp = &copied
			}
			_, err = e.ReplaceCatalogInOperation(ctx, stage, stage.CatalogRead(), publicationOp)
			switch route {
			case "cancel", "stale", "foreign-principal", "exhaustion", "copied-operation":
				if err == nil {
					t.Fatal("invalid explicit repair published", route)
				}
				if route == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if route == "stale" && !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal(err)
				}
				if route == "exhaustion" && err.Error() != "stuntgres: catalog definition revision exhausted" {
					t.Fatal(err)
				}
				unchanged()
				for runtime, want := range frontiers {
					got := clocks{runtime.Catalog.NextRevision(), runtime.Catalog.NextOID(), runtime.Storage.RowFrontier(), runtime.Storage.VersionFrontier()}
					if got != want {
						t.Fatal("rejected explicit repair changed a target frontier", got, want)
					}
				}
				if e.RoleStore.Published != roles {
					t.Fatal("rejected explicit repair changed role authority")
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
				published := e.Catalog.State.Get(address).(*catalog.Tablespace)
				owner := original.Owner
				if route != "acl" {
					owner = e.Roles().LookupRef("pg_read_all_data")
				}
				if published.Owner != owner || published.ACL.Explicit != (route != "owner") {
					t.Fatal("untouched shared field was imported", published)
				}
				if peer.Catalog.State.Get(address) != published || peer.Catalog.State.Epoch() != peerEpoch || &peer.Storage != peerStore {
					t.Fatal("repair was not atomic across exact shared/local identities")
				}
				if original.Owner != e.Roles().LookupRef("postgres") || original.ACL.Explicit {
					t.Fatal("retained predecessor mutated")
				}
			}
			if session.PrivateSessionOf(maintenance.SessionToken) != maintenance || maintenance.SessionToken.ID() != 0 {
				t.Fatal("replay did not use private physical identity")
			}
		})
	}
}

// Public lifecycle controls assert the SQL/data/privilege image. This owning
// fixture additionally retains the actual catalog/storage roots and allocator
// frontiers, so rejected replay cannot publish even an equal-looking successor.
func databaseAlterStageNamePublication(t *testing.T) {
	e, seed := engine.New(), engine.New()
	selected := databaseCreateUnitInstall(t, e, seed, "selected")
	databaseCreateUnitInstall(t, e, seed, "peer")
	for _, runtime := range e.Cluster.Targets {
		if err := engine.Restore(t.Context(), runtime, strings.NewReader("CREATE TABLE repair_retained(v integer PRIMARY KEY); INSERT INTO repair_retained VALUES (42); CREATE SEQUENCE repair_sequence START WITH 41;")); err != nil {
			t.Fatal("initialize retained local roots", err)
		}
	}
	op, done := enginetest.RoleOperation(t, e, "postgres")
	enginetest.RoleExecute(t, e, op, "ALTER TABLESPACE pg_global RENAME TO repair_destination_alias")
	enginetest.RoleExecute(t, e, op, "GRANT CREATE ON TABLESPACE repair_destination_alias TO pg_read_all_data WITH GRANT OPTION")
	done()

	unchanged := databaseCreateUnitUnchanged(t, e)
	roles := e.RoleStore.Published
	var checkRoots []func()
	for _, runtime := range e.Cluster.Targets {
		root := runtime.Catalog.State
		domain, epoch := runtime.Catalog.Domain, root.Epoch()
		database := root.MustDatabase(runtime.Catalog.DatabaseAddress())
		revision, oid := runtime.Catalog.NextRevision(), runtime.Catalog.NextOID()
		row, version := runtime.Storage.RowFrontier(), runtime.Storage.VersionFrontier()
		relations := maps.Clone(runtime.Storage.Relations)
		indexes := maps.Clone(runtime.Storage.IndexRoots)
		sequences := maps.Clone(runtime.Storage.Sequences)
		checkRoots = append(checkRoots, func() {
			t.Helper()
			if runtime.Catalog.State != root || runtime.Catalog.Domain != domain || runtime.Catalog.State.Epoch() != epoch || runtime.Catalog.State.MustDatabase(runtime.Catalog.DatabaseAddress()) != database {
				t.Fatal("rejected stage-name repair changed a target catalog or attachment")
			}
			if runtime.Catalog.NextRevision() != revision || runtime.Catalog.NextOID() != oid || runtime.Storage.RowFrontier() != row || runtime.Storage.VersionFrontier() != version {
				t.Fatal("rejected stage-name repair changed a target publication frontier")
			}
			if !maps.Equal(runtime.Storage.Relations, relations) || !maps.Equal(runtime.Storage.IndexRoots, indexes) || !maps.Equal(runtime.Storage.Sequences, sequences) {
				t.Fatal("rejected stage-name repair replaced retained local backing")
			}
		})
	}
	// A valid prefix produces real local rows and a touched shared delta. It
	// must stay isolated when the later exact stage-name lookup fails.
	prefix := `CREATE TABLE repair_unpublished(v integer); INSERT INTO repair_unpublished VALUES (123); UPDATE pg_catalog.pg_tablespace SET spcowner='pg_read_all_data'::pg_catalog.regrole WHERE spcname='pg_default'::pg_catalog.name; UPDATE pg_catalog.pg_tablespace SET spcacl=pg_catalog.acldefault('c'::pg_catalog."char",0::pg_catalog.oid)::pg_catalog.aclitem[] WHERE spcname='pg_default'::pg_catalog.name; `
	for _, probe := range []struct {
		name, setup, target string
	}{
		{"destination-alias-without-setup", "", "repair_destination_alias"},
		{"wrong-stage-alias", "ALTER TABLESPACE pg_global RENAME TO repair_stage_other; ", "repair_destination_alias"},
		{"displaced-canonical-name", "ALTER TABLESPACE pg_global RENAME TO repair_stage_other; ", "pg_global"},
	} {
		input := prefix + probe.setup + "UPDATE pg_catalog.pg_tablespace SET spcacl=NULL::pg_catalog.aclitem[] WHERE spcname='" + probe.target + "'::pg_catalog.name;"
		err := engine.Restore(t.Context(), selected, strings.NewReader(input))
		if err == nil || err.Error() != "stuntgres: missing object ACL repair target" {
			t.Fatal(probe.name, err)
		}
		unchanged()
		for _, check := range checkRoots {
			check()
		}
		if e.RoleStore.Published != roles {
			t.Fatal("rejected stage-name repair changed role authority")
		}
	}
}
