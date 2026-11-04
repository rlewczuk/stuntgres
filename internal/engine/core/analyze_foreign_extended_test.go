package core_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
)

// Reuse the installed borrowed-record provider and genuine import/SQL machinery.
// The foreign relation's physical order is b,a; no parent rows enter this pass.
func newDirectExtendedFixture(t *testing.T) inheritedForeignFixture {
	t.Helper()
	f := newInheritedForeignFixture(t, 0, false)
	f.parent = f.foreign
	f.wrapper.sources["mixed_foreign"] = &inheritedForeignSource{pages: 1, sample: catalog.ForeignSample{
		Rows:      []catalog.ForeignRow{{int64(10), int64(1)}, {int64(10), int64(1)}, {int64(10), int64(1)}, {int64(20), int64(2)}, {int64(20), int64(2)}, {int64(30), int64(3)}},
		TotalRows: 6, TotalPages: 7.25,
	}}
	for _, q := range []string{
		"ALTER FOREIGN TABLE mixed_foreign ALTER a SET STATISTICS 1",
		"ALTER FOREIGN TABLE mixed_foreign ALTER b SET STATISTICS 1",
		"CREATE STATISTICS direct_joint(ndistinct,dependencies,mcv) ON a,b FROM mixed_foreign",
		"ALTER STATISTICS direct_joint SET STATISTICS 2",
		"CREATE STATISTICS direct_expression ON (a+b) FROM mixed_foreign",
		"ALTER STATISTICS direct_expression SET STATISTICS 1",
	} {
		enginetest.SessionSQL(t, f.s, q)
	}
	return f
}

func directAnalyze(t *testing.T, f inheritedForeignFixture, prepared bool, columns string) {
	t.Helper()
	if err := f.execute(t, context.Background(), "ANALYZE ONLY mixed_foreign"+columns, prepared); err != nil {
		t.Fatal(err)
	}
}

func directJoint(t *testing.T, fact *storage.ExtendedStatistics, rows uint64, groups float64) {
	t.Helper()
	if fact == nil || fact.Hierarchy != nil || len(fact.Items) != 1 || fact.Items[0].Value != groups || len(fact.DependencyItems) != 2 || fact.MCVData == nil || fact.MCVData.Rows != rows {
		t.Fatal("missing actual noninherited joint/dependency/MCV payload", fact)
	}
}

func TestAnalyzeForeignExtendedSample(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			f := newDirectExtendedFixture(t)
			source := f.wrapper.sources["mixed_foreign"]
			source.pages = 0 // Direct acquisition must still run at zero negotiated weight.
			rows, versions := f.e.Storage.RowFrontier(), f.e.Storage.VersionFrontier()
			directAnalyze(t, f, prepared, "")
			metadata := f.e.Storage.RelationMetadata[f.foreign.Address]
			relation := f.e.CatalogRead().MustRelation(f.foreign.Address)
			facts := metadata.Statistics
			joint := facts.Extended[ndistinctKey(t, f.e, "direct_joint")]
			directJoint(t, joint, 6, 3)
			if len(source.targets) != 1 || source.targets[0] != 30000 || len(source.requests) != 1 || source.requests[0].MappingOID == 0 || len(f.wrapper.negotiated) != 1 || f.wrapper.negotiated[0].MappingOID != 0 {
				t.Fatal("scalar and extended work did not share one owner-mapped acquisition", source.targets, source.requests)
			}
			if len(facts.Columns) != 2 || len(facts.Extended) != 2 || metadata.ForeignAnalyze.SampleRows != 6 || metadata.ForeignAnalyze.TotalRows != 6 || metadata.ForeignAnalyze.TotalPages != 7.25 || metadata.AnalyzeCount != 1 || metadata.Scanned || metadata.InheritedScanned || f.e.Storage.Relations[f.foreign.Address] != nil {
				t.Fatal("direct extended publication invented a heap, hierarchy or population", metadata)
			}
			for _, attribute := range relation.Attributes {
				fact := facts.Columns[storage.StatisticsKey{Attribute: attribute}]
				if fact == nil || fact.Rows != 6 || fact.NullCount != 0 || fact.DistinctCount != 3 || len(fact.MCV) != 1 || fact.MCV[0].Count != 3 || len(fact.Histogram) != 2 {
					t.Fatal("scalar payload was derived from population or joint marginal products", fact)
				}
			}
			if joint.DependencyItems[0].Value != 1 || joint.DependencyItems[1].Value != 1 || len(joint.MCVData.Items) != 2 || joint.MCVData.Items[0].Count != 3 || joint.MCVData.Items[1].Count != 2 {
				t.Fatal("joint payload is not measured correlated groups3 versus marginal product9", joint)
			}
			expression := facts.Extended[ndistinctKey(t, f.e, "direct_expression")].Expression(0)
			if expression.Rows != 6 || expression.DistinctCount != 3 || mixedMCVCount(t, expression, 11) != 3 || mixedMCVCount(t, expression, 22) != 2 {
				t.Fatal("expression summary did not consume the same six accepted rows", expression)
			}
			retained := f.retained(t)
			for _, row := range source.sample.Rows {
				row[0], row[1] = int64(99), int64(9)
			}
			retained()
			if f.e.Storage.RowFrontier() != rows || f.e.Storage.VersionFrontier() != versions {
				t.Fatal("direct statistics allocated tuple identities")
			}
			enginetest.LockEmpty(t, f.e)
		})
	}
}

