package core_test

import (
	"database/sql/driver"
	"fmt"
	"strings"
	"testing"

	rootengine "stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/storage"
)

// These are composed-owner witnesses: every fact is collected and published by
// ANALYZE, then selected by the real EXPLAIN source/flavor/population bridge.
// No constructed ColumnStatistics or replacement planner input is supplied.
func TestAlterStatisticsInheritedConsumer(t *testing.T) {
	for _, partitioned := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "partitioned"}[partitioned], func(t *testing.T) {
			e := rootengine.New()
			s := enginetest.StartSession(t, e, nil)
			root := "statistics_consumer"
			if partitioned {
				for _, query := range []string{
					"CREATE TABLE statistics_consumer(k integer,a integer,b integer) PARTITION BY RANGE(k)",
					"CREATE TABLE statistics_consumer0 PARTITION OF statistics_consumer FOR VALUES FROM(0) TO(1)",
					"CREATE TABLE statistics_consumer1 PARTITION OF statistics_consumer FOR VALUES FROM(1) TO(2)",
				} {
					enginetest.SessionSQL(t, s, query)
				}
				values := make([]string, 24)
				for i := range values {
					values[i] = fmt.Sprintf("(%d,%d,%d)", i/12, i+1, i+1)
				}
				enginetest.SessionSQL(t, s, "INSERT INTO "+root+" VALUES"+strings.Join(values, ","))
			} else {
				enginetest.SessionSQL(t, s, "CREATE TABLE statistics_consumer(a integer,b integer)")
				enginetest.SessionSQL(t, s, "CREATE TABLE statistics_consumer_child() INHERITS(statistics_consumer)")
				for _, segment := range []struct {
					name        string
					first, last int
				}{{root, 1, 8}, {"statistics_consumer_child", 9, 24}} {
					values := make([]string, 0, segment.last-segment.first+1)
					for i := segment.first; i <= segment.last; i++ {
						values = append(values, fmt.Sprintf("(%d,%d)", i, i))
					}
					enginetest.SessionSQL(t, s, "INSERT INTO "+segment.name+" VALUES"+strings.Join(values, ","))
				}
			}
			enginetest.SessionSQL(t, s, "ALTER TABLE "+root+" ALTER a SET(n_distinct=2,n_distinct_inherited=3)")
			enginetest.SessionSQL(t, s, "ANALYZE "+root)
			ref := enginetest.CatalogRef(t, e, e.CatalogRead(), root)
			descriptor := e.CatalogRead().MustRelation(ref.Address)
			attribute := descriptor.Attributes[descriptor.Index["a"]]
			key := storage.StatisticsKey{Attribute: attribute, Inherited: true}
			metadata := e.Storage.RelationMetadata[ref.Address]
			positive := metadata.Statistics.Columns[key]
			local := metadata.Statistics.Columns[storage.StatisticsKey{Attribute: attribute}]
			if !metadata.InheritedScanned || metadata.InheritedTuples != 24 || positive == nil || positive.Rows != 24 || positive.DistinctCount != 24 || positive.DistinctOverride != 3 || len(positive.MCV) != 0 {
				t.Fatal("missing genuine inherited publication", metadata, positive)
			}
			if partitioned {
				if e.Storage.Relations[ref.Address] != nil || local != nil {
					t.Fatal("partition parent gained synthetic heap/local fact")
				}
			} else if local == nil || local.Rows != 8 || local.DistinctCount != 8 || local.DistinctOverride != 2 || local == positive || e.Storage.Relations[ref.Address].Maintenance.Tuples != 8 {
				t.Fatal("local authority does not distinguish inherited source", local)
			}
			query := "EXPLAIN SELECT a FROM " + root + " WHERE a=1"
			ast, description := sequencePreparedDescription(t, e, s, query)
			read := func(want int) [][]driver.Value {
				t.Helper()
				result, err := sequencePreparedExecute(e, s, ast, &description)
				if err != nil {
					t.Fatal(err)
				}
				if result.Rows == nil {
					t.Fatal("missing actual EXPLAIN result")
				}
				defer result.Rows.Close()
				rows := enginetest.Tuples(result)
				if len(rows) == 0 || len(rows[0]) != 1 {
					t.Fatal(rows)
				}
				line, ok := rows[0][0].(string)
				if !ok || !strings.Contains(line, fmt.Sprintf("rows=%d ", want)) {
					t.Fatal("real inherited EXPLAIN bridge selected wrong fact/population", want, rows)
				}
				return rows
			}
			// Actual inherited24/distinct3=>8, whereas local8/distinct2=>4.
			retained := read(8)
			saved := fmt.Sprint(retained)
			if !partitioned {
				result := enginetest.SessionSQL(t, s, "EXPLAIN SELECT a FROM ONLY "+root+" WHERE a=1")
				rows := enginetest.Tuples(result)
				if len(rows) == 0 || !strings.Contains(rows[0][0].(string), "rows=4 ") {
					t.Fatal("ONLY did not select actual local fact", rows)
				}
			}
			enginetest.SessionSQL(t, s, "ALTER TABLE "+root+" ALTER a SET(n_distinct_inherited=-0.25)")
			if e.Storage.RelationMetadata[ref.Address] != metadata || metadata.Statistics.Columns[key] != positive {
				t.Fatal("configuration eagerly replaced published statistics")
			}
			read(8)
			enginetest.SessionSQL(t, s, "ANALYZE "+root)
			negative := e.Storage.RelationMetadata[ref.Address].Statistics.Columns[key]
			if negative == positive || negative.Rows != 24 || negative.DistinctCount != 24 || negative.DistinctOverride != -.25 || positive.DistinctOverride != 3 {
				t.Fatal("ANALYZE failed actual signed flavor publication", positive, negative)
			}
			// The real inherited population24 gives distinct6=>4. A local fact
			// gives12 on ordinary inheritance; local population8 also gives12,
			// and a heapless partition parent's local population0 would give24.
			read(4)
			values := make([]string, 0, 16)
			for i := 25; i <= 40; i++ {
				if partitioned {
					values = append(values, fmt.Sprintf("(1,%d,%d)", i, i))
				} else {
					values = append(values, fmt.Sprintf("(%d,%d)", i, i))
				}
			}
			destination := "statistics_consumer_child"
			if partitioned {
				destination = root
			}
			enginetest.SessionSQL(t, s, "INSERT INTO "+destination+" VALUES"+strings.Join(values, ","))
			enginetest.SessionSQL(t, s, "ANALYZE "+root+"(b)")
			refreshed := e.Storage.RelationMetadata[ref.Address]
			if refreshed.InheritedTuples != 40 || refreshed.Statistics.Columns[key] != negative || negative.Rows != 24 || negative.DistinctOverride != -.25 {
				t.Fatal("independent ANALYZE did not preserve old inherited fact while refreshing source population")
			}
			// Current40 gives distinct10=>4. Using the fact's captured24 instead
			// would give40/6=>7 and using ordinary local8 would give20.
			read(4)
			if fmt.Sprint(retained) != saved {
				t.Fatal("later publication mutated retained EXPLAIN")
			}
		})
	}
}
