package core_test

import (
	"slices"
	rootengine "stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/storage"
	"testing"
)

func TestAlterStatisticsPublication(t *testing.T) {
	e := rootengine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE statistics_prepared(a integer)", "INSERT INTO statistics_prepared VALUES(1),(2)", "ANALYZE statistics_prepared"} {
		enginetest.SessionSQL(t, s, q)
	}
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "statistics_prepared")
	descriptor := e.CatalogRead().MustRelation(ref.Address)
	metadata := e.Storage.RelationMetadata[ref.Address]
	fact := metadata.Statistics.Columns[storage.StatisticsKey{Attribute: 1}]
	ast, d := sequencePreparedDescription(t, e, s, "SELECT a,abs(-7) FROM statistics_prepared ORDER BY a")
	result, err := sequencePreparedExecute(e, s, ast, &d)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != nil {
		result.Rows.Close()
	}
	analysis, foldOwner := d.Analysis, d.Folds
	accepted := d.Folds.Current()
	if analysis == nil || len(analysis.Dependencies) == 0 || accepted.Generation == 0 || len(accepted.Entries) == 0 {
		t.Fatal("real prepared ownership absent")
	}
	oldEntries := slices.Clone(accepted.Entries)
	for i := range oldEntries {
		oldEntries[i].Inputs = slices.Clone(oldEntries[i].Inputs)
	}
	oldDependencies := slices.Clone(accepted.Dependencies)
	oldAnalysisDependencies := slices.Clone(analysis.Dependencies)
	// Unchanged execution must actually reuse this definition's accepted
	// generation/backing, not merely recompute an equal scalar.
	result, err = sequencePreparedExecute(e, s, ast, &d)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != nil {
		result.Rows.Close()
	}
	if d.Analysis != analysis || d.Folds != foldOwner || d.Folds.Current().Generation != accepted.Generation || &d.Folds.Current().Entries[0] != &accepted.Entries[0] {
		t.Fatal("unchanged execution did not retain actual prepared ownership/backing")
	}
	dependency := analysis.Dependencies[0]
	enginetest.SessionSQL(t, s, "ALTER TABLE statistics_prepared ALTER a SET STATISTICS 3, ALTER a SET(n_distinct=2)")
	changed := e.CatalogRead().MustRelation(ref.Address)
	if changed == descriptor || changed.Revision() <= descriptor.Revision() || preparedRebindValid(t, e, s, analysis) || e.Storage.RelationMetadata[ref.Address] != metadata {
		t.Fatal("metadata revision did not invalidate actual prepared analysis or eagerly replaced facts")
	}
	result, err = sequencePreparedExecute(e, s, ast, &d)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != nil {
		result.Rows.Close()
	}
	current := d.Folds.Current()
	// Relation definition dependencies retire root syntax on DDL. Reanalysis
	// preserves the live definition owner, not the obsolete root generation.
	if d.Analysis == analysis || d.Folds != foldOwner || !foldOwner.Live() || analysis.Dependencies[0] != dependency || current.Generation <= accepted.Generation || len(current.Entries) == 0 || &current.Entries[0] == &accepted.Entries[0] {
		t.Fatal("DDL did not refresh analysis/root generation under the same definition owner")
	}
	if len(accepted.Entries) != len(oldEntries) || !slices.Equal(accepted.Dependencies, oldDependencies) || !slices.Equal(analysis.Dependencies, oldAnalysisDependencies) {
		t.Fatal("rebind mutated retained accepted or analysis dependency backing")
	}
	for i, entry := range oldEntries {
		if !samePublishedFold(accepted.Entries[i], entry) {
			t.Fatal("rebind mutated a retained accepted datum", i)
		}
	}
	enginetest.SessionSQL(t, s, "ANALYZE statistics_prepared")
	fresh := e.Storage.RelationMetadata[ref.Address].Statistics.Columns[storage.StatisticsKey{Attribute: 1}]
	if fresh == fact || fresh.DistinctCount != 2 || fresh.DistinctOverride != 2 || fact.DistinctOverride != 0 || fact.DistinctCount != 2 {
		t.Fatal("ANALYZE failed immutable measured/estimate split")
	}
}
