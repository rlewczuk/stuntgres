package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqlstate"
)

// Reuse the existing installed-comparison Context seam. The injected error is
// returned by the actual typed operation, not an acquisition/import callback.
type mixedTypedFailureContext struct {
	*amcheckComparisonContext
	failure error
}

func (c *mixedTypedFailureContext) Err() error {
	if err := c.amcheckComparisonContext.Err(); err != nil {
		return err
	}
	if c.entered && c.failure != nil {
		return c.failure
	}
	return nil
}

func TestAnalyzeInheritedForeignTypedBoundary(t *testing.T) {
	for _, partition := range []bool{false, true} {
		for _, boundary := range []string{"local", "inherited"} {
			if partition && boundary == "local" {
				continue
			}
			for _, kernel := range []string{"sqltype.Grouping.Equal", "sqltype.Comparison.Compare"} {
				for _, cancelWork := range []bool{false, true} {
					t.Run(map[bool]string{false: "ordinary/", true: "partition/"}[partition]+boundary+"/"+kernel+map[bool]string{false: "/failure", true: "/cancel"}[cancelWork], func(t *testing.T) {
						f := newInheritedForeignFixture(t, 10, partition)
						f.wrapper.sources["mixed_foreign"] = mixedForeignSource(1, 30, 30)
						enginetest.SessionSQL(t, f.s, "CREATE STATISTICS mixed_typed(ndistinct,dependencies,mcv) ON a,b FROM mixed_parent")
						enginetest.SessionSQL(t, f.s, "CREATE STATISTICS mixed_typed_expression ON (a+b) FROM mixed_parent")
						f.analyze(t, false, "")
						before := f.e.Storage.RelationMetadata[f.parent.Address]
						retained := f.retained(t)
						// Distinct residual b values force genuine ordering; repeated
						// existing a/b values also force genuine grouping equality.
						enginetest.SessionSQL(t, f.s, "INSERT INTO mixed_parent VALUES(1,0),(1,1),(1,2),(1,3),(1,4),(1,5),(1,6),(1,7),(1,8),(1,9)")
						ctx, cancel := context.WithCancel(context.Background())
						defer cancel()
						op, err := f.s.Acquire(ctx)
						if err != nil {
							t.Fatal(err)
						}
						comparison := &amcheckComparisonContext{Context: op.Ctx, match: kernel}
						if boundary == "inherited" {
							comparison.match = "unreachable_before_inherited_negotiation"
							f.wrapper.hook = func(_ context.Context, phase, _ string) error {
								if phase == "negotiate" {
									comparison.match = kernel
								}
								return nil
							}
						}
						failure := errors.New("actual mixed typed kernel failure")
						active := &mixedTypedFailureContext{amcheckComparisonContext: comparison}
						if cancelWork {
							comparison.cancel = cancel
						} else {
							active.failure = failure
						}
						op.Ctx = active
						_, err = f.e.Execute(op, enginetest.Statement(t, "ANALYZE ONLY mixed_parent"), 0, nil)
						op.Done()
						want := failure
						if cancelWork {
							want = context.Canceled
						}
						if !comparison.entered || !errors.Is(err, want) {
							t.Fatal("owning typed operation boundary missed or original error changed", comparison.entered, err)
						}
						after := f.e.Storage.RelationMetadata[f.parent.Address]
						if after.Statistics != before.Statistics || after.InheritedTuples != 40 {
							t.Fatal("typed kernel failure changed old scalar/extended/inherited backing")
						}
						if partition || boundary == "local" {
							if after != before {
								t.Fatal("unfinished pass published maintenance")
							}
						} else if after.AnalyzeCount != before.AnalyzeCount+1 || !after.LastAnalyze.After(before.LastAnalyze) || f.e.Storage.Relations[f.parent.Address].Maintenance.Tuples != 20 {
							t.Fatal("inherited kernel failure retracted completed local20")
						}
						if f.wrapper.active.Load() != 0 {
							t.Fatal("typed failure leaked sampler resource")
						}
						retained()
						enginetest.LockEmpty(t, f.e)
					})
				}
			}
		}
	}
}

func TestAnalyzeInheritedForeignExpressionFailure(t *testing.T) {
	for _, partition := range []bool{false, true} {
		for _, local := range []bool{false, true} {
			if partition && local {
				continue
			}
			for _, prepared := range []bool{false, true} {
				t.Run(map[bool]string{false: "ordinary/", true: "partition/"}[partition]+map[bool]string{false: "inherited", true: "local"}[local]+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					f := newInheritedForeignFixture(t, 10, partition)
					source := mixedForeignSource(1, 30, 30)
					f.wrapper.sources["mixed_foreign"] = source
					enginetest.SessionSQL(t, f.s, "CREATE STATISTICS mixed_expression_failure ON (100/b) FROM mixed_parent")
					f.analyze(t, false, "")
					before := f.e.Storage.RelationMetadata[f.parent.Address]
					retained := f.retained(t)
					if local {
						enginetest.SessionSQL(t, f.s, "UPDATE ONLY mixed_parent SET b=0")
					} else {
						for _, row := range source.sample.Rows {
							row[0] = int64(0)
						}
					}
					err := f.execute(t, context.Background(), "ANALYZE ONLY mixed_parent", prepared)
					var diagnostic *sqlstate.Error
					if !errors.As(err, &diagnostic) || diagnostic.Code != "22012" || diagnostic.Message != "division by zero" {
						t.Fatal("actual expression failure suppressed or changed", err)
					}
					after := f.e.Storage.RelationMetadata[f.parent.Address]
					if after.Statistics != before.Statistics || after.InheritedTuples != 40 {
						t.Fatal("failed expression replaced typed or inherited facts")
					}
					if partition || local {
						if after != before {
							t.Fatal("uncompleted expression pass published success")
						}
					} else if after.AnalyzeCount != before.AnalyzeCount+1 || !after.LastAnalyze.After(before.LastAnalyze) {
						t.Fatal("inherited expression failure retracted local completion")
					}
					retained()
					enginetest.LockEmpty(t, f.e)
				})
			}
		}
	}
}
