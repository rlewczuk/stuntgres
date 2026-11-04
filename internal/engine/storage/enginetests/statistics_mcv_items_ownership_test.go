package enginetests_test

import (
	"encoding/json"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestStatisticsMCVItemsOwnership(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	// Create actual destination declarations through typed DDL stages, not SQL
	// queries or a substitute read/codec. There are deliberately no source rows.
	syntax := &parser.CreateTable{Name: enginetest.ConfigName("mcv_items_destination")}
	for _, name := range []string{"a", "b"} {
		syntax.Elements = append(syntax.Elements, parser.TableElement{Column: &parser.Column{Name: name, TypeName: parser.TypeName{Name: "integer"}}})
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	err = tablecmds.SchemaTableLocked(e, op, auth, stage, syntax, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, auth.State.CurrentUser, sqltype.InputEnvironment{}, nil)
	e.Mu.Unlock()
	if err != nil {
		e.DiscardCatalogStage(stage)
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	enginetest.PublishOperationStage(t, e, op, stage)
	declaration := &parser.CreateStatistics{Name: enginetest.ConfigName("mcv_items_destination_s"), Relation: syntax.Name, Elements: []parser.Expr{parser.ColumnExpr{Name: enginetest.ConfigName("a")}, parser.ColumnExpr{Name: enginetest.ConfigName("b")}}, Kinds: []parser.Identifier{{Text: "mcv"}}}
	statistics, err := enginetest.StatisticsUnitStage(t, e, op, declaration)
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(statistics)
	definition := enginetest.StatisticsUnitDescriptor(t, statistics)
	enginetest.PublishOperationStage(t, e, op, statistics)
	dictionary := []json.RawMessage{json.RawMessage(`{"kind":"scalar","scalar":"1"}`), json.RawMessage(`{"kind":"scalar","scalar":"2"}`)}
	record := storage.ExtendedStatisticsSnapshot{
		Schema: "public", Name: "mcv_items_destination_s", RelationSchema: "public", RelationName: "mcv_items_destination", Keys: []int32{1, 2}, Kinds: "m",
		Usages: []storage.NDistinctUsageRecord{{TypeSchema: "pg_catalog", TypeName: "int4", Typmod: -1}, {TypeSchema: "pg_catalog", TypeName: "int4", Typmod: -1}}, Subsets: [][]int16{}, Counts: []uint64{},
		MCV: &storage.ExtendedMCVSnapshot{Rows: 4, Dictionaries: [][]json.RawMessage{dictionary, dictionary}, Items: []storage.ExtendedMCVSnapshotItem{
			{Nulls: []bool{false, false}, Indexes: []uint16{0, 0}, Count: 2, MarginalCounts: []uint64{2, 2}},
			{Nulls: []bool{false, false}, Indexes: []uint16{1, 1}, Count: 1, MarginalCounts: []uint64{1, 1}},
			{Nulls: []bool{true, true}, Indexes: []uint16{0, 0}, Count: 1, MarginalCounts: []uint64{1, 1}},
		}},
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	read := e.Catalog.Current()
	codec := core.StatisticsSnapshotCodec(t.Context(), e, read)
	metadata, err := storage.RestoreExtendedStatisticsSnapshots(t.Context(), read, []storage.ExtendedStatisticsSnapshot{record}, codec)
	if err != nil {
		t.Fatal(err)
	}
	key := storage.ExtendedStatisticsKey{Definition: read.Domain().Ref(read.Epoch(), definition.Address())}
	fact := metadata[definition.Relation.Address].Statistics.Extended[key]
	view, err := fact.MCV.StatisticsMCV()
	if err != nil || view.Width() != 2 || view.Len() != 3 {
		t.Fatal("destination builder omitted typed construction", view, err)
	}
	integer, _ := read.LookupType(sqltype.Int4)
	list, _ := read.LookupType(sqltype.PgMCVList)
	if fact.MCV.Type() != list || fact.Usages[0].Type != integer || fact.Usages[1].Type != integer {
		t.Fatal("restored outer/dimension generation is foreign")
	}
	for index, expected := range []struct {
		integer             int64
		null                bool
		frequency, baseline float64
	}{{1, false, .5, .25}, {2, false, .25, .0625}, {0, true, .25, .0625}} {
		item, err := view.Item(index)
		if err != nil || item.Frequency != expected.frequency || item.BaseFrequency != expected.baseline {
			t.Fatal("retained snapshot arithmetic", item, err)
		}
		for dimension := range 2 {
			value, err := view.Value(index, dimension)
			if err != nil || value.Type() != integer || value.IsNull() != expected.null || item.Nulls[dimension] != expected.null {
				t.Fatal("destination dictionary identity", index, dimension, err)
			}
			if !expected.null {
				n, err := value.Integer()
				if err != nil || n != expected.integer {
					t.Fatal(n, err)
				}
			}
		}
	}
	// The collector's mutable count/dictionary owner is not the inspection owner.
	fact.MCVData.Dictionaries[0][0] = sqltype.Null(integer)
	fact.MCVData.Items[0].Count = 1
	value, err := view.Value(0, 0)
	n, integerErr := value.Integer()
	item, itemErr := view.Item(0)
	if err != nil || integerErr != nil || itemErr != nil || n != 1 || item.Frequency != .5 {
		t.Fatal("inspection retained mutable storage construction", n, item, err, integerErr, itemErr)
	}
}
