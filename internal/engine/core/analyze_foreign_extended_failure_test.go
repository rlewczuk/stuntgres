package core_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
)

func TestAnalyzeForeignExtendedScalarBoundary(t *testing.T) {
	f := newDirectExtendedFixture(t)
	directAnalyze(t, f, false, "")
	before := f.e.Storage.RelationMetadata[f.foreign.Address]
	retained := f.retained(t)
	for _, q := range []string{"CREATE SEQUENCE direct_scalar_boundary", "CREATE STATISTICS direct_scalar_once ON (nextval('direct_scalar_boundary')) FROM mixed_foreign"} {
		enginetest.SessionSQL(t, f.s, q)
	}
	enginetest.TransactionRows(t, f.s, "SELECT nextval('direct_scalar_boundary')", 1)
	for i, row := range f.wrapper.sources["mixed_foreign"].sample.Rows {
		row[0], row[1] = int64(i+10), int64(i+1)
	}
	op, err := f.s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	comparison := &amcheckComparisonContext{Context: op.Ctx, match: "sqltype.Comparison.Compare"}
	failure := errors.New("actual direct scalar ordering failure")
	op.Ctx = &mixedTypedFailureContext{amcheckComparisonContext: comparison, failure: failure}
	_, err = f.e.Execute(op, enginetest.Statement(t, "ANALYZE ONLY mixed_foreign"), 0, nil)
	op.Done()
	if !comparison.entered || !errors.Is(err, failure) || f.e.Storage.RelationMetadata[f.foreign.Address] != before {
		t.Fatal("actual scalar Finish failed to precede extended evaluation/publication", comparison.entered, err)
	}
	// Scalar ordering must finish before any expression can consume its sequence.
	enginetest.TransactionRows(t, f.s, "SELECT nextval('direct_scalar_boundary')", 2)
	retained()
	enginetest.LockEmpty(t, f.e)
}

func TestAnalyzeForeignExtendedInstallCancellation(t *testing.T) {
	for _, phase := range []string{"acceptExtended", "publishLocal"} {
		t.Run(phase, func(t *testing.T) {
			f := newDirectExtendedFixture(t)
			for _, q := range []string{"DROP STATISTICS direct_joint", "DROP STATISTICS direct_expression", "CREATE SEQUENCE direct_gate_sequence", "CREATE STATISTICS direct_gate(ndistinct,dependencies,mcv) ON (nextval('direct_gate_sequence')),b FROM mixed_foreign"} {
				enginetest.SessionSQL(t, f.s, q)
			}
			directAnalyze(t, f, false, "")
			before := f.e.Storage.RelationMetadata[f.foreign.Address]
			retained := f.retained(t)
			enginetest.TransactionRows(t, f.s, "SELECT nextval('direct_gate_sequence')", 7)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, err := f.s.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			checkpoint := &mixedPassInstallContext{Context: op.Ctx, cancel: cancel, owner: phase}
			op.Ctx = checkpoint
			_, err = f.e.Execute(op, enginetest.Statement(t, "ANALYZE ONLY mixed_foreign"), 0, nil)
			op.Done()
			if !checkpoint.fired || !errors.Is(err, context.Canceled) || f.e.Storage.RelationMetadata[f.foreign.Address] != before {
				t.Fatal("real definition-acceptance/final-publication gate failed", phase, checkpoint.fired, err)
			}
			// Both checkpoints follow six genuine expression evaluations. At
			// publishLocal the completed definition has already been accepted into
			// the command-local delta, but neither checkpoint may publish it.
			enginetest.TransactionRows(t, f.s, "SELECT nextval('direct_gate_sequence')", 14)
			retained()
			enginetest.LockEmpty(t, f.e)
		})
	}
}

