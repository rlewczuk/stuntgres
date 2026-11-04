package writes_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func statisticsViewsInput(in pgcatalog.Input, oid ids.OID) pgcatalog.Input {
	in.Source = in.Catalog.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid})
	in.Ref = in.Owner.Domain.Ref(in.Catalog.Epoch(), in.Source.Address())
	return in
}

func statisticsViewsProduce(t *testing.T, in pgcatalog.Input) [][]sqltype.Value {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(in.Source.Address().ObjectID)
	if !ok {
		t.Fatal("missing registered statistics producer")
	}
	rows, err := produce(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if len(row) != len(in.Source.Columns) {
			t.Fatal("row width")
		}
		for i, value := range row {
			if !value.ValidResult(in.Source.Columns[i].Usage) {
				t.Fatal("foreign result ownership", i)
			}
		}
	}
	return rows
}

func statisticsViewsFind(t *testing.T, rows [][]sqltype.Value, name string) []sqltype.Value {
	t.Helper()
	for _, row := range rows {
		text, _ := row[3].Text()
		if text == name {
			return row
		}
	}
	t.Fatal("missing statistics definition", name)
	return nil
}

func TestStatisticsViewsProjection(t *testing.T) {
	e, s, ref := expressionCollectionFixture(t)
	enginetest.SessionSQL(t, s, "CREATE STATISTICS expr_joint ON b,(a+1) FROM expr_owner")
	enginetest.SessionSQL(t, s, "ANALYZE expr_owner")
	key := ndistinctKey(t, e, "expr_joint")
	facts := analyzeFacts(e, ref.Address)
	fact := facts.Extended[key]
	data := fact.MCVData
	store := e.Storage.Relations[ref.Address]
	if data == nil || data.Rows != 4 || len(data.Items) != 3 {
		t.Fatal("fixture lacks retained construction facts")
	}
	itemBacking := &data.Items[0]
	dictionaryBacking := &data.Dictionaries[0][0]
	statisticExtDataInput(t, e, func(in pgcatalog.Input, _ core.AuthorizationView) {
		in = statisticsViewsInput(in, pgcatalog.StatsExtOID)
		row := statisticsViewsFind(t, statisticsViewsProduce(t, in), "expr_joint")
		for _, value := range row[11:13] {
			if value.ArrayRank() != 2 || value.ArrayDimension(0) != (sqltype.ArrayDimension{Lower: 1, Length: 3}) || value.ArrayDimension(1) != (sqltype.ArrayDimension{Lower: 1, Length: 2}) {
				t.Fatal("MCV shape lost", value.ArrayDimensions())
			}
		}
		for i, item := range data.Items {
			frequency, _ := row[13].ArrayElement(i).Float()
			baseline, _ := row[14].ArrayElement(i).Float()
			wantBase := 1.0
			for dimension := range 2 {
				wantBase *= float64(item.MarginalCounts[dimension]) / 4
				flag, _ := row[12].ArrayElement(i*2 + dimension).Boolean()
				value := row[11].ArrayElement(i*2 + dimension)
				if flag != item.Nulls[dimension] || value.IsNull() != flag {
					t.Fatal("NULL flags no longer describe actual NULL cells")
				}
				if !flag {
					original := data.Dictionaries[dimension][item.Indexes[dimension]]
					want, err := original.Type().Output(t.Context(), original)
					if err != nil {
						t.Fatal(err)
					}
					got, _ := value.Text()
					if got != want {
						t.Fatal("wrong retained dimension", got, want)
					}
				}
			}
			if frequency != float64(item.Count)/4 || baseline != wantBase {
				t.Fatal("MCV count/base projection", frequency, baseline)
			}
		}
		// Exported dimensions are detached, not mutable access to captured shape.
		dimensions := row[11].ArrayDimensions()
		dimensions[0].Length = 99
		if row[11].ArrayDimension(0).Length != 3 {
			t.Fatal("array dimensions borrowed")
		}
		// The expression row uses its genuine element descriptor beneath ANYARRAY.
		expr := statisticsViewsFind(t, statisticsViewsProduce(t, statisticsViewsInput(in, pgcatalog.StatsExtExprsOID)), "expr_joint")
		usage, ok := expr[10].StatisticArrayUsage()
		if !ok || usage != fact.Expression(0).Usage || expr[10].ArrayElement(0).Type() != usage.Type {
			t.Fatal("expression nominal/element authority diverged")
		}
	})
	if analyzeFacts(e, ref.Address) != facts || facts.Extended[key] != fact || fact.MCVData != data || &data.Items[0] != itemBacking || &data.Dictionaries[0][0] != dictionaryBacking || e.Storage.Relations[ref.Address] != store {
		t.Fatal("catalog read changed actual immutable backing")
	}
	t.Run("imported slots", statisticsViewsImportedSlots)
	t.Run("imported range slots", statisticsViewsImportedRangeSlots)
}

