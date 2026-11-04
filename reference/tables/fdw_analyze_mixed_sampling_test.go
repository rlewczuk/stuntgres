package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceFDWAnalyzeMixedSampling(t *testing.T) {
	for _, kind := range []string{"ordinary", "partition"} {
		t.Run(kind, func(t *testing.T) {
			fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
				_, foreign := fdwAnalyzeMixedSetup(t, p, b, r, run, kind, 1000, 3000)
				for _, column := range []string{"id", "label", "flag"} {
					run(fixture.Exec("ALTER TABLE fdw_analyze_parent ALTER "+column+" SET STATISTICS 1", 0))
				}
				// This is provider metadata, not an attempted physical PG page match.
				r.advanced.mu.Lock()
				r.advanced.analyzePages = func(stuntgres.ForeignRequest) float64 { return 24 }
				r.advanced.mu.Unlock()
				for _, prepared := range []bool{false, true} {
					step := fixture.Exec("ANALYZE ONLY fdw_analyze_parent", 0)
					step.Prepared = prepared
					run(step)
					population := float64(1000)
					if kind == "partition" {
						population = 4000
					}
					fdwAnalyzeRelationPopulation(run, "fdw_analyze_parent", population)
					fdwAnalyzeRelationPopulation(run, foreign, -1)
					if b.PG {
						// PostgreSQL chooses its own physical allocation/random subset.
						// Two homogeneous populations necessarily have two values, no NULLs;
						// do not pin its sample count, plan shape, or rounded frequency.
						run(fixture.Select("SELECT n_distinct,null_frac FROM pg_stats WHERE schemaname='public' AND tablename='fdw_analyze_parent' AND inherited AND attname='id'", []string{"n_distinct", "null_frac"}, []string{"FLOAT4", "FLOAT4"}, [][]any{{float64(2), float64(0)}}))
					} else {
						fdwAnalyzeLastAllocation(t, r, 225, 225, 3000)
						fdwAnalyzeHierarchyEstimate(t, p, c, "id=1", 1000)
						fdwAnalyzeHierarchyEstimate(t, p, c, "id=2", 3000)
					}
					run(fixture.Prepared(fixture.Select("SELECT count(*) FROM fdw_analyze_parent WHERE id=1", []string{"count"}, []string{"INT8"}, [][]any{{int64(1000)}})))
					run(fixture.Select("SELECT count(*) FROM fdw_analyze_parent WHERE id=2", []string{"count"}, []string{"INT8"}, [][]any{{int64(3000)}}))
				}
				if b.PG {
					return
				}
				// Explicit native-only provider controls: actual source reads continue;
				// underfill is never padded and population is not the sample size.
				r.advanced.mu.Lock()
				r.advanced.limit = 20
				r.advanced.mu.Unlock()
				run(fixture.Exec("ANALYZE ONLY fdw_analyze_parent", 0))
				fdwAnalyzeLastAllocation(t, r, 225, 20, 3000)
				fdwAnalyzeHierarchyEstimate(t, p, c, "id=2", 843)
				fdwAnalyzeHierarchyEstimate(t, p, c, "id=1", 3158)
				r.advanced.mu.Lock()
				r.advanced.limit = 0
				r.advanced.sampleTransform = func(_ stuntgres.ForeignRequest, sample stuntgres.ForeignSample) (stuntgres.ForeignSample, error) {
					sample.TotalRows = 3000.5
					return sample, nil
				}
				r.advanced.mu.Unlock()
				run(fixture.Prepared(fixture.Exec("ANALYZE ONLY fdw_analyze_parent", 0)))
				fdwAnalyzeLastAllocation(t, r, 225, 225, 3000.5)
				if kind == "partition" {
					fdwAnalyzeRelationPopulation(run, "fdw_analyze_parent", 4000.5)
				}
				fdwAnalyzeHierarchyEstimate(t, p, c, "id=2", 3001)
				// Zero and rounded-zero weights are completed visits, not permission
				// to acquire with a fake minimum-one or consult a missing mapping.
				for _, pages := range []float64{0, 0.001} {
					r.advanced.mu.Lock()
					r.advanced.sampleTransform = nil
					r.advanced.analyzePages = func(stuntgres.ForeignRequest) float64 { return pages }
					before := len(r.advanced.acquisitions)
					r.advanced.mu.Unlock()
					run(fixture.Exec("DROP USER MAPPING FOR CURRENT_USER SERVER fdw_live", 0))
					run(fixture.Exec("ANALYZE ONLY fdw_analyze_parent", 0))
					r.advanced.mu.Lock()
					after := len(r.advanced.acquisitions)
					r.advanced.mu.Unlock()
					if after != before {
						t.Fatal("zero-work visit acquired foreign records", pages)
					}
					fdwAnalyzeHierarchyEstimate(t, p, c, "id=1", 1000)
					run(fixture.Exec("CREATE USER MAPPING FOR CURRENT_USER SERVER fdw_live", 0))
				}
				// Direct FDW analysis is deliberately not an inherited zero-work visit.
				r.advanced.mu.Lock()
				r.advanced.analyzePages = func(stuntgres.ForeignRequest) float64 { return 0 }
				before := len(r.advanced.acquisitions)
				r.advanced.mu.Unlock()
				run(fixture.Exec("ANALYZE "+foreign, 0))
				fdwAnalyzeRelationPopulation(run, foreign, 3000)
				r.advanced.mu.Lock()
				after := len(r.advanced.acquisitions)
				r.advanced.mu.Unlock()
				if after != before+1 {
					t.Fatal("direct zero-weight capability failed to acquire")
				}
			})
		})
	}
}

func fdwAnalyzeLastAllocation(t *testing.T, r *fdwSQLReader, target, rows int, population float64) {
	t.Helper()
	r.advanced.mu.Lock()
	acquisitions := append([]fdwSampleAcquisition(nil), r.advanced.acquisitions...)
	r.advanced.mu.Unlock()
	if len(acquisitions) == 0 {
		t.Fatal("missing actual source acquisition")
	}
	last := acquisitions[len(acquisitions)-1]
	if last.target != target || len(last.sample.Rows) != rows || last.sample.TotalRows != population {
		t.Fatalf("allocation=%d sample=%d population=%v, want %d/%d/%v", last.target, len(last.sample.Rows), last.sample.TotalRows, target, rows, population)
	}
	for _, row := range last.sample.Rows {
		if len(row) != 3 || row[0] != int64(2) || row[1] != "foreign" || row[2] != true {
			t.Fatal("accepted record not from genuine SQL source", row)
		}
	}
	state := r.snapshot()
	if state.openRows != 0 || state.openPools != 0 {
		t.Fatal("sample resources retained", state)
	}
}
