package core_test

import (
	"context"
	"database/sql/driver"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/storage"
)

func TestAnalyzeInheritedForeignMidpointExpression(t *testing.T) {
	f := newInheritedForeignFixture(t, 0, false)
	for _, query := range []string{
		"INSERT INTO mixed_parent VALUES(0,0),(1,1),(2,2),(3,3),(4,4),(5,5),(6,6),(7,7),(8,8),(9,9)",
		"CREATE STATISTICS mixed_midpoint ON (a+0) FROM mixed_parent",
	} {
		enginetest.SessionSQL(t, f.s, query)
	}
	// Expression summaries require K30000 independently of scalar target1.
	// Weights1/9999 give native quota3: genuine midpoint ranks1,5,8.
	f.wrapper.sources["mixed_foreign"] = mixedForeignSource(9999, 300, 3000)
	f.analyze(t, true, "(b)")
	key := ndistinctKey(t, f.e, "mixed_midpoint")
	key.Inherited = true
	fact := f.e.Storage.RelationMetadata[f.parent.Address].Statistics.Extended[key]
	source := f.wrapper.sources["mixed_foreign"]
	if len(source.targets) != 1 || source.targets[0] != 29997 || fact == nil || fact.ExpressionLen() != 1 || fact.Expression(0).Rows != 303 || fact.Expression(0).DistinctCount != 4 || len(fact.Expression(0).Histogram) != 3 || mixedMCVCount(t, fact.Expression(0), 2) != 300 {
		t.Fatal("expression-only selection did not use shared genuine midpoint sample", source.targets, fact)
	}
	for i, want := range []int64{1, 5, 8} {
		got, err := fact.Expression(0).Histogram[i].Integer()
		if err != nil || got != want {
			t.Fatal("native sample used repeated/prefix/non-midpoint rows", i, got, err)
		}
	}
}

func TestAnalyzeInheritedForeignRoundedCapacity(t *testing.T) {
	f := newInheritedForeignFixture(t, 0, false)
	// Seven equal positive visits round300/7 to43. First underfill20 must
	// leave actual capacity for six more43-row acquisitions (278 total),
	// rather than consuming its quota and capping the last visit at42.
	f.wrapper.sources["mixed_foreign"] = mixedForeignSource(1, 20, 20)
	for i := 1; i < 7; i++ {
		name := fmt.Sprintf("mixed_equal_%d", i)
		enginetest.SessionSQL(t, f.s, "CREATE FOREIGN TABLE "+name+"(a integer,b integer) SERVER mixed_owner_server")
		enginetest.SessionSQL(t, f.s, "ALTER FOREIGN TABLE "+name+" INHERIT mixed_parent")
		source := &inheritedForeignSource{pages: 1, sample: catalog.ForeignSample{TotalRows: 43, TotalPages: 1}}
		for range 43 {
			source.sample.Rows = append(source.sample.Rows, catalog.ForeignRow{int64(2), int64(20)})
		}
		f.wrapper.sources[name] = source
	}
	f.analyze(t, false, "")
	for name, source := range f.wrapper.sources {
		if len(source.targets) != 1 || source.targets[0] != 43 {
			t.Fatal("actual underfill did not free capacity", name, source.targets)
		}
	}
	if fact := f.inherited(t, 0); fact.Rows != 278 || mixedMCVCount(t, fact, 2) != 278 || f.e.Storage.RelationMetadata[f.parent.Address].InheritedTuples != 278 {
		t.Fatal("rounded visits padded or silently dropped genuine rows", fact)
	}
}

