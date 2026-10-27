package storage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestStatisticsExpressionCollectionProjected(t *testing.T) {
	types := statisticsTypes()
	integer := types[sqltype.Int4]
	ctx := context.Background()
	collector, err := NewProjectedStatisticsCollector(integer.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for i := range 20 {
		value := sqltype.Null(integer)
		if i < 16 {
			n := int64(2)
			if i >= 14 {
				n = 3
			}
			value, _ = sqltype.Integer(integer, n)
		}
		if err := collector.Add(ctx, value); err != nil {
			t.Fatal(err)
		}
	}
	fact, err := collector.Finish(ctx)
	if err != nil || fact.Rows != 20 || fact.NullCount != 4 || !fact.DistinctKnown || fact.DistinctCount != 2 || fact.AverageWidth != 4 || len(fact.MCV) != 2 || len(fact.Histogram) != 0 {
		t.Fatal(fact, err)
	}
	for i, count := range []uint64{14, 2} {
		n, err := fact.MCV[i].Value.Integer()
		if err != nil || n != int64(i+2) || fact.MCV[i].Count != count {
			t.Fatal(fact.MCV, err)
		}
	}
	distinct, known := fact.EncodedDistinct()
	if !known || distinct != 2 {
		t.Fatal(distinct, known)
	}
	allnull, _ := NewProjectedStatisticsCollector(integer.Usage())
	for range 5 {
		if err := allnull.Add(ctx, sqltype.Null(integer)); err != nil {
			t.Fatal(err)
		}
	}
	nullFact, err := allnull.Finish(ctx)
	if err != nil || nullFact.NullFraction() != 1 || nullFact.DistinctCount != 0 || nullFact.AverageWidth != 4 || len(nullFact.MCV) != 0 || len(nullFact.Histogram) != 0 {
		t.Fatal(nullFact, err)
	}
	unique, _ := NewProjectedStatisticsCollector(integer.Usage())
	for i := range 200 {
		value, _ := sqltype.Integer(integer, int64(i+2))
		if err := unique.Add(ctx, value); err != nil {
			t.Fatal(err)
		}
	}
	uniqueFact, err := unique.Finish(ctx)
	if err != nil || len(uniqueFact.MCV) != 0 || len(uniqueFact.Histogram) != 101 {
		t.Fatal(uniqueFact, err)
	}
	low, _ := uniqueFact.Histogram[0].Integer()
	high, _ := uniqueFact.Histogram[100].Integer()
	if low != 2 || high != 201 {
		t.Fatal(low, high)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if got, err := unique.Finish(canceled); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(got, err)
	}
	empty, _ := NewProjectedStatisticsCollector(integer.Usage())
	if got, err := empty.Finish(ctx); got != nil || err != nil {
		t.Fatal(got, err)
	}
	text, _ := NewProjectedStatisticsCollector(types[sqltype.Text].Usage())
	for _, value := range []string{"é", "é", "four"} {
		if err := text.Add(ctx, sqltype.String(types[sqltype.Text], value)); err != nil {
			t.Fatal(err)
		}
	}
	textFact, err := text.Finish(ctx)
	if err != nil || textFact.AverageWidth != 6 || textFact.MCV[0].Value.Type() != types[sqltype.Text] {
		t.Fatal(textFact, err)
	}
}

func expressionSnapshotFixture() ExtendedStatisticsSnapshot {
	usage := NDistinctUsageRecord{TypeSchema: "pg_catalog", TypeName: "int4", Typmod: -1}
	expression := StatisticsExpressionSnapshot{Definition: "(a + 1)", Usage: usage, Inputs: []int32{1}, InputUsages: []NDistinctUsageRecord{usage}, Rows: 20, NullCount: 4, DistinctCount: 2, DistinctKnown: true, AverageWidth: 4, MCV: []StatisticsMCVSnapshot{{Value: json.RawMessage(`{"kind":"scalar","scalar":"2"}`), Count: 14}, {Value: json.RawMessage(`{"kind":"scalar","scalar":"3"}`), Count: 2}}, Histogram: []json.RawMessage{}}
	return ExtendedStatisticsSnapshot{Schema: "public", Name: "expr", RelationSchema: "public", RelationName: "t", Keys: []int32{}, Kinds: "e", Usages: []NDistinctUsageRecord{}, Subsets: [][]int16{}, Counts: []uint64{}, Expressions: []StatisticsExpressionSnapshot{expression}}
}

func TestStatisticsExpressionSnapshotCodec(t *testing.T) {
	record := expressionSnapshotFixture()
	encoded, err := EncodeExtendedStatisticsSnapshot(record)
	if err != nil {
		t.Fatal(err)
	}
	decoded, recognized, err := DecodeExtendedStatisticsSnapshot(strings.TrimPrefix(encoded, "-- "))
	if err != nil || !recognized || len(decoded.Expressions) != 1 || decoded.Expressions[0].MCV[0].Count != 14 {
		t.Fatal(decoded, recognized, err)
	}
	raw, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(strings.TrimPrefix(encoded, "-- "+NDistinctSnapshotMarker+" ")))
	for _, bad := range []string{
		strings.Replace(string(raw), `"rows":20`, `"rows":3`, 1),
		strings.Replace(string(raw), `"null_count":4`, `"null_count":21`, 1),
		strings.Replace(string(raw), `"count":14`, `"count":0`, 1),
		strings.Replace(string(raw), `"distinct_known":true`, `"distinct_known":false`, 1),
		strings.Replace(string(raw), `"mcv":[`, `"mcv":null,"ignored":[`, 1),
		strings.Replace(string(raw), `"average_width":4`, `"average_width":-1`, 1),
		strings.Replace(string(raw), `"inputs":[1]`, `"inputs":[1,1]`, 1),
		strings.Replace(string(raw), `"definition":"(a + 1)"`, `"definition":null`, 1),
	} {
		_, recognized, err := DecodeExtendedStatisticsSnapshot(NDistinctSnapshotMarker + " " + base64.StdEncoding.EncodeToString([]byte(bad)))
		if !recognized || err == nil {
			t.Fatal("accepted malformed expression fact", bad, recognized, err)
		}
	}
	mixed := expressionSnapshotFixture()
	mixed.Keys = []int32{2}
	mixed.Usages = []NDistinctUsageRecord{mixed.Expressions[0].Usage}
	mixed.Kinds = "de"
	mixed.Subsets = [][]int16{{2, -1}}
	mixed.Counts = []uint64{3}
	if err := mixed.Validate(); err != nil {
		t.Fatal(err)
	}
	mixed.Subsets[0] = []int16{-1, 2}
	if err := mixed.Validate(); err == nil {
		t.Fatal("accepted noncanonical signed dimensions")
	}
}

func TestStatisticsExpressionCollectionAllNullWidth(t *testing.T) {
	types := statisticsTypes()
	for _, test := range []struct {
		id    sqltype.ID
		width int32
	}{
		{sqltype.Int2, 2}, {sqltype.Int4, 4}, {sqltype.Int8, 8},
		{sqltype.Bool, 1}, {sqltype.Date, 4}, {sqltype.Text, 0}, {sqltype.Bytea, 0},
	} {
		t.Run(types[test.id].Name(), func(t *testing.T) {
			collector, err := NewProjectedStatisticsCollector(types[test.id].Usage())
			if err != nil {
				t.Fatal(err)
			}
			for range 3 {
				if err := collector.Add(context.Background(), sqltype.Null(types[test.id])); err != nil {
					t.Fatal(err)
				}
			}
			fact, err := collector.Finish(context.Background())
			if err != nil || fact == nil {
				t.Fatal(fact, err)
			}
			if fact.AverageWidth != test.width || fact.NullCount != 3 || fact.Rows != 3 || fact.DistinctCount != 0 || len(fact.MCV) != 0 || len(fact.Histogram) != 0 {
				t.Fatal("all-NULL result lost its actual fixed width or invented variable width", fact)
			}
		})
	}
}