func TestAnalyzeForeignExtendedExpressionFailure(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, phase := range []string{"expression", "import", "acquire"} {
			t.Run(map[bool]string{false: "direct/", true: "prepared/"}[prepared]+phase, func(t *testing.T) {
				f := newDirectExtendedFixture(t)
				enginetest.SessionSQL(t, f.s, "CREATE STATISTICS direct_division ON (100/b) FROM mixed_foreign")
				directAnalyze(t, f, false, "")
				before := f.e.Storage.RelationMetadata[f.foreign.Address]
				retained := f.retained(t)
				source := f.wrapper.sources["mixed_foreign"]
				sentinel := errors.New("genuine direct acquisition failure")
				switch phase {
				case "expression":
					source.sample.Rows[len(source.sample.Rows)-1][0] = int64(0)
				case "import":
					source.sample.Rows[len(source.sample.Rows)-1][0] = "not_an_integer"
				case "acquire":
					source.failure = sentinel
				}
				err := f.execute(t, context.Background(), "ANALYZE ONLY mixed_foreign", prepared)
				if phase == "acquire" {
					if !errors.Is(err, sentinel) {
						t.Fatal("ordinary provider error identity changed", err)
					}
				} else {
					var diagnostic *sqlstate.Error
					code := map[string]string{"expression": "22012", "import": "22P02"}[phase]
					if !errors.As(err, &diagnostic) || diagnostic.Code != code || phase == "expression" && diagnostic.Message != "division by zero" {
						t.Fatal("real expression/import error changed", err)
					}
				}
				if f.e.Storage.RelationMetadata[f.foreign.Address] != before {
					t.Fatal("failed direct work published scalar/extended/maintenance products")
				}
				retained()
				enginetest.LockEmpty(t, f.e)
			})
		}
	}
}

func TestAnalyzeForeignExtendedDetachedAuthority(t *testing.T) {
	for _, phase := range []string{"negotiate", "acquire", "import"} {
		for _, action := range []string{"cancel", "failure", "mapping", "replace"} {
			t.Run(phase+"/"+action, func(t *testing.T) {
				f := newDirectExtendedFixture(t)
				directAnalyze(t, f, false, "")
				before := f.e.Storage.RelationMetadata[f.foreign.Address]
				retained := f.retained(t)
				peer := f
				peer.s = enginetest.StartSession(t, f.e, nil)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				entered, release := make(chan struct{}), make(chan struct{})
				var unblockOnce, gateOnce sync.Once
				unblock := func() { unblockOnce.Do(func() { close(release) }) }
				failure := errors.New("genuine detached direct callback failure")
				gate := func(active context.Context) error {
					var err error
					gateOnce.Do(func() {
						close(entered)
						select {
						case <-release:
						case <-active.Done():
						}
						if action == "failure" {
							err = failure
						} else {
							err = active.Err()
						}
					})
					return err
				}
				if phase == "import" {
					f.wrapper.importHook = gate
				} else {
					f.wrapper.hook = func(active context.Context, at, _ string) error {
						if at == phase {
							return gate(active)
						}
						return active.Err()
					}
				}
				done := make(chan error, 1)
				go func() { done <- f.execute(t, ctx, "ANALYZE ONLY mixed_foreign", false) }()
				joined := false
				defer func() {
					cancel()
					unblock()
					if !joined {
						<-done
					}
				}()
				select {
				case <-entered:
				case err := <-done:
					joined = true
					t.Fatal("genuine detached phase not reached", err)
				case <-ctx.Done():
					t.Fatal("detached phase blocked", ctx.Err())
				}
				result, err := enginetest.ExecuteAsync(t, f.e, peer.s, ctx, "SELECT 42")()
				if result.Rows != nil {
					result.Rows.Close()
				}
				if err != nil {
					t.Fatal("callback retained engine/replacement ownership", err)
				}
				var installed *storage.RelationMetadata
				switch action {
				case "cancel":
					cancel()
				case "mapping":
					if err := peer.execute(t, ctx, "ALTER USER MAPPING FOR CURRENT_USER SERVER mixed_owner_server OPTIONS(ADD user 'changed')", false); err != nil {
						t.Fatal(err)
					}
				case "replace":
					if _, err := f.e.ReplaceCatalog(ctx, f.e, f.e.CatalogRead()); err != nil {
						t.Fatal("detached callback retained replacement reservation", err)
					}
					installed = f.e.Storage.RelationMetadata[f.foreign.Address]
				}
				unblock()
				err = <-done
				joined = true
				if err == nil || action == "failure" && !errors.Is(err, failure) || action == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatal("detached failure/cancellation/authority identity changed", err)
				}
				if action == "mapping" || action == "replace" {
					if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
						t.Fatal("authority fence was replaced by timeout/cancellation", err)
					}
				}
				want := before
				if action == "replace" {
					want = installed
				}
				if f.e.Storage.RelationMetadata[f.foreign.Address] != want || f.e.Storage.Relations[f.foreign.Address] != nil {
					t.Fatal("rejected detached direct work changed canonical owner")
				}
				retained()
				enginetest.LockEmpty(t, f.e)
			})
		}
	}
}

