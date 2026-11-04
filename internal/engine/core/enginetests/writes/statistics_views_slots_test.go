package writes_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func statisticsViewsImportedRangeSlots(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE sv_range(r int4range)")
	ref, _ := vacuumStore(t, e, "sv_range")
	read := e.CatalogRead()
	relation := read.MustRelation(ref.Address)
	usage := relation.Columns[0].Usage
	float8, _ := read.LookupType(sqltype.Float8)
	floatArray, _ := read.LookupType(1021)
	anyarray, _ := read.LookupType(2277)
	length, _ := sqltype.Float(float8, 2)
	bound, err := usage.Type.Input(t.Context(), "[1,3)", -1)
	if err != nil {
		t.Fatal(err)
	}
	shape := []sqltype.ArrayDimension{{Lower: 1, Length: 1}}
	lengths, err := sqltype.StatisticArrayValue(anyarray, float8.Usage(), shape, []sqltype.Value{length})
	if err != nil {
		t.Fatal(err)
	}
	bounds, err := sqltype.StatisticArrayValue(anyarray, usage, shape, []sqltype.Value{bound})
	if err != nil {
		t.Fatal(err)
	}
	empty, _ := sqltype.Float(floatArray.Element(), .25)
	numbers, err := sqltype.ArrayValue(floatArray, shape, []sqltype.Value{empty})
	if err != nil {
		t.Fatal(err)
	}
	imported := &storage.ImportedStatistics{NullFractionSet: true, NullFraction: .125, DistinctSet: true, Distinct: -1}
	if err := imported.SetSlot(storage.AttributeStatisticsSlot{Kind: 6, Values: lengths, Numbers: numbers}); err != nil {
		t.Fatal(err)
	}
	if err := imported.SetSlot(storage.AttributeStatisticsSlot{Kind: 7, Values: bounds}); err != nil {
		t.Fatal(err)
	}
	// An imported summary has no manufactured measured row count. Its typed
	// specialized arrays are independently owned construction artifacts.
	fact := &storage.ColumnStatistics{Usage: usage, AverageWidth: 17, Imported: imported}
	metadata := storage.RelationMetadata{}
	if old := e.Storage.RelationMetadata[ref.Address]; old != nil {
		metadata = *old
	}
	metadata.Statistics = storage.MergeStatistics(metadata.Statistics, map[storage.StatisticsKey]*storage.ColumnStatistics{{Attribute: 1}: fact})
	if e.Storage.RelationMetadata == nil {
		e.Storage.RelationMetadata = make(map[ids.ObjectAddress]*storage.RelationMetadata)
	}
	e.Storage.RelationMetadata[ref.Address] = &metadata
	statisticExtDataInput(t, e, func(in pgcatalog.Input, _ core.AuthorizationView) {
		rows := statisticsViewsProduce(t, statisticsViewsInput(in, pgcatalog.StatsOID))
		if len(rows) != 1 {
			t.Fatal("unexpected genuine imported population", len(rows))
		}
		row := rows[0]
		if row[14] != lengths || row[16] != bounds || row[15] != empty {
			t.Fatal("range slots lost original immutable backing")
		}
		lengthUsage, _ := row[14].StatisticArrayUsage()
		boundsUsage, _ := row[16].StatisticArrayUsage()
		if lengthUsage != float8.Usage() || boundsUsage != usage {
			t.Fatal("range slot element usages replaced")
		}
		for _, column := range []int{7, 8, 9, 10, 11, 12, 13} {
			if !row[column].IsNull() {
				t.Fatal("invented unavailable specialized field", column)
			}
		}
	})
	if metadata.Statistics.Columns[storage.StatisticsKey{Attribute: 1}] != fact || fact.Imported != imported || fact.Rows != 0 {
		t.Fatal("read manufactured or replaced imported population")
	}
}
