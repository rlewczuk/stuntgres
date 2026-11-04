package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceFDWAnalyzeInheritedAuthority(t *testing.T) {
	for _, kind := range []string{"ordinary", "partition", "foreign"} {
		t.Run(kind, func(t *testing.T) {
			fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
				fdwAnalyzeMixedSetup(t, p, b, r, run, kind, 10, 30)
				run(fixture.Exec("ANALYZE ONLY fdw_analyze_parent", 0))
				old := fdwExplainText(t, p, c, "EXPLAIN SELECT * FROM fdw_analyze_parent WHERE label='foreign'")
				saved := strings.Join(old, "\n")
				// Real target/column admission failure is common SQL on both backends.
				run(fixture.Prepared(fixture.ErrorDiagnostic("ANALYZE ONLY fdw_analyze_parent(label,label)", fdwDiagnostic("42701", `column "label" of relation "fdw_analyze_parent" appears more than once`))))
				fdwAnalyzeBaselineFacts(t, p, b, c, run)
				if b.PG {
					return
				}
				// Provider gates/Reset/Restore are explicit native controls, not an
				// invented physical postgres_fdw sampling/failure equivalence.
				for _, phase := range []string{"AnalyzeForeignTable", "AcquireSampleRows", "AcquireSampleRowsResult"} {
					for _, action := range []string{"cancel", "provider", "mapping"} {
						before := fdwAnalyzePassState(t, p, b, c, "fdw_analyze_parent", -1)
						fdwGatedInformation(t, p, b, c, r, phase, action, nil, "ANALYZE ONLY fdw_analyze_parent")
						increment := int64(1)
						// A foreign root encounters the first foreign callback in
						// its local pass; these particular gates fail before completion.
						if kind == "partition" || kind == "foreign" {
							increment = 0
						}
						fdwAnalyzePassState(t, p, b, c, "fdw_analyze_parent", before.count+increment)
						fdwAnalyzeBaselineFacts(t, p, b, c, run)
					}
				}
				// Reject actual corrupt transport and provider contract results. The
				// root's local pass may finish, but neither typed flavor is installed.
				for _, failure := range []string{"width", "type", "population", "pages"} {
					r.advanced.mu.Lock()
					r.advanced.sampleTransform = func(_ stuntgres.ForeignRequest, sample stuntgres.ForeignSample) (stuntgres.ForeignSample, error) {
						switch failure {
						case "width":
							sample.Rows[0] = sample.Rows[0][:1]
						case "type":
							sample.Rows[0][0] = "not an integer"
						case "population":
							sample.TotalRows = math.NaN()
						case "pages":
							sample.TotalPages = math.Inf(1)
						}
						return sample, nil
					}
					r.advanced.mu.Unlock()
					_, err := c.ExecContext(p.Ctx, "ANALYZE ONLY fdw_analyze_parent")
					r.advanced.mu.Lock()
					r.advanced.sampleTransform = nil
					r.advanced.mu.Unlock()
					if err == nil {
						t.Fatal("invalid source sample published", failure)
					}
					if failure == "type" {
						fixture.AssertDiagnostic(t, err, new(fdwDiagnostic("22P02", `invalid input syntax for type integer: "not an integer"`)))
					}
					fdwAnalyzeBaselineFacts(t, p, b, c, run)
				}
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				before := r.snapshot()
				_, err := c.ExecContext(ctx, "ANALYZE ONLY fdw_analyze_parent")
				if !errors.Is(err, context.Canceled) || r.snapshot() != before {
					t.Fatal("pre-cancel entered provider", err)
				}
				// Actual native replacement while a callback is detached must reject
				// its old authority, and replacement itself must not negotiate/sample.
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				fdwGatedInformation(t, p, b, c, r, "AcquireSampleRowsResult", "restore", dump.Bytes(), "ANALYZE ONLY fdw_analyze_parent")
				fdwAnalyzeRelationPopulation(run, "fdw_analyze_parent", -1)
				run(fixture.Exec("ANALYZE ONLY fdw_analyze_parent", 0))
				fdwAnalyzeBaselineFacts(t, p, b, c, run)
				if strings.Join(old, "\n") != saved {
					t.Fatal("held source plan text mutated after rejection/replacement")
				}
			})
		})
	}
}
