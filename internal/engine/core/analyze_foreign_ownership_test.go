package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"math"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

type foreignAnalyzeOwnerWrapper struct {
	fdwUnitWrapper
	sample               catalog.ForeignSample
	supported            bool
	nilSampler           bool
	fail                 error
	negotiated, acquired catalog.ForeignRequest
	target               int
	hook                 func(context.Context, string) error
}

func (w *foreignAnalyzeOwnerWrapper) AnalyzeForeignTable(ctx context.Context, request catalog.ForeignRequest) (catalog.ForeignAnalyzeInfo, bool, error) {
	if w.hook != nil {
		if err := w.hook(ctx, "AnalyzeForeignTable"); err != nil {
			return catalog.ForeignAnalyzeInfo{}, false, err
		}
	}
	w.negotiated = request
	if w.nilSampler {
		return catalog.ForeignAnalyzeInfo{}, true, nil
	}
	return catalog.ForeignAnalyzeInfo{AcquireSampleRows: func(ctx context.Context, request catalog.ForeignRequest, target int) (catalog.ForeignSample, error) {
		if w.hook != nil {
			if err := w.hook(ctx, "AcquireSampleRows"); err != nil {
				return catalog.ForeignSample{}, err
			}
		}
		w.acquired, w.target = request, target
		return w.sample, w.fail
	}}, w.supported, nil
}
func foreignAnalyzeOwnerFixture(t *testing.T) (*core.Engine, *session.Session, *foreignAnalyzeOwnerWrapper) {
	t.Helper()
	e := engine.New()
	w := &foreignAnalyzeOwnerWrapper{supported: true, sample: catalog.ForeignSample{Rows: []catalog.ForeignRow{{int64(1), int64(10)}, {int64(1), int64(10)}, {int64(2), nil}}, TotalRows: 200, TotalPages: 7.25}}
	definition, err := fdw.ValidateWrapper(fdw.WrapperDefinition{HandlerName: catalog.ForeignFunctionName{Name: "foreign_analyze_owner_handler"}, Wrapper: w})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.InstallForeignDefinitions([]fdw.WrapperDefinition{definition}, nil,
		func(_ context.Context, value driver.Value, usage sqltype.Usage, _ sqltype.InputEnvironment) (sqltype.Value, error) {
			if value == nil {
				return sqltype.Null(usage.Type), nil
			}
			return sqltype.Integer(usage.Type, value.(int64))
		}, func(_ context.Context, value sqltype.Value, _ sqltype.OutputEnvironment) (driver.Value, error) {
			return enginetest.Export(value), nil
		}); err != nil {
		t.Fatal(err)
	}
	s := enginetest.StartSession(t, e, nil)
	for _, query := range []string{"CREATE FOREIGN DATA WRAPPER foreign_analyze_owner_fdw HANDLER foreign_analyze_owner_handler", "CREATE SERVER foreign_analyze_owner_server FOREIGN DATA WRAPPER foreign_analyze_owner_fdw", "CREATE USER MAPPING FOR CURRENT_USER SERVER foreign_analyze_owner_server", "CREATE FOREIGN TABLE foreign_analyze_owner(a integer,b integer) SERVER foreign_analyze_owner_server", "ANALYZE foreign_analyze_owner"} {
		enginetest.SessionSQL(t, s, query)
	}
	return e, s, w
}

