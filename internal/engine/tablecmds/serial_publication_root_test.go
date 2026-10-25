package tablecmds_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
)

func TestSerialDefaultMergedRootProviderValidation(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, `CREATE SEQUENCE q`)
	enginetest.Run(t, e, `CREATE TABLE consumer(id INT DEFAULT nextval('q'))`)
	ref := enginetest.RoleTableRef(t, e, "consumer")
	definition := e.Catalog.State.MustRelation(ref.Address).ColumnMeta.Defaults[1]
	enginetest.Run(t, e, `ALTER TABLE consumer ALTER id DROP DEFAULT`)
	read := e.CatalogRead()
	original := read.MustRelation(ref.Address)
	candidate := *original
	candidate.ObjectRevision++
	candidate.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{1: definition}
	stage := e.BeginCatalogStage(read)
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(&candidate); err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal("valid captured provider rejected", err)
	}
	enginetest.Run(t, e, `DROP SEQUENCE q RESTRICT`)
	enginetest.Run(t, e, `CREATE SEQUENCE q START 99`)
	before := e.Catalog.State
	if _, err := enginetest.PublishCatalog(e, prepared, engine.PublicationToken{Epoch: read.Epoch()}); err == nil {
		t.Fatal("merged root published a default referencing a removed provider")
	}
	if e.Catalog.State != before || e.Catalog.State.Get(ref.Address) != original {
		t.Fatal("rejected default publication changed catalog")
	}
	checkReviewDependencyValid(t, e)
}