func TestAnalyzeForeignExtendedImportExpressionOnce(t *testing.T) {
	f := newInheritedForeignFixture(t, 0, false)
	for _, q := range []string{
		"CREATE SEQUENCE direct_import_sequence",
		"CREATE SEQUENCE direct_expression_sequence",
		"CREATE DOMAIN direct_import_integer AS integer CHECK(nextval('direct_import_sequence') > 0)",
		"CREATE FOREIGN TABLE direct_once(a direct_import_integer,b integer) SERVER mixed_owner_server",
		"CREATE STATISTICS direct_once_joint(ndistinct,dependencies,mcv) ON a,b FROM direct_once",
		"CREATE STATISTICS direct_once_expression(ndistinct) ON (nextval('direct_expression_sequence')),b FROM direct_once",
	} {
		enginetest.SessionSQL(t, f.s, q)
	}
	f.wrapper.sources["direct_once"] = &inheritedForeignSource{pages: 1, sample: catalog.ForeignSample{Rows: []catalog.ForeignRow{{int64(1), int64(10)}, {int64(1), int64(10)}, {int64(2), int64(20)}, {int64(2), int64(20)}, {int64(3), int64(30)}, {int64(3), int64(30)}}, TotalRows: 6, TotalPages: 1}}
	// Sequence side effects come from genuine domain import and expression
	// evaluation, not instrumented collector calls or counters in a mock.
	enginetest.TransactionRows(t, f.s, "SELECT nextval('direct_import_sequence')", 1)
	enginetest.TransactionRows(t, f.s, "SELECT nextval('direct_expression_sequence')", 1)
	if err := f.execute(t, context.Background(), "ANALYZE direct_once", true); err != nil {
		t.Fatal(err)
	}
	ref := enginetest.CatalogRef(t, f.e, f.e.CatalogRead(), "direct_once")
	facts := enginetest.AnalyzeFacts(f.e, ref.Address)
	directJoint(t, facts.Extended[ndistinctKey(t, f.e, "direct_once_joint")], 6, 3)
	once := facts.Extended[ndistinctKey(t, f.e, "direct_once_expression")]
	if once == nil || once.ExpressionLen() != 1 || len(once.Items) != 1 || once.Items[0].Value != 6 || once.Expression(0).Rows != 6 || once.Expression(0).DistinctCount != 6 {
		t.Fatal("joint and expression collectors did not share one evaluation per row", once)
	}
	hist := once.Expression(0).Histogram
	first, firstErr := hist[0].Integer()
	last, lastErr := hist[len(hist)-1].Integer()
	if firstErr != nil || lastErr != nil || first != 2 || last != 7 || len(f.wrapper.sources["direct_once"].targets) != 1 {
		t.Fatal("expression evaluation replayed or reacquired rows", first, last, firstErr, lastErr)
	}
	enginetest.TransactionRows(t, f.s, "SELECT nextval('direct_import_sequence')", 8)
	enginetest.TransactionRows(t, f.s, "SELECT nextval('direct_expression_sequence')", 8)
	enginetest.LockEmpty(t, f.e)
}