func TestAnalyzeForeignJournalOwnership(t *testing.T) {
	for _, completion := range []string{"COMMIT", "ROLLBACK", "COMMIT PREPARED 'foreign_analyze_owner'", "ROLLBACK PREPARED 'foreign_analyze_owner'"} {
		t.Run(completion, func(t *testing.T) {
			e, s, w := foreignAnalyzeOwnerFixture(t)
			peer := enginetest.StartSession(t, e, nil)
			ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "foreign_analyze_owner")
			relation := e.CatalogRead().MustRelation(ref.Address)
			a, b := storage.StatisticsKey{Attribute: relation.Attributes[0]}, storage.StatisticsKey{Attribute: relation.Attributes[1]}
			baseline := e.Storage.RelationMetadata[ref.Address]
			if baseline == nil || baseline.ForeignAnalyze == nil || baseline.ForeignAnalyze.SampleRows != 3 || baseline.ForeignAnalyze.TotalRows != 200 || baseline.ForeignAnalyze.TotalPages != 7.25 || baseline.Tuples != 0 || baseline.Scanned || e.Storage.Relations[ref.Address] != nil {
				t.Fatal("foreign sample created fake heap/population", baseline)
			}
			if w.negotiated.MappingOID != 0 || len(w.negotiated.MappingOptions) != 0 || w.negotiated.Transaction != nil || w.acquired.MappingOID == 0 || w.target != 30000 {
				t.Fatal("capability/mapping/target authority confused", w.negotiated, w.acquired, w.target)
			}
			fact := baseline.Statistics.Columns[a]
			if fact.Rows != 3 || fact.DistinctCount != 2 || fact.MCV[0].Count != 2 {
				t.Fatal("sample counts changed to population", fact)
			}
			rowFrontier, versionFrontier := e.Storage.RowFrontier(), e.Storage.VersionFrontier()
			enginetest.SessionSQL(t, s, "BEGIN")
			enginetest.SessionSQL(t, s, "ANALYZE foreign_analyze_owner(a)")
			first := s.Transactions.Catalog
			selected := first.Statistics[ref.Address]
			if selected.Original != nil || selected.OriginalRelation != relation || selected.Relation != relation || len(selected.Columns) != 1 || selected.Columns[a] == nil || selected.Columns[a] == fact {
				t.Fatal("missing actual selected foreign journal", selected)
			}
			clone := first.Clone()
			if clone.Statistics[ref.Address].Columns[a] != selected.Columns[a] {
				t.Fatal("clone recopied selected immutable backing")
			}
			enginetest.SessionSQL(t, s, "SAVEPOINT kept")
			enginetest.SessionSQL(t, s, "ANALYZE foreign_analyze_owner(b)")
			if len(first.Statistics[ref.Address].Columns) != 1 || len(s.Transactions.Catalog.Statistics[ref.Address].Columns) != 2 {
				t.Fatal("selected merge mutated predecessor")
			}
			enginetest.SessionSQL(t, s, "ROLLBACK TO kept")
			own := session.ResolveStatistics(s.Transactions.Catalog, ref.Address, e.Storage.RelationMetadata[ref.Address], relation, e.CatalogRead())
			if own.Columns[a] != selected.Columns[a] || own.Columns[b] != baseline.Statistics.Columns[b] || analyzeFacts(e, ref.Address) != baseline.Statistics {
				t.Fatal("private facts leaked or unselected backing copied")
			}
			enginetest.SessionSQL(t, peer, "ANALYZE foreign_analyze_owner(b)")
			newB := analyzeFacts(e, ref.Address).Columns[b]
			maintenance := *e.Storage.RelationMetadata[ref.Address]
			if strings.Contains(completion, "PREPARED") {
				enginetest.SessionSQL(t, s, "PREPARE TRANSACTION 'foreign_analyze_owner'")
				entry := core.EnginePreparedRef(e).Lookup("foreign_analyze_owner")
				if entry == nil || entry.Catalog.Statistics[ref.Address].Columns[a] != selected.Columns[a] || s.Transactions.Catalog != nil {
					t.Fatal("prepared owner lost selected backing")
				}
				enginetest.SessionSQL(t, peer, completion)
			} else {
				enginetest.SessionSQL(t, s, completion)
			}
			wantA := fact
			if strings.HasPrefix(completion, "COMMIT") {
				wantA = selected.Columns[a]
			}
			current := *e.Storage.RelationMetadata[ref.Address]
			if current.Statistics.Columns[a] != wantA || current.Statistics.Columns[b] != newB {
				t.Fatal("completion lost selected or newer unrelated backing")
			}
			current.Statistics = maintenance.Statistics
			if current != maintenance || e.Storage.Relations[ref.Address] != nil || e.Storage.RowFrontier() != rowFrontier || e.Storage.VersionFrontier() != versionFrontier {
				t.Fatal("statistics completion changed foreign maintenance/heap identity")
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestAnalyzeForeignRejectedPublication(t *testing.T) {
	e, s, w := foreignAnalyzeOwnerFixture(t)
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "foreign_analyze_owner")
	baseline := e.Storage.RelationMetadata[ref.Address]
	enginetest.SessionSQL(t, s, "ALTER FOREIGN TABLE foreign_analyze_owner ALTER a SET STATISTICS 1")
	enginetest.SessionSQL(t, s, "ALTER FOREIGN TABLE foreign_analyze_owner ALTER b SET STATISTICS 1")
	for _, sample := range []catalog.ForeignSample{
		{TotalRows: math.NaN()}, {TotalRows: 3, TotalDeadRows: -1}, {TotalRows: 3, TotalPages: math.Inf(1)},
		{Rows: []catalog.ForeignRow{{int64(1)}}, TotalRows: 3},
		{Rows: make([]catalog.ForeignRow, 301), TotalRows: 301},
	} {
		w.sample = sample
		op, cleanup, err := enginetest.Operation(e, context.Background())
		if err != nil {
			t.Fatal(err)
		}
		_, err = enginetest.ExecuteFrame(e, op, enginetest.Statement(t, "ANALYZE foreign_analyze_owner"), nil)
		cleanup()
		if err == nil || e.Storage.RelationMetadata[ref.Address] != baseline {
			t.Fatal("invalid sample published partial facts", err)
		}
	}
	w.nilSampler = true
	missingOp, missingCleanup, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = enginetest.ExecuteFrame(e, missingOp, enginetest.Statement(t, "ANALYZE foreign_analyze_owner"), nil)
	missingCleanup()
	if err == nil || e.Storage.RelationMetadata[ref.Address] != baseline {
		t.Fatal("supported nil sampler published facts")
	}
	w.nilSampler = false
	w.fail = errors.New("sample failed")
	op, cleanup, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = enginetest.ExecuteFrame(e, op, enginetest.Statement(t, "ANALYZE foreign_analyze_owner"), nil)
	cleanup()
	if !errors.Is(err, w.fail) || e.Storage.RelationMetadata[ref.Address] != baseline {
		t.Fatal("failed acquisition changed canonical root", err)
	}
	// Unsupported capability negotiates without requiring any mapping.
	enginetest.SessionSQL(t, s, "DROP USER MAPPING FOR CURRENT_USER SERVER foreign_analyze_owner_server")
	w.supported = false
	enginetest.SessionSQL(t, s, "ANALYZE foreign_analyze_owner")
	if e.Storage.RelationMetadata[ref.Address] != baseline || w.negotiated.MappingOID != 0 {
		t.Fatal("unsupported capability required mapping or changed facts")
	}
}
