package core_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

// Keep independent header/count/value snapshots as well as exact immutable
// payload pointers. Equal reltuples alone cannot establish failed publication.
func captureForeignPublication(t *testing.T, e *core.Engine, s *session.Session, address ids.ObjectAddress) func(bool) {
	t.Helper()
	e.Mu.Lock()
	metadata := e.Storage.RelationMetadata[address]
	var saved storage.RelationMetadata
	var population storage.ForeignAnalyzeStatistics
	facts := make(map[storage.StatisticsKey]*storage.ColumnStatistics)
	values := make(map[storage.StatisticsKey]storage.ColumnStatistics)
	if metadata != nil {
		saved = *metadata
		if metadata.ForeignAnalyze != nil {
			population = *metadata.ForeignAnalyze
		}
		if metadata.Statistics != nil {
			for key, fact := range metadata.Statistics.Columns {
				facts[key] = fact
				copy := *fact
				copy.MCV = slices.Clone(fact.MCV)
				copy.Histogram = slices.Clone(fact.Histogram)
				values[key] = copy
			}
		}
	}
	rows, versions := e.Storage.RowFrontier(), e.Storage.VersionFrontier()
	heap := e.Storage.Relations[address]
	e.Mu.Unlock()
	return func(current bool) {
		t.Helper()
		e.Mu.Lock()
		defer e.Mu.Unlock()
		if current {
			if e.Storage.RelationMetadata[address] != metadata || e.Storage.Relations[address] != heap || e.Storage.RowFrontier() != rows || e.Storage.VersionFrontier() != versions {
				t.Fatal("failed detached work changed canonical root or heap/frontiers")
			}
			s.Mu.Lock()
			journal := s.Transactions.Catalog
			hasDelta := journal != nil && len(journal.Statistics) != 0
			s.Mu.Unlock()
			if hasDelta {
				t.Fatal("failed detached work retained a statistics journal delta")
			}
		}
		if metadata == nil {
			return
		}
		if *metadata != saved || metadata.ForeignAnalyze != nil && *metadata.ForeignAnalyze != population {
			t.Fatal("failed detached work mutated retained population/count/time/header")
		}
		if saved.Statistics != nil {
			if len(saved.Statistics.Columns) != len(facts) {
				t.Fatal("retained statistics map mutated")
			}
			for key, fact := range facts {
				if saved.Statistics.Columns[key] != fact || !reflect.DeepEqual(*fact, values[key]) {
					t.Fatal("selected/unselected typed backing or literal counts mutated", key)
				}
			}
		}
	}
}

// Reuse the installed-comparison context seam, not an importer/provider gate.
// An injected error here is raised by the actual typed operation's Err check.
type foreignTypedFailureContext struct {
	*amcheckComparisonContext
	failure error
}

func (c *foreignTypedFailureContext) Err() error {
	if err := c.amcheckComparisonContext.Err(); err != nil {
		return err
	}
	if c.entered && c.failure != nil {
		return c.failure
	}
	return nil
}

type foreignDetachedOutcome struct {
	err  error
	rows bool
}

