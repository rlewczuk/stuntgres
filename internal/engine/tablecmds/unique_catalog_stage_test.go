package tablecmds_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
)

// TYPE UNIQUE rebuilds replace one backing relation with a new identity under
// the same name. CatalogStage must require the exact staged incumbent removal.
func TestUniqueCatalogStageExactNameReuse(t *testing.T) {
	for _, mode := range []string{"valid", "missing_removal", "unrelated_removal", "stale_removal", "foreign_removal", "reused_address", "duplicate", "duplicate_address", "concurrent", "name_reassignment", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			e := engine.New()
			enginetest.Run(t, e, "CREATE TABLE t(k INT)")
			enginetest.Run(t, e, "CREATE INDEX idx ON t(k)")
			enginetest.Run(t, e, "CREATE INDEX other ON t(k)")
			base := e.CatalogRead()
			oldRef := enginetest.RoleTableRef(t, e, "idx")
			old := base.MustRelation(oldRef.Address)
			next := *old
			mint := func(d *catalog.Relation) {
				e.Mu.Lock()
				id, err := e.Catalog.AllocateOIDLocked()
				revision, revisionErr := e.Catalog.RevisionLocked()
				e.Mu.Unlock()
				if err != nil || revisionErr != nil {
					t.Fatal(err, revisionErr)
				}
				d.ObjectAddress.ObjectID = id
				d.ObjectRevision = revision
			}
			mint(&next)
			stage := e.BeginCatalogStage(base)
			reject := false
			switch mode {
			case "missing_removal":
				reject = true
			case "unrelated_removal":
				if err := stage.Remove(enginetest.RoleTableRef(t, e, "other")); err != nil {
					t.Fatal(err)
				}
				reject = true
			case "stale_removal", "foreign_removal":
				invalid := oldRef
				if mode == "stale_removal" {
					invalid.Epoch++
				} else {
					invalid = engine.New().Catalog.Domain.Ref(invalid.Epoch, invalid.Address)
				}
				if stage.Remove(invalid) == nil {
					t.Fatal("invalid removal admitted")
				}
				reject = true
			default:
				if err := stage.Remove(oldRef); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "reused_address" {
				next.ObjectAddress = old.Address()
				reject = true
			}
			err := stage.Create(&next)
			if reject {
				if err == nil {
					t.Fatal("reused name/address without coherent exact removal")
				}
				if e.CatalogRead().State() != base.State() {
					t.Fatal("failed staging changed catalog")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			private := e.StageRead(stage)
			if _, ok := private.Find(oldRef.Address); ok || private.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: old.Namespace(), Name: old.Name()}) != next.Address() {
				t.Fatal("private overlay retained incumbent")
			}
			if mode == "duplicate" || mode == "duplicate_address" {
				duplicate := next
				if mode == "duplicate" {
					mint(&duplicate)
				} else {
					duplicate.ObjectName = "different"
				}
				if stage.Create(&duplicate) == nil {
					t.Fatal("second replacement claimed name/address")
				}
				return
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			prepared, err := e.ValidateCatalogStage(ctx, stage)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "concurrent" || mode == "name_reassignment" {
				concurrent := e.BeginCatalogStage(base)
				changed := *old
				if mode == "concurrent" {
					changed.ObjectRevision++
					if err := concurrent.Replace(&changed); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := concurrent.Remove(oldRef); err != nil {
						t.Fatal(err)
					}
					mint(&changed)
					if err := concurrent.Create(&changed); err != nil {
						t.Fatal(err)
					}
				}
				p, err := e.ValidateCatalogStage(context.Background(), concurrent)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := enginetest.PublishCatalog(e, p, engine.PublicationToken{Epoch: base.Epoch()}); err != nil {
					t.Fatal(err)
				}
			}
			before := e.CatalogRead()
			if mode == "canceled" {
				cancel()
			}
			_, err = enginetest.PublishCatalog(e, prepared, engine.PublicationToken{Epoch: base.Epoch()})
			switch mode {
			case "concurrent", "name_reassignment":
				if err == nil || e.CatalogRead().State() != before.State() {
					t.Fatal("concurrent incumbent change published replacement", err)
				}
			case "canceled":
				if !errors.Is(err, context.Canceled) || e.CatalogRead().State() != before.State() {
					t.Fatal("canceled stage published", err)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
				if _, ok := e.Catalog.State.Find(oldRef.Address); ok {
					t.Fatal("old identity survived")
				}
				index, _ := btreeRoot(t, e, "idx")
				if index.Address() != next.Address() {
					t.Fatal("wrong name owner")
				}
			}
		})
	}
}

func TestUniqueCoCreatedParentStorageFence(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t(k INT CONSTRAINT u UNIQUE)")
	ref := enginetest.RoleTableRef(t, e, "t")
	parent := e.Catalog.State.MustRelation(ref.Address)
	idx := parent.Constraints.Unique[0].Index
	if e.Storage.Relations[ref.Address] == nil || e.Storage.IndexRoots[idx] == nil || len(e.Storage.IndexRoots[idx].Entries) != 0 {
		t.Fatal("co-created parent and empty backing root did not publish together")
	}
	captured := index.CaptureIndexStores(e.Storage.Relations, e.Catalog.State)
	if !index.StoresUnchanged(e.Storage.Relations, captured, e.Catalog.State, e.Catalog.State, e.Storage.IndexRoots, nil) {
		t.Fatal("unchanged existing parent failed its storage fence")
	}
	if index.StoresUnchanged(nil, captured, e.Catalog.State, e.Catalog.State, e.Storage.IndexRoots, nil) {
		t.Fatal("existing parent disappearance passed its storage fence")
	}
	absent := &catalog.State{}
	if !index.StoresUnchanged(nil, nil, absent, e.Catalog.State, e.Storage.IndexRoots, nil) {
		t.Fatal("new parent must fence committed absence, not its candidate store")
	}
	if index.StoresUnchanged(e.Storage.Relations, nil, absent, e.Catalog.State, e.Storage.IndexRoots, nil) ||
		index.StoresUnchanged(nil, captured, absent, e.Catalog.State, e.Storage.IndexRoots, nil) {
		t.Fatal("new-parent fence accepted committed or captured incumbent storage")
	}
	changed := *e.Storage.Relations[ref.Address]
	changed.Generation++
	if index.StoresUnchanged(map[ids.ObjectAddress]*storage.Relation{ref.Address: &changed}, captured, e.Catalog.State, e.Catalog.State, e.Storage.IndexRoots, nil) {
		t.Fatal("existing parent generation change passed its storage fence")
	}
	before, root, stores := e.Catalog.State, e.Storage.IndexRoots[idx], len(e.Storage.Relations)
	_, err := enginetest.Execute(e, context.Background(), enginetest.Statement(t, "CREATE TABLE other(k INT CONSTRAINT u UNIQUE)"), nil)
	if err == nil || e.Catalog.State != before || len(e.Storage.Relations) != stores || e.Storage.IndexRoots[idx] != root {
		t.Fatal("failed backing-name reservation partially published a new parent", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = enginetest.Execute(e, ctx, enginetest.Statement(t, "CREATE TABLE canceled(k INT UNIQUE)"), nil)
	if !errors.Is(err, context.Canceled) || e.Catalog.State != before || len(e.Storage.Relations) != stores || e.Storage.IndexRoots[idx] != root {
		t.Fatal("canceled atomic parent/backing publication changed state", err)
	}
}