// This uses authentic constructor-owned summaries before publishing them to the
// test store. No SQL parser, live rescan or fabricated serialized MCV is involved.
func statisticsViewsImportedSlots(t *testing.T) {
	e, _, ref := expressionCollectionFixture(t)
	key := ndistinctKey(t, e, "expr_a")
	read := e.CatalogRead()
	definition, _ := read.Statistics(key.Definition.Address)
	usage := definition.Expressions.At(0).Usage()
	collector, err := storage.NewExtendedCollector(storage.ExtendedInput{Key: key, Definition: definition, Columns: []storage.StatisticsColumn{{Usage: usage, Slot: 0, Target: 100}}, Providers: storage.CaptureStatisticsProviders(read, definition)})
	if err != nil {
		t.Fatal(err)
	}
	one, _ := sqltype.Integer(usage.Type, 1)
	if err := collector.Add(t.Context(), []sqltype.Value{one}); err != nil {
		t.Fatal(err)
	}
	fact, err := collector.Finish(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	anyarray, _ := read.LookupType(2277)
	floatArray, _ := read.LookupType(1021)
	values, err := sqltype.StatisticArrayValue(anyarray, usage, []sqltype.ArrayDimension{{Lower: 1, Length: 1}}, []sqltype.Value{one})
	if err != nil {
		t.Fatal(err)
	}
	probability, _ := sqltype.Float(floatArray.Element(), .25)
	numbers, err := sqltype.ArrayValue(floatArray, []sqltype.ArrayDimension{{Lower: 1, Length: 1}}, []sqltype.Value{probability})
	if err != nil {
		t.Fatal(err)
	}
	imported := &storage.ImportedStatistics{NullFractionSet: true, NullFraction: .25, DistinctSet: true, Distinct: -.5}
	for _, slot := range []storage.AttributeStatisticsSlot{{Kind: 1, Values: values, Numbers: numbers}, {Kind: 2, Values: values}, {Kind: 3, Numbers: numbers}, {Kind: 4, Values: values, Numbers: numbers}, {Kind: 5, Numbers: numbers}} {
		if err := imported.SetSlot(slot); err != nil {
			t.Fatal(err)
		}
	}
	fact.Expression(0).Imported = imported
	metadata := *e.Storage.RelationMetadata[ref.Address]
	metadata.Statistics = storage.MergeExtendedStatistics(metadata.Statistics, map[storage.ExtendedStatisticsKey]*storage.ExtendedStatistics{key: fact})
	e.Storage.RelationMetadata[ref.Address] = &metadata
	statisticExtDataInput(t, e, func(in pgcatalog.Input, _ core.AuthorizationView) {
		row := statisticsViewsFind(t, statisticsViewsProduce(t, statisticsViewsInput(in, pgcatalog.StatsExtExprsOID)), "expr_a")
		for _, column := range []int{7, 13} {
			got, _ := row[column].Float()
			if got != .25 {
				t.Fatal("imported scalar slot lost", column, got)
			}
		}
		got, _ := row[9].Float()
		if got != -.5 {
			t.Fatal("imported distinct lost", got)
		}
		// Value is comparable: these assert the actual immutable container
		// backing is reused, not just that a reconstruction happens to be equal.
		for _, column := range []int{10, 12, 14} {
			if row[column] != values {
				t.Fatal("imported ANYARRAY backing changed", column)
			}
		}
		for _, column := range []int{11, 15, 16} {
			if row[column] != numbers {
				t.Fatal("imported numbers backing changed", column)
			}
		}
	})
	if fact.Expression(0).Imported != imported || e.Storage.RelationMetadata[ref.Address] != &metadata {
		t.Fatal("projection replaced source-owned imported summary")
	}
}

type statisticsViewsCancelRuntime struct {
	pgcatalog.Runtime
	cancel  context.CancelFunc
	corrupt bool
}

func (r statisticsViewsCancelRuntime) VisitExtendedStatisticsView(ctx context.Context, source *catalog.Relation, visit func([]sqltype.Value) error) error {
	return r.Runtime.VisitExtendedStatisticsView(ctx, source, func(row []sqltype.Value) error {
		if err := visit(row); err != nil {
			return err
		}
		if r.corrupt {
			bad := append([]sqltype.Value(nil), row...)
			bad[0] = sqltype.Value{}
			return visit(bad)
		}
		r.cancel()
		return nil
	})
}

func (r statisticsViewsCancelRuntime) VisitAttributeStatistics(ctx context.Context, source *catalog.Relation, visit func([]sqltype.Value) error) error {
	return r.Runtime.VisitAttributeStatistics(ctx, source, func(row []sqltype.Value) error {
		if err := visit(row); err != nil {
			return err
		}
		if r.corrupt {
			bad := append([]sqltype.Value(nil), row...)
			bad[0] = sqltype.Value{}
			return visit(bad)
		}
		r.cancel()
		return nil
	})
}

func TestStatisticsViewsCaptureCancellation(t *testing.T) {
	e, _, ref := expressionCollectionFixture(t)
	before := analyzeFacts(e, ref.Address)
	for _, oid := range []ids.OID{pgcatalog.StatsOID, pgcatalog.StatsExtOID, pgcatalog.StatsExtExprsOID} {
		statisticExtDataInput(t, e, func(in pgcatalog.Input, _ core.AuthorizationView) {
			in = statisticsViewsInput(in, oid)
			produce, _ := pgcatalog.ProducerFor(oid)
			for _, corrupt := range []bool{false, true} {
				ctx, cancel := context.WithCancel(in.Context)
				bad := in
				bad.Context = ctx
				bad.Runtime = statisticsViewsCancelRuntime{Runtime: in.Runtime, cancel: cancel, corrupt: corrupt}
				rows, err := produce(bad)
				cancel()
				if rows != nil || err == nil || !corrupt && !errors.Is(err, context.Canceled) {
					t.Fatal("partial capture escaped failure", oid, corrupt, len(rows), err)
				}
			}
		})
	}
	if analyzeFacts(e, ref.Address) != before {
		t.Fatal("failed capture mutated statistics")
	}
}
