package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// TYPE UNIQUE rebuilds replace one backing relation with a new identity under
// the same name. CatalogStage must require the exact staged incumbent removal.
func TestUniqueCatalogStageExactNameReuse(t *testing.T) {
	for _, mode := range []string{"valid", "missing_removal", "unrelated_removal", "stale_removal", "foreign_removal", "reused_address", "duplicate", "duplicate_address", "concurrent", "name_reassignment", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			e := New()
			run(t, e, "CREATE TABLE t(k INT)")
			run(t, e, "CREATE INDEX idx ON t(k)")
			run(t, e, "CREATE INDEX other ON t(k)")
			base := e.CatalogRead()
			oldRef := roleTableRef(t, e, "idx")
			old := base.MustRelation(oldRef.Address)
			next := *old
			mint := func(d *catalog.Relation) {
				e.mu.Lock()
				id, err := e.catalog.allocateOIDLocked()
				revision, revisionErr := e.catalog.revisionLocked()
				e.mu.Unlock()
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
				if err := stage.Remove(roleTableRef(t, e, "other")); err != nil {
					t.Fatal(err)
				}
				reject = true
			case "stale_removal", "foreign_removal":
				invalid := oldRef
				if mode == "stale_removal" {
					invalid.Epoch++
				} else {
					invalid = New().catalog.Domain.Ref(invalid.Epoch, invalid.Address)
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
			private := e.stageRead(stage)
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
				if _, err := e.PublishCatalog(p, PublicationToken{Epoch: base.Epoch()}); err != nil {
					t.Fatal(err)
				}
			}
			before := e.CatalogRead()
			if mode == "canceled" {
				cancel()
			}
			_, err = e.PublishCatalog(prepared, PublicationToken{Epoch: base.Epoch()})
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
				if _, ok := e.catalog.state.Find(oldRef.Address); ok {
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
	e := New()
	run(t, e, "CREATE TABLE t(k INT CONSTRAINT u UNIQUE)")
	ref := roleTableRef(t, e, "t")
	parent := e.catalog.state.MustRelation(ref.Address)
	index := parent.UniqueConstraints[0].Index
	if e.storage[ref.Address] == nil || e.indexRoots[index] == nil || len(e.indexRoots[index].entries) != 0 {
		t.Fatal("co-created parent and empty backing root did not publish together")
	}
	captured := captureIndexStores(e.storage, e.catalog.state)
	if !indexStoresUnchanged(e.storage, captured, e.catalog.state, e.catalog.state, e.indexRoots, nil) {
		t.Fatal("unchanged existing parent failed its storage fence")
	}
	if indexStoresUnchanged(nil, captured, e.catalog.state, e.catalog.state, e.indexRoots, nil) {
		t.Fatal("existing parent disappearance passed its storage fence")
	}
	absent := &catalog.State{}
	if !indexStoresUnchanged(nil, nil, absent, e.catalog.state, e.indexRoots, nil) {
		t.Fatal("new parent must fence committed absence, not its candidate store")
	}
	if indexStoresUnchanged(e.storage, nil, absent, e.catalog.state, e.indexRoots, nil) ||
		indexStoresUnchanged(nil, captured, absent, e.catalog.state, e.indexRoots, nil) {
		t.Fatal("new-parent fence accepted committed or captured incumbent storage")
	}
	changed := *e.storage[ref.Address]
	changed.generation++
	if indexStoresUnchanged(map[ids.ObjectAddress]*relationStorage{ref.Address: &changed}, captured, e.catalog.state, e.catalog.state, e.indexRoots, nil) {
		t.Fatal("existing parent generation change passed its storage fence")
	}
	before, root, stores := e.catalog.state, e.indexRoots[index], len(e.storage)
	_, err := testExecute(e, context.Background(), statement(t, "CREATE TABLE other(k INT CONSTRAINT u UNIQUE)"), nil)
	if err == nil || e.catalog.state != before || len(e.storage) != stores || e.indexRoots[index] != root {
		t.Fatal("failed backing-name reservation partially published a new parent", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = testExecute(e, ctx, statement(t, "CREATE TABLE canceled(k INT UNIQUE)"), nil)
	if !errors.Is(err, context.Canceled) || e.catalog.state != before || len(e.storage) != stores || e.indexRoots[index] != root {
		t.Fatal("canceled atomic parent/backing publication changed state", err)
	}
}