func TestAnalyzeInheritedForeignStructuralAuthority(t *testing.T) {
	f := newInheritedForeignFixture(t, 10, false)
	f.wrapper.sources["mixed_foreign"].unsupported = true
	// Traverse unsupported foreign ancestors and deduplicate an actual diamond.
	peer := enginetest.StartSession(t, f.e, nil)
	for _, query := range []string{"CREATE TEMP TABLE mixed_peer() INHERITS(mixed_parent)", "INSERT INTO mixed_peer VALUES(4,40),(4,40)"} {
		enginetest.SessionSQL(t, peer, query)
	}
	for _, query := range []string{"CREATE TEMP TABLE mixed_own() INHERITS(mixed_parent)", "INSERT INTO mixed_own VALUES(5,50),(5,50)"} {
		enginetest.SessionSQL(t, f.s, query)
	}
	for _, query := range []string{
		"CREATE TABLE mixed_other(a integer,b integer) INHERITS(mixed_parent)",
		"CREATE TABLE mixed_diamond(a integer,b integer) INHERITS(mixed_foreign,mixed_other)",
		"INSERT INTO mixed_diamond VALUES(3,30),(3,30),(3,30)",
		"CREATE STATISTICS mixed_structure(ndistinct) ON a,b FROM mixed_parent",
	} {
		enginetest.SessionSQL(t, f.s, query)
	}
	f.analyze(t, false, "")
	metadata := f.e.Storage.RelationMetadata[f.parent.Address]
	fact := f.inherited(t, 0)
	if fact.Rows != 15 || metadata.InheritedTuples != 15 || len(f.wrapper.sources["mixed_foreign"].targets) != 0 {
		t.Fatal("unsupported node pruned descendants, sampled native closure, duplicated diamond or included peer TEMP", fact, metadata)
	}
	key := ndistinctKey(t, f.e, "mixed_structure")
	key.Inherited = true
	extended := metadata.Statistics.Extended[key]
	if extended == nil || len(extended.Hierarchy.Relations) != 5 || len(fact.Hierarchy.Relations) != 5 {
		t.Fatal("structural authority replaced by retained contributor set")
	}
	calls := len(f.wrapper.negotiated)
	f.wrapper.sources["mixed_foreign"].unsupported = false
	f.wrapper.hook = func(context.Context, string, string) error {
		t.Fatal("stale structural compatibility attempted capability discovery")
		return nil
	}
	root := f.e.CatalogRead().MustRelation(f.parent.Address)
	if !storage.StatisticsHierarchyCurrent(fact.Hierarchy, root, f.e.CatalogRead()) || storage.CompatibleExtendedStatistics(metadata.Statistics, root, f.e.CatalogRead()).Extended[key] != extended {
		t.Fatal("capability change invalidated structurally compatible retained fact")
	}
	enginetest.SessionSQL(t, f.s, "EXPLAIN SELECT * FROM mixed_parent WHERE a=3")
	if len(f.wrapper.negotiated) != calls {
		t.Fatal("consumer negotiated provider")
	}
	retained := f.retained(t)
	enginetest.SessionSQL(t, f.s, "ALTER TABLE mixed_diamond NO INHERIT mixed_foreign")
	enginetest.SessionSQL(t, f.s, "ALTER TABLE mixed_diamond NO INHERIT mixed_other")
	if storage.StatisticsHierarchyCurrent(fact.Hierarchy, f.e.CatalogRead().MustRelation(f.parent.Address), f.e.CatalogRead()) {
		t.Fatal("removed structural member accepted obsolete hierarchy authority")
	}
	retained()
}

func TestAnalyzeInheritedForeignOwnerMapping(t *testing.T) {
	f := newInheritedForeignFixture(t, 10, false)
	for _, query := range []string{
		"CREATE ROLE mixed_child_owner",
		"GRANT USAGE ON FOREIGN SERVER mixed_owner_server TO mixed_child_owner",
		"CREATE USER MAPPING FOR PUBLIC SERVER mixed_owner_server OPTIONS(user 'public_source')",
		"CREATE USER MAPPING FOR mixed_child_owner SERVER mixed_owner_server OPTIONS(user 'child_source')",
		"ALTER FOREIGN TABLE mixed_foreign OWNER TO mixed_child_owner",
	} {
		enginetest.SessionSQL(t, f.s, query)
	}
	f.wrapper.sources["mixed_foreign"] = mixedForeignSource(1, 30, 30)
	for _, exact := range []bool{true, false} {
		if !exact {
			enginetest.SessionSQL(t, f.s, "DROP USER MAPPING FOR mixed_child_owner SERVER mixed_owner_server")
		}
		f.analyze(t, false, "")
		source := f.wrapper.sources["mixed_foreign"]
		request := source.requests[len(source.requests)-1]
		want := "child_source"
		if !exact {
			want = "public_source"
		}
		if request.Role != "mixed_child_owner" || request.RoleOID == 0 || request.MappingOID == 0 || len(request.MappingOptions) != 1 || request.MappingOptions[0].Value != want {
			t.Fatal("parent/actor mapping substituted for child owner exact/PUBLIC mapping", request)
		}
	}
}

