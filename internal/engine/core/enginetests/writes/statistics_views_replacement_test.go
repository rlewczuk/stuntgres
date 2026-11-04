package writes_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/pgcatalog"
)

func TestStatisticsViewsReplacementOwnership(t *testing.T) {
	e, s, ref := expressionCollectionFixture(t)
	enginetest.SessionSQL(t, s, "CREATE STATISTICS expr_joint ON b,(a+1) FROM expr_owner")
	enginetest.SessionSQL(t, s, "ANALYZE expr_owner")
	key := ndistinctKey(t, e, "expr_joint")
	before := e.CatalogRead()
	old := analyzeFacts(e, ref.Address).Extended[key]
	data := old.MCVData
	itemBacking := &data.Items[0]
	dictionaryBacking := &data.Dictionaries[0][0]
	captured := statisticsViewsCaptured(t, e, pgcatalog.StatsExtOID)
	row := statisticsViewsFind(t, captured.Anonymous, "expr_joint")
	values := row[11]
	rowBacking := &row[0]
	enginetest.SessionSQL(t, s, "INSERT INTO expr_owner VALUES(9,90,90)")
	// A same-engine raw catalog replacement intentionally keeps only genuine
	// destination TEMP metadata. Use the foreign replacement route here: it
	// captures semantic statistics with StatisticsSnapshotCodec and imports
	// them under the destination definitions at the installation boundary.
	source, sourceSession, sourceRef := expressionCollectionFixture(t)
	enginetest.SessionSQL(t, sourceSession, "CREATE STATISTICS expr_joint ON b,(a+1) FROM expr_owner")
	enginetest.SessionSQL(t, sourceSession, "ANALYZE expr_owner")
	sourceKey := ndistinctKey(t, source, "expr_joint")
	sourceRoot := analyzeFacts(source, sourceRef.Address)
	sourceFact := sourceRoot.Extended[sourceKey]
	sourceData := sourceFact.MCVData
	sourceItems := &sourceData.Items[0]
	sourceDictionary := &sourceData.Dictionaries[0][0]
	enginetest.SessionSQL(t, sourceSession, "INSERT INTO expr_owner VALUES(9,90,90)")
	sourceRead := source.CatalogRead()
	sourceRows := source.Storage.Relations[sourceRef.Address]
	if sourceRead.Domain() == before.Domain() || sourceData.Rows != 4 || len(sourceRows.Versions) != 5 {
		t.Fatal("fixture lacks independent stale semantic source")
	}
	if _, err := e.ReplaceCatalog(t.Context(), source, sourceRead); err != nil {
		t.Fatal(err)
	}
	after := e.CatalogRead()
	newRef := enginetest.CatalogRef(t, e, after, "expr_owner")
	newKey := ndistinctKey(t, e, "expr_joint")
	freshRoot := analyzeFacts(e, newRef.Address)
	if freshRoot == nil || freshRoot.Extended[newKey] == nil {
		t.Fatal("semantic replacement omitted collected statistics")
	}
	fresh := freshRoot.Extended[newKey]
	if after.Epoch() != before.Epoch()+1 || after.Domain() != before.Domain() || newKey.Definition.Epoch != after.Epoch() || fresh == old || fresh.MCVData == data || fresh.MCVData.Rows != 4 || &fresh.MCVData.Items[0] == itemBacking || &fresh.MCVData.Dictionaries[0][0] == dictionaryBacking || fresh.Usages[0].Type == old.Usages[0].Type {
		t.Fatal("replacement did not reown real source generation/backing")
	}
	if fresh == sourceFact || fresh.MCVData == sourceData || &fresh.MCVData.Items[0] == sourceItems || &fresh.MCVData.Dictionaries[0][0] == sourceDictionary || len(e.Storage.Relations[newRef.Address].Versions) != 5 {
		t.Fatal("semantic import adopted source backing or lost the genuine live population")
	}
	if source.CatalogRead() != sourceRead || analyzeFacts(source, sourceRef.Address) != sourceRoot || sourceRoot.Extended[sourceKey] != sourceFact || sourceFact.MCVData != sourceData || &sourceData.Items[0] != sourceItems || &sourceData.Dictionaries[0][0] != sourceDictionary || source.Storage.Relations[sourceRef.Address] != sourceRows {
		t.Fatal("semantic capture/import changed the source owner or immutable backing")
	}
	newCapture := statisticsViewsCaptured(t, e, pgcatalog.StatsExtOID)
	newRow := statisticsViewsFind(t, newCapture.Anonymous, "expr_joint")
	if newCapture.Ref.Epoch != after.Epoch() || captured.Ref.Epoch != before.Epoch() || newRow[11].Type() == values.Type() || newRow[11] == values || values.ValidResult(after.MustRelation(newCapture.Ref.Address).Columns[11].Usage) {
		t.Fatal("view artifact crossed replacement descriptor authority")
	}
	if equal, err := newRow[11].SamePayload(t.Context(), values); err != nil || !equal {
		t.Fatal("replacement changed retained stale cells", err)
	}
	if &row[0] != rowBacking || row[11] != values || old.MCVData != data || &data.Items[0] != itemBacking || &data.Dictionaries[0][0] != dictionaryBacking || data.Rows != 4 {
		t.Fatal("replacement mutated retired immutable source/capture")
	}
	accepted := e.Storage.RelationMetadata[newRef.Address]
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.ReplaceCatalog(ctx, source, sourceRead); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled replacement admitted", err)
	}
	if e.CatalogRead() != after || e.Storage.RelationMetadata[newRef.Address] != accepted || accepted.Statistics.Extended[newKey] != fresh || newRow[11].Type() != after.MustRelation(newCapture.Ref.Address).Columns[11].Usage.Type || row[11] != values {
		t.Fatal("rejected replacement changed accepted/retired artifacts")
	}
	if source.CatalogRead() != sourceRead || analyzeFacts(source, sourceRef.Address) != sourceRoot || sourceFact.MCVData != sourceData || &sourceData.Items[0] != sourceItems || &sourceData.Dictionaries[0][0] != sourceDictionary {
		t.Fatal("rejected semantic replacement changed source backing")
	}
}