func TestAnalyzeForeignExtendedEligibility(t *testing.T) {
	t.Run("fresh_empty", func(t *testing.T) {
		f := newDirectExtendedFixture(t)
		f.wrapper.sources["mixed_foreign"].sample = catalog.ForeignSample{}
		directAnalyze(t, f, false, "")
		metadata := f.e.Storage.RelationMetadata[f.foreign.Address]
		if metadata == nil || metadata.Statistics != nil || metadata.ForeignAnalyze.SampleRows != 0 || metadata.ForeignAnalyze.TotalRows != 0 || metadata.AnalyzeCount != 1 || f.e.Storage.Relations[f.foreign.Address] != nil {
			t.Fatal("fresh empty direct sample fabricated typed records or heap", metadata)
		}
		enginetest.LockEmpty(t, f.e)
	})
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			f := newDirectExtendedFixture(t)
			directAnalyze(t, f, prepared, "")
			key, exprKey := ndistinctKey(t, f.e, "direct_joint"), ndistinctKey(t, f.e, "direct_expression")
			baseline := enginetest.AnalyzeFacts(f.e, f.foreign.Address)
			retained := f.retained(t)
			source := f.wrapper.sources["mixed_foreign"]
			for _, row := range source.sample.Rows {
				row[0], row[1] = int64(99), int64(9)
			}
			enginetest.SessionSQL(t, f.s, "ALTER FOREIGN TABLE mixed_foreign ALTER a SET STATISTICS 0")
			for _, target := range []string{"2", "0"} {
				enginetest.SessionSQL(t, f.s, "ALTER STATISTICS direct_joint SET STATISTICS "+target)
				source.targets = nil
				notices, err := f.executeNotices(t, context.Background(), "ANALYZE ONLY mixed_foreign", prepared)
				want := []sqlstate.Error{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "42P17", Message: `statistics object "public.direct_joint" could not be computed for relation "public.mixed_foreign"`, Schema: "public", Table: "mixed_foreign"}}
				if err != nil || !reflect.DeepEqual(notices, want) {
					t.Fatal("direct missing-key diagnostic/order changed", notices, err)
				}
				after := enginetest.AnalyzeFacts(f.e, f.foreign.Address)
				if after.Extended[key] != baseline.Extended[key] || after.Extended[exprKey] == baseline.Extended[exprKey] || after.Extended[exprKey].Expression(0).DistinctCount != 1 || mixedMCVCount(t, after.Extended[exprKey].Expression(0), 108) != 6 || len(source.targets) != 1 || source.targets[0] != 30000 {
					t.Fatal("eligible expression did not retain its established target30000", source.targets)
				}
				retained()
			}
			// Disable the expression itself to isolate the skipped joint's budget:
			// admitted scalar b alone needs300, not the joint's600 or expression30000.
			enginetest.SessionSQL(t, f.s, "ALTER STATISTICS direct_expression SET STATISTICS 0")
			expression := enginetest.AnalyzeFacts(f.e, f.foreign.Address).Extended[exprKey]
			source.targets = nil
			directAnalyze(t, f, prepared, "")
			if len(source.targets) != 1 || source.targets[0] != 300 || enginetest.AnalyzeFacts(f.e, f.foreign.Address).Extended[exprKey] != expression || enginetest.AnalyzeFacts(f.e, f.foreign.Address).Extended[key] != baseline.Extended[key] {
				t.Fatal("disabled expression or missing-key joint inflated scalar-only budget", source.targets)
			}
			enginetest.SessionSQL(t, f.s, "ALTER STATISTICS direct_joint SET STATISTICS 2")
			enginetest.SessionSQL(t, f.s, "ALTER FOREIGN TABLE mixed_foreign ALTER b SET STATISTICS 0")
			disabled := enginetest.AnalyzeFacts(f.e, f.foreign.Address)
			source.targets = nil
			notices, err := f.executeNotices(t, context.Background(), "ANALYZE ONLY mixed_foreign", prepared)
			if err != nil || len(notices) != 0 || len(source.targets) != 1 || source.targets[0] != 100 || enginetest.AnalyzeFacts(f.e, f.foreign.Address) != disabled {
				t.Fatal("no admitted scalar did not silently retain all payloads at minimum100", source.targets, notices, err)
			}
			enginetest.SessionSQL(t, f.s, "ALTER FOREIGN TABLE mixed_foreign ALTER a SET STATISTICS 1")
			enginetest.SessionSQL(t, f.s, "ALTER FOREIGN TABLE mixed_foreign ALTER b SET STATISTICS 1")
			source.targets = nil
			directAnalyze(t, f, prepared, "")
			fresh := enginetest.AnalyzeFacts(f.e, f.foreign.Address)
			directJoint(t, fresh.Extended[key], 6, 1)
			if fresh.Extended[key] == baseline.Extended[key] || len(source.targets) != 1 || source.targets[0] != 600 {
				t.Fatal("reenabled joint did not rebuild genuine changed single group", source.targets)
			}
			// Both zero-population and nonzero-population empty acquisitions retain
			// exact selected/unselected backing, but publish actual sample estimates.
			for _, total := range []float64{0, 17.5} {
				before := f.e.Storage.RelationMetadata[f.foreign.Address]
				source.sample = catalog.ForeignSample{TotalRows: total, TotalPages: 9}
				directAnalyze(t, f, prepared, "")
				after := f.e.Storage.RelationMetadata[f.foreign.Address]
				if after.Statistics != fresh || after.ForeignAnalyze.SampleRows != 0 || after.ForeignAnalyze.TotalRows != total || after.ForeignAnalyze.TotalPages != 9 || after.AnalyzeCount != before.AnalyzeCount+1 {
					t.Fatal("empty direct collection replaced backing or fabricated records", after)
				}
			}
			retained()
			enginetest.LockEmpty(t, f.e)
		})
	}
}