func TestAnalyzeForeignDetachedPublication(t *testing.T) {
	for _, phase := range []string{"AnalyzeForeignTable", "AcquireSampleRows", "comparison", "selectivity"} {
		for _, action := range []string{"failure", "cancel", "mapping", "restore"} {
			t.Run(phase+"/"+action, func(t *testing.T) {
				e, s, wrapper := foreignAnalyzeOwnerFixture(t)
				peer := enginetest.StartSession(t, e, nil)
				ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "foreign_analyze_owner")
				old := captureForeignPublication(t, e, s, ref.Address)
				// Valid repeated/singleton input reaches the real collection
				// comparison kernels; EXPLAIN separately reaches MCV equality.
				wrapper.sample = catalog.ForeignSample{Rows: []catalog.ForeignRow{{int64(1), int64(10)}, {int64(1), int64(20)}, {int64(2), int64(30)}, {int64(3), int64(40)}}, TotalRows: 40, TotalPages: 2}
				wrapper.paths = []catalog.ForeignPath{{TotalCost: 3, Attributes: []int32{1, 2}}}
				var dump strings.Builder
				if action == "restore" {
					if err := engine.Dump(t.Context(), e, &dump); err != nil {
						t.Fatal(err)
					}
				}
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				entered, release := make(chan struct{}), make(chan struct{})
				var releaseOnce sync.Once
				unblock := func() { releaseOnce.Do(func() { close(release) }) }
				failure := errors.New("detached typed/provider work failed")
				gate := func(active context.Context) error {
					close(entered)
					select {
					case <-release:
					case <-active.Done():
						return active.Err()
					}
					if action == "failure" {
						return failure
					}
					return active.Err()
				}
				op, err := s.Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				query := "ANALYZE foreign_analyze_owner"
				var comparison *amcheckComparisonContext
				if phase == "comparison" || phase == "selectivity" {
					match := "sqltype.Comparison.Compare"
					if phase == "selectivity" {
						match, query = "sqltype.Grouping.Equal", "EXPLAIN SELECT * FROM foreign_analyze_owner WHERE a=1"
					}
					comparison = &amcheckComparisonContext{Context: op.Ctx, match: match}
					comparison.work = func() { _ = gate(comparison.Context) }
					active := &foreignTypedFailureContext{amcheckComparisonContext: comparison}
					if action == "failure" {
						active.failure = failure
					}
					op.Ctx = active
				} else {
					wrapper.hook = func(active context.Context, name string) error {
						if name == phase {
							return gate(active)
						}
						return active.Err()
					}
				}
				statement := enginetest.Statement(t, query)
				done := make(chan foreignDetachedOutcome, 1)
				go func() {
					result, err := e.Execute(op, statement, 0, nil)
					hasRows := result.Rows != nil
					if result.Rows != nil {
						result.Rows.Close()
					}
					op.Done()
					done <- foreignDetachedOutcome{err: err, rows: hasRows}
				}()
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
				case outcome := <-done:
					joined = true
					t.Fatal("intended detached phase not reached", outcome)
				case <-ctx.Done():
					t.Fatal("detached phase admission", ctx.Err())
				}
				// A peer completes while actual comparison or provider work is gated.
				peerDone := make(chan error, 1)
				peerStatement := enginetest.Statement(t, "SELECT 42")
				go func() {
					peerOp, err := peer.Acquire(ctx)
					if err == nil {
						result, executeErr := e.Execute(peerOp, peerStatement, 0, nil)
						if result.Rows != nil {
							result.Rows.Close()
						}
						peerOp.Done()
						err = executeErr
					}
					peerDone <- err
				}()
				peerJoined := false
				defer func() {
					if !peerJoined {
						cancel()
						unblock()
						<-peerDone
					}
				}()
				select {
				case err := <-peerDone:
					peerJoined = true
					if err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal("peer blocked under detached work", ctx.Err())
				}
				var admitted func(bool)
				switch action {
				case "cancel":
					cancel()
				case "mapping":
					enginetest.SessionSQL(t, peer, "ALTER USER MAPPING FOR CURRENT_USER SERVER foreign_analyze_owner_server OPTIONS(ADD user 'postgres')")
					unblock()
				case "restore":
					if err := engine.Restore(ctx, e, strings.NewReader(dump.String())); err != nil {
						t.Fatal(err)
					}
					admitted = captureForeignPublication(t, e, s, ref.Address)
					unblock()
				default:
					unblock()
				}
				var outcome foreignDetachedOutcome
				select {
				case outcome = <-done:
					joined = true
				case <-ctx.Done():
					if action != "cancel" {
						t.Fatal("detached worker completion", ctx.Err())
					}
					outcome = <-done
					joined = true
				}
				if outcome.rows {
					t.Fatal("failed detached work published partial TEXT rows")
				}
				switch action {
				case "failure":
					if !errors.Is(outcome.err, failure) {
						t.Fatal(outcome.err)
					}
				case "cancel":
					if !errors.Is(outcome.err, context.Canceled) {
						t.Fatal(outcome.err)
					}
				default:
					if !errors.Is(outcome.err, ids.ErrCatalogReplaced) {
						t.Fatal(outcome.err)
					}
				}
				if comparison != nil && !comparison.entered {
					t.Fatal("actual typed comparison not entered")
				}
				old(action != "restore")
				if admitted != nil {
					admitted(true)
				}
			})
		}
	}
}
