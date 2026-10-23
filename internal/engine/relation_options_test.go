package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// These assertions exercise catalog providers directly; incidental fixture SQL
// only constructs a real relation, not a duplicate public SQL scenario.
func TestRelationOptionsMetadataPublication(t *testing.T) {
	e, _, done, old := policyUnitFixture(t)
	defer done()
	base := e.CatalogRead()
	store := e.storage.Relations[old.Address()]
	candidate, err := table.RelationOptionsCandidate(old, parser.AlterTableAction{Action: parser.SetRelationOptions, Options: []parser.IndexOption{{Name: parser.Identifier{Text: "fillfactor"}, Value: parser.Value{Kind: parser.IntegerValue, Integer: 70, Text: "70"}, HasValue: true}}})
	if err != nil {
		t.Fatal(err)
	}
	candidate.ObjectRevision++
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(candidate); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	prepared, err := e.ValidateCatalogStage(ctx, stage)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: base.Epoch()}); !errors.Is(err, context.Canceled) {
		t.Fatal("final cancellation fence", err)
	}
	if e.CatalogRead().State() != base.State() || e.storage.Relations[old.Address()] != store {
		t.Fatal("canceled stage published")
	}
	staleStage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(staleStage)
	if err := staleStage.Replace(candidate); err != nil {
		t.Fatal(err)
	}
	stale, err := e.ValidateCatalogStage(context.Background(), staleStage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.PublishCatalog(stale, PublicationToken{Epoch: base.Epoch() + 1}); err == nil {
		t.Fatal("replacement epoch fence accepted")
	}
	if e.CatalogRead().State() != base.State() || e.storage.Relations[old.Address()] != store {
		t.Fatal("epoch failure published")
	}
	stage2 := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage2)
	if err := stage2.Replace(candidate); err != nil {
		t.Fatal(err)
	}
	prepared, err = e.ValidateCatalogStage(context.Background(), stage2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	current := e.CatalogRead().MustRelation(old.Address())
	if current == old || current.Revision() != old.Revision()+1 || current.RowType != old.RowType || &current.Columns[0] != &old.Columns[0] || &current.Attributes[0] != &old.Attributes[0] || e.storage.Relations[old.Address()] != store {
		t.Fatal("option publication changed heap/shape/identity")
	}
	if len(old.StorageOptions.Base) != 0 || len(current.StorageOptions.Base) != 1 || current.StorageOptions.Base[0].Integer != 70 {
		t.Fatal("snapshot or normalized intent lost")
	}
	if current.RelationKind != catalog.TableRelation {
		t.Fatal("relation kind changed")
	}
	// The earlier candidate still names the old descriptor at the same epoch.
	// This proves a current-identity fence independently of transactional RENAME.
	published := e.CatalogRead()
	_, err = e.PublishCatalog(stale, PublicationToken{Epoch: base.Epoch()})
	var conflict catalog.Conflict
	if !errors.As(err, &conflict) {
		t.Fatal("stale relation identity accepted", err)
	}
	if e.CatalogRead().State() != published.State() || e.CatalogRead().MustRelation(old.Address()) != current || e.storage.Relations[old.Address()] != store {
		t.Fatal("stale identity failure changed catalog or storage")
	}
}
