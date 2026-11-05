package core_test

import (
	"context"
	"database/sql/driver"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// The installed provider returns genuine borrowed records, not weighted copies.
// Tests retain those buffers to distinguish typed import from borrowed backing.
type inheritedForeignSource struct {
	pages       float64
	sample      catalog.ForeignSample
	unsupported bool
	nilSampler  bool
	uncapped    bool
	failure     error
	targets     []int
	requests    []catalog.ForeignRequest
}

type inheritedForeignWrapper struct {
	fdwUnitWrapper
	sources    map[string]*inheritedForeignSource
	negotiated []catalog.ForeignRequest
	hook       func(context.Context, string, string) error
	importHook func(context.Context) error
	active     atomic.Int32
}

func (w *inheritedForeignWrapper) AnalyzeForeignTable(ctx context.Context, request catalog.ForeignRequest) (catalog.ForeignAnalyzeInfo, bool, error) {
	w.negotiated = append(w.negotiated, request)
	if w.hook != nil {
		if err := w.hook(ctx, "negotiate", request.Table); err != nil {
			return catalog.ForeignAnalyzeInfo{}, false, err
		}
	}
	source := w.sources[request.Table]
	if source == nil || source.unsupported {
		return catalog.ForeignAnalyzeInfo{}, false, nil
	}
	info := catalog.ForeignAnalyzeInfo{TotalPages: source.pages}
	if !source.nilSampler {
		info.AcquireSampleRows = func(ctx context.Context, acquired catalog.ForeignRequest, target int) (catalog.ForeignSample, error) {
			w.active.Add(1)
			defer w.active.Add(-1)
			source.targets = append(source.targets, target)
			source.requests = append(source.requests, acquired)
			if w.hook != nil {
				if err := w.hook(ctx, "acquire", acquired.Table); err != nil {
					return catalog.ForeignSample{}, err
				}
			}
			sample := source.sample
			if !source.uncapped && len(sample.Rows) > target {
				sample.Rows = sample.Rows[:target]
			}
			return sample, source.failure
		}
	}
	return info, true, nil
}

func (w *inheritedForeignWrapper) GetForeignPaths(_ context.Context, request catalog.ForeignRequest, _ catalog.ForeignRelSize) ([]catalog.ForeignPath, error) {
	return []catalog.ForeignPath{{TotalCost: 1, Attributes: slices.Clone(request.RequiredAttributes)}}, nil
}

func (w *inheritedForeignWrapper) BeginForeignScan(_ context.Context, request catalog.ForeignRequest, plan catalog.ForeignPlan) (catalog.ForeignScan, error) {
	source := w.sources[request.Table]
	if source == nil {
		return nil, fmt.Errorf("missing genuine foreign query records for %s", request.Table)
	}
	scan := &inheritedForeignScan{rows: source.sample.Rows}
	for _, attribute := range plan.Attributes {
		slot := slices.IndexFunc(request.Columns, func(column catalog.ForeignColumn) bool { return column.Attribute == attribute })
		if slot < 0 {
			return nil, fmt.Errorf("missing foreign query attribute %d", attribute)
		}
		scan.slots = append(scan.slots, slot)
	}
	return scan, nil
}

type inheritedForeignScan struct {
	rows     []catalog.ForeignRow
	slots    []int
	position int
}

func (s *inheritedForeignScan) IterateForeignScan(ctx context.Context) (catalog.ForeignRow, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if s.position == len(s.rows) {
		return nil, false, nil
	}
	row := s.rows[s.position]
	s.position++
	projected := make(catalog.ForeignRow, len(s.slots))
	for i, slot := range s.slots {
		projected[i] = row[slot]
	}
	return projected, true, nil
}

func (s *inheritedForeignScan) ReScanForeignScan(context.Context, catalog.ForeignScanParameters) error {
	s.position = 0
	return nil
}

func (s *inheritedForeignScan) EndForeignScan(context.Context) error {
	s.rows = nil
	return nil
}

type inheritedForeignFixture struct {
	e       *core.Engine
	s       *session.Session
	wrapper *inheritedForeignWrapper
	parent  ids.ObjectRef
	foreign ids.ObjectRef
}

func newInheritedForeignFixture(t *testing.T, nativeRows int, partition bool) inheritedForeignFixture {
	t.Helper()
	e := engine.New()
	wrapper := &inheritedForeignWrapper{sources: make(map[string]*inheritedForeignSource)}
	definition, err := fdw.ValidateWrapper(fdw.WrapperDefinition{HandlerName: catalog.ForeignFunctionName{Name: "mixed_owner_handler"}, Wrapper: wrapper})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.InstallForeignDefinitions(nil, []fdw.WrapperDefinition{definition}, nil,
		func(ctx context.Context, value driver.Value, usage sqltype.Usage, environment sqltype.InputEnvironment) (sqltype.Value, error) {
			if wrapper.importHook != nil {
				if err := wrapper.importHook(ctx); err != nil {
					return sqltype.Value{}, err
				}
			}
			if value == nil {
				null := sqltype.Null(usage.Type.BaseType())
				if err := usage.Type.CheckDomainInput(ctx, null, environment); err != nil {
					return sqltype.Value{}, err
				}
				if usage.Type.IsDomain() {
					return usage.Type.Wrap(null)
				}
				return null, nil
			}
			var text string
			switch value := value.(type) {
			case int64:
				text = strconv.FormatInt(value, 10)
			case string:
				text = value
			default:
				return sqltype.Value{}, fmt.Errorf("mixed provider input: %T", value)
			}
			return usage.Type.InputWithEnvironment(ctx, text, usage.Typmod, environment)
		}, func(_ context.Context, value sqltype.Value, _ sqltype.OutputEnvironment) (driver.Value, error) {
			return enginetest.Export(value), nil
		}); err != nil {
		t.Fatal(err)
	}
	s := enginetest.StartSession(t, e, nil)
	queries := []string{
		"CREATE FOREIGN DATA WRAPPER mixed_owner_fdw HANDLER mixed_owner_handler",
		"CREATE SERVER mixed_owner_server FOREIGN DATA WRAPPER mixed_owner_fdw",
		"CREATE USER MAPPING FOR CURRENT_USER SERVER mixed_owner_server",
	}
	if partition {
		queries = append(queries,
			"CREATE TABLE mixed_parent(a integer,b integer) PARTITION BY RANGE(a)",
			"CREATE TABLE mixed_native PARTITION OF mixed_parent FOR VALUES FROM(0) TO(2)",
			"CREATE FOREIGN TABLE mixed_foreign(b integer,a integer) SERVER mixed_owner_server",
			"ALTER TABLE mixed_parent ATTACH PARTITION mixed_foreign FOR VALUES FROM(2) TO(4)")
	} else {
		queries = append(queries,
			"CREATE TABLE mixed_parent(a integer,b integer)",
			"CREATE FOREIGN TABLE mixed_foreign(b integer,a integer) SERVER mixed_owner_server",
			"ALTER FOREIGN TABLE mixed_foreign INHERIT mixed_parent")
	}
	queries = append(queries,
		"ALTER TABLE mixed_parent ALTER a SET STATISTICS 1",
		"ALTER TABLE mixed_parent ALTER b SET STATISTICS 1")
	for _, query := range queries {
		enginetest.SessionSQL(t, s, query)
	}
	if nativeRows != 0 {
		var query strings.Builder
		query.WriteString("INSERT INTO mixed_parent VALUES")
		for i := range nativeRows {
			if i != 0 {
				query.WriteByte(',')
			}
			query.WriteString("(1,10)")
		}
		enginetest.SessionSQL(t, s, query.String())
	}
	wrapper.sources["mixed_foreign"] = mixedForeignSource(24, 3000, 3000.5)
	read := e.CatalogRead()
	return inheritedForeignFixture{e: e, s: s, wrapper: wrapper,
		parent:  enginetest.CatalogRef(t, e, read, "mixed_parent"),
		foreign: enginetest.CatalogRef(t, e, read, "mixed_foreign")}
}

func mixedForeignSource(pages float64, rows int, population float64) *inheritedForeignSource {
	source := &inheritedForeignSource{pages: pages, sample: catalog.ForeignSample{TotalRows: population, TotalPages: pages}}
	for range rows {
		// Physical child layout is b,a, deliberately unlike the parent.
		source.sample.Rows = append(source.sample.Rows, catalog.ForeignRow{int64(20), int64(2)})
	}
	return source
}

func (f inheritedForeignFixture) analyze(t *testing.T, prepared bool, columns string) {
	t.Helper()
	query := "ANALYZE ONLY mixed_parent" + columns
	if !prepared {
		enginetest.SessionSQL(t, f.s, query)
		return
	}
	statement, description := enginetest.PreparedDescription(t, f.e, f.s, query)
	result, err := enginetest.PreparedExecute(f.e, f.s, statement, &description)
	if result.Rows != nil {
		result.Rows.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
}

func (f inheritedForeignFixture) inherited(t *testing.T, slot int) *storage.ColumnStatistics {
	t.Helper()
	relation := f.e.CatalogRead().MustRelation(f.parent.Address)
	metadata := f.e.Storage.RelationMetadata[f.parent.Address]
	if metadata == nil || metadata.Statistics == nil {
		t.Fatal("missing actual mixed publication")
	}
	fact := metadata.Statistics.Columns[storage.StatisticsKey{Attribute: relation.Attributes[slot], Inherited: true}]
	if fact == nil {
		t.Fatal("missing inherited scalar fact", slot)
	}
	return fact
}

func mixedMCVCount(t *testing.T, fact *storage.ColumnStatistics, value int64) uint64 {
	t.Helper()
	for _, item := range fact.MCV {
		n, err := item.Value.Integer()
		if err != nil {
			t.Fatal(err)
		}
		if n == value {
			return item.Count
		}
	}
	return 0
}

func (f inheritedForeignFixture) execute(t *testing.T, ctx context.Context, query string, prepared bool) error {
	t.Helper()
	_, err := f.executeNotices(t, ctx, query, prepared)
	return err
}

func (f inheritedForeignFixture) executeNotices(t *testing.T, ctx context.Context, query string, prepared bool) ([]sqlstate.Error, error) {
	t.Helper()
	var description *core.Description
	statement := enginetest.Statement(t, query)
	if prepared {
		ast, d := enginetest.PreparedDescription(t, f.e, f.s, query)
		statement, description = ast, &d
	}
	op, err := f.s.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer op.Done()
	var result core.Result
	if description != nil {
		scope, startErr := f.e.StartStatement(op)
		if startErr != nil {
			return op.DetachNotices(), startErr
		}
		scope.Expected = description
		result, err = scope.Execute(statement, 0, nil)
		err = scope.Finish(err)
	} else {
		result, err = f.e.Execute(op, statement, 0, nil)
	}
	if result.Rows != nil {
		result.Rows.Close()
		t.Fatal("ANALYZE returned a rowset")
	}
	if err == nil && result.Count != 0 {
		t.Fatal("ANALYZE reported modified rows", result.Count)
	}
	return op.DetachNotices(), err
}

// Capture both immutable pointers and independent slice/map contents. Rejected
// inherited work may advance completed local maintenance, never typed backing.
func (f inheritedForeignFixture) retained(t *testing.T) func() {
	t.Helper()
	metadata := f.e.Storage.RelationMetadata[f.parent.Address]
	header := *metadata
	facts := metadata.Statistics
	columns := make(map[storage.StatisticsKey]*storage.ColumnStatistics)
	values := make(map[storage.StatisticsKey]storage.ColumnStatistics)
	for key, fact := range facts.Columns {
		columns[key] = fact
		value := *fact
		value.MCV = slices.Clone(fact.MCV)
		value.Histogram = slices.Clone(fact.Histogram)
		values[key] = value
	}
	extended := make(map[storage.ExtendedStatisticsKey]*storage.ExtendedStatistics)
	for key, fact := range facts.Extended {
		extended[key] = fact
	}
	immutableExtended := make(map[storage.ExtendedStatisticsKey]func())
	for key, fact := range extended {
		immutableExtended[key] = retainMixedExtended(t, fact)
	}
	return func() {
		t.Helper()
		if *metadata != header || len(facts.Columns) != len(columns) || len(facts.Extended) != len(extended) {
			t.Fatal("retained metadata header or map mutated")
		}
		for key, fact := range columns {
			if facts.Columns[key] != fact || !reflect.DeepEqual(*fact, values[key]) {
				t.Fatal("retained scalar backing mutated", key)
			}
		}
		for key, fact := range extended {
			if facts.Extended[key] != fact {
				t.Fatal("retained extended backing mutated", key)
			}
			immutableExtended[key]()
		}
	}
}

func retainMixedColumn(fact *storage.ColumnStatistics) storage.ColumnStatistics {
	value := *fact
	value.MCV = slices.Clone(fact.MCV)
	value.Histogram = slices.Clone(fact.Histogram)
	return value
}

func retainMixedExtended(t *testing.T, fact *storage.ExtendedStatistics) func() {
	t.Helper()
	value := *fact
	cloneItems := func(items []sqltype.StatisticsItem) []sqltype.StatisticsItem {
		result := slices.Clone(items)
		for i := range result {
			result[i].Attributes = slices.Clone(items[i].Attributes)
		}
		return result
	}
	value.Items = cloneItems(fact.Items)
	value.DependencyItems = cloneItems(fact.DependencyItems)
	if fact.MCVData != nil {
		mcv := *fact.MCVData
		mcv.Items = slices.Clone(mcv.Items)
		for i := range mcv.Dictionaries {
			mcv.Dictionaries[i] = slices.Clone(mcv.Dictionaries[i])
		}
		value.MCVData = &mcv
	}
	expressions := make([]*storage.ColumnStatistics, fact.ExpressionLen())
	expressionValues := make([]storage.ColumnStatistics, len(expressions))
	for i := range expressions {
		expressions[i] = fact.Expression(i)
		expressionValues[i] = retainMixedColumn(expressions[i])
	}
	return func() {
		t.Helper()
		if !reflect.DeepEqual(*fact, value) || fact.ExpressionLen() != len(expressions) {
			t.Fatal("retained extended construction payload mutated")
		}
		for i, expression := range expressions {
			if fact.Expression(i) != expression || !reflect.DeepEqual(*expression, expressionValues[i]) {
				t.Fatal("retained extended expression backing mutated", i)
			}
		}
	}
}