func TestAnalyzeForeignExtendedNativeReplacement(t *testing.T) {
	source, destination := newDirectExtendedFixture(t), newDirectExtendedFixture(t)
	directAnalyze(t, source, false, "")
	directAnalyze(t, destination, false, "")
	before := destination.e.Storage.RelationMetadata[destination.foreign.Address]
	retained := source.retained(t)
	destinationRetained := destination.retained(t)
	original := enginetest.AnalyzeFacts(source.e, source.foreign.Address)
	for _, row := range source.wrapper.sources["mixed_foreign"].sample.Rows {
		row[0], row[1] = int64(99), int64(9)
	}
	for _, f := range []inheritedForeignFixture{source, destination} {
		f.wrapper.hook = func(context.Context, string, string) error {
			t.Fatal("native replacement reacquired provider data")
			return nil
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := destination.e.ReplaceCatalog(ctx, source.e, source.e.CatalogRead()); !errors.Is(err, context.Canceled) || destination.e.Storage.RelationMetadata[destination.foreign.Address] != before {
		t.Fatal("cancelled native replacement lost exact canonical backing", err)
	}
	if err := engine.Restore(context.Background(), destination.e, strings.NewReader("CREATE TABLE broken(")); err == nil || destination.e.Storage.RelationMetadata[destination.foreign.Address] != before {
		t.Fatal("failed native restore replaced previous typed backing", err)
	}
	destinationRetained()
	if _, err := destination.e.ReplaceCatalog(context.Background(), source.e, source.e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	ref := enginetest.CatalogRef(t, destination.e, destination.e.CatalogRead(), "mixed_foreign")
	read := destination.e.CatalogRead()
	metadata := destination.e.Storage.RelationMetadata[ref.Address]
	if metadata == nil || metadata == before || metadata.AnalyzeCount != 0 || metadata.InheritedScanned || destination.e.Storage.Relations[ref.Address] != nil {
		t.Fatal("native typed replacement fabricated ANALYZE success or a heap", metadata)
	}
	for key, old := range original.Columns {
		fact := metadata.Statistics.Columns[key]
		relation := read.MustRelation(ref.Address)
		if fact == nil || fact == old || fact.Rows != 6 || fact.NullCount != 0 || fact.DistinctCount != 3 || len(fact.MCV) != 1 || fact.MCV[0].Count != 3 || len(fact.Histogram) != 2 || &fact.MCV[0] == &old.MCV[0] || fact.Usage != relation.Columns[relation.IndexForAttribute(key.Attribute)].Usage {
			t.Fatal("native replacement lost measured scalar facts or borrowed source backing", key)
		}
	}
	for _, name := range []string{"direct_joint", "direct_expression"} {
		oldKey, key := ndistinctKey(t, source.e, name), ndistinctKey(t, destination.e, name)
		old, fact := original.Extended[oldKey], metadata.Statistics.Extended[key]
		definition, ok := read.Statistics(key.Definition.Address)
		if !ok || key.Definition.Domain() == oldKey.Definition.Domain() || fact == nil || fact == old || fact.Relation != definition.Relation || fact.Recipes != definition.Expressions || fact.Selection != definition.Selection || fact.Hierarchy != nil || fact.ExpressionLen() != old.ExpressionLen() {
			t.Fatal("native replacement failed real generation/definition/typed ownership", name)
		}
		if name == "direct_joint" {
			directJoint(t, fact, 6, 3)
			if fact.MCVData == old.MCVData || !reflect.DeepEqual(fact.MCVData.Items, old.MCVData.Items) || &fact.Items[0] == &old.Items[0] {
				t.Fatal("native replacement borrowed source joint backing")
			}
		} else if fact.Expression(0) == old.Expression(0) || fact.Expression(0).Rows != 6 || fact.Expression(0).DistinctCount != 3 || mixedMCVCount(t, fact.Expression(0), 11) != 3 || mixedMCVCount(t, fact.Expression(0), 22) != 2 {
			t.Fatal("native replacement recomputed changed provider values or borrowed expression backing")
		}
	}
	if storage.CompatibleExtendedStatistics(metadata.Statistics, read.MustRelation(ref.Address), read) != metadata.Statistics {
		t.Fatal("reminted direct definition/provider authority rejected")
	}
	retained()
	destinationRetained()
	if err := engine.Reset(context.Background(), destination.e); err != nil {
		t.Fatal(err)
	}
	if destination.e.Storage.RelationMetadata[ref.Address] != nil {
		t.Fatal("reset retained obsolete persistent direct statistics")
	}
	enginetest.LockEmpty(t, source.e)
	enginetest.LockEmpty(t, destination.e)
}