func TestAnalyzeInheritedForeignLayoutAndResults(t *testing.T) {
	f := newInheritedForeignFixture(t, 2, false)
	enginetest.SessionSQL(t, f.s, "DROP FOREIGN TABLE mixed_foreign")
	for _, query := range []string{
		"CREATE FOREIGN TABLE mixed_foreign(extra integer,b integer,a integer) SERVER mixed_owner_server",
		"ALTER FOREIGN TABLE mixed_foreign INHERIT mixed_parent",
	} {
		enginetest.SessionSQL(t, f.s, query)
	}
	source := &inheritedForeignSource{pages: 1, sample: catalog.ForeignSample{Rows: []catalog.ForeignRow{{int64(999), int64(20), int64(2)}, {int64(998), int64(20), int64(2)}, {int64(997), nil, nil}}, TotalRows: 3, TotalPages: 1}}
	f.wrapper.sources["mixed_foreign"] = source
	query := "SELECT a,b FROM mixed_parent ORDER BY a,b"
	before := enginetest.SessionSQL(t, f.s, query)
	want := [][]driver.Value{{int64(1), int64(10)}, {int64(1), int64(10)}, {int64(2), int64(20)}, {int64(2), int64(20)}, {nil, nil}}
	if got := enginetest.Tuples(before); !reflect.DeepEqual(got, want) {
		t.Fatal("genuine foreign query layout baseline differs", got)
	}
	for _, prepared := range []bool{false, true} {
		f.analyze(t, prepared, "")
		fact, b := f.inherited(t, 0), f.inherited(t, 1)
		if fact.Rows != 5 || fact.NullCount != 1 || b.NullCount != 1 || fact.DistinctCount != 2 || mixedMCVCount(t, fact, 1) != 2 || mixedMCVCount(t, b, 10) != 2 {
			t.Fatal("extra/reordered attributes or NULL sample lost", fact, b)
		}
		ast, description := enginetest.PreparedDescription(t, f.e, f.s, query)
		after, err := enginetest.PreparedExecute(f.e, f.s, ast, &description)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(enginetest.Tuples(after), want) || !reflect.DeepEqual(enginetest.Tuples(before), want) {
			t.Fatal("ANALYZE changed actual rows or retained eager result")
		}
		after.Rows.Close()
	}
	oldA := f.inherited(t, 0)
	retained := f.retained(t)
	enginetest.SessionSQL(t, f.s, "UPDATE ONLY mixed_parent SET b=NULL")
	for _, row := range source.sample.Rows {
		row[1] = nil
	}
	changed := enginetest.SessionSQL(t, f.s, query)
	f.analyze(t, true, "(b)")
	allNull := f.inherited(t, 1)
	if allNull.Rows != 5 || allNull.NullCount != 5 || allNull.DistinctCount != 0 || len(allNull.MCV) != 0 || len(allNull.Histogram) != 0 || f.inherited(t, 0) != oldA {
		t.Fatal("positive all-NULL mixed sample did not replace only selected b", allNull)
	}
	refreshed := enginetest.SessionSQL(t, f.s, query)
	if !reflect.DeepEqual(enginetest.Tuples(changed), enginetest.Tuples(refreshed)) || !reflect.DeepEqual(enginetest.Tuples(before), want) {
		t.Fatal("all-NULL refresh changed results or held previous output")
	}
	retained()
	enginetest.LockEmpty(t, f.e)
}

func TestAnalyzeInheritedForeignRoundEven(t *testing.T) {
	for _, test := range []struct {
		name     string
		pages    float64
		disabled bool
		quota    int
		sample   uint64
		total    float64
	}{
		{"half_to_zero", 599, false, 300, 300, 3000},
		{"one_half_to_two", 199, false, 298, 300, 3128},
		{"minimum_target_half_to_zero", 199, true, 100, 0, 3000},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newInheritedForeignFixture(t, 128, false)
			source := mixedForeignSource(test.pages, 3000, 3000)
			f.wrapper.sources["mixed_foreign"] = source
			if test.disabled {
				enginetest.SessionSQL(t, f.s, "ALTER TABLE mixed_parent ALTER a SET STATISTICS 0")
				enginetest.SessionSQL(t, f.s, "ALTER TABLE mixed_parent ALTER b SET STATISTICS 0")
			}
			f.analyze(t, false, "")
			if len(source.targets) != 1 || source.targets[0] != test.quota || f.e.Storage.RelationMetadata[f.parent.Address].InheritedTuples != test.total {
				t.Fatal("released round-to-even allocation changed", source.targets)
			}
			if !test.disabled {
				fact := f.inherited(t, 0)
				if fact.Rows != test.sample || mixedMCVCount(t, fact, 2) != uint64(test.quota) {
					t.Fatal("native half tie used ceil/floor instead of even rounding", fact)
				}
			}
		})
	}
}

func TestAnalyzeInheritedForeignExpressionOnce(t *testing.T) {
	f := newInheritedForeignFixture(t, 10, false)
	f.wrapper.sources["mixed_foreign"] = mixedForeignSource(1, 30, 30)
	for _, query := range []string{
		"CREATE SEQUENCE mixed_expression_sequence",
		"CREATE STATISTICS mixed_once(ndistinct) ON (nextval('mixed_expression_sequence')),b FROM mixed_parent",
	} {
		enginetest.SessionSQL(t, f.s, query)
	}
	f.analyze(t, true, "(b)")
	key := ndistinctKey(t, f.e, "mixed_once")
	key.Inherited = true
	fact := f.e.Storage.RelationMetadata[f.parent.Address].Statistics.Extended[key]
	if fact == nil || fact.ExpressionLen() != 1 || fact.Expression(0).Rows != 40 || fact.Expression(0).DistinctCount != 40 || len(fact.Items) != 1 || fact.Items[0].Value != 40 {
		t.Fatal("joint and expression collection did not share accepted rows", fact)
	}
	histogram := fact.Expression(0).Histogram
	first, firstErr := histogram[0].Integer()
	last, lastErr := histogram[len(histogram)-1].Integer()
	if firstErr != nil || lastErr != nil || first != 11 || last != 50 {
		t.Fatal("expression evaluated outside its accepted local/inherited pass", first, last)
	}
	enginetest.TransactionRows(t, f.s, "SELECT nextval('mixed_expression_sequence')", 51)
	if len(f.wrapper.sources["mixed_foreign"].targets) != 1 {
		t.Fatal("extended collection reacquired foreign sample")
	}
}
