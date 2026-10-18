package engine

import (
	"context"
	"testing"
)

func TestSerialDefaultMergedRootProviderValidation(t *testing.T) {
	e := New()
	run(t,e,`CREATE SEQUENCE q`)
	run(t,e,`CREATE TABLE consumer(id INT DEFAULT nextval('q'))`)
	ref := roleTableRef(t,e,"consumer")
	definition := e.catalog.state.descriptors[ref.Address].(*relationDescriptor).defaults[1]
	run(t,e,`ALTER TABLE consumer ALTER id DROP DEFAULT`)
	read := e.CatalogRead()
	original := read.state.descriptors[ref.Address].(*relationDescriptor)
	candidate := *original
	candidate.revision++
	candidate.defaults = map[int32]columnDefault{1:definition}
	stage := e.BeginCatalogStage(read)
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(&candidate); err != nil { t.Fatal(err) }
	prepared,err := e.ValidateCatalogStage(context.Background(),stage)
	if err != nil { t.Fatal("valid captured provider rejected",err) }
	run(t,e,`DROP SEQUENCE q RESTRICT`)
	run(t,e,`CREATE SEQUENCE q START 99`)
	before := e.catalog.state
	if _,err := e.PublishCatalog(prepared,PublicationToken{Epoch:read.Epoch()}); err == nil { t.Fatal("merged root published a default referencing a removed provider") }
	if e.catalog.state != before || e.catalog.state.descriptors[ref.Address] != original { t.Fatal("rejected default publication changed catalog") }
	checkReviewDependencyValid(t,e)
}
