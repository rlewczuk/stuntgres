package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceFDWAnalyzeInheritedExtended(t *testing.T) {
	for _, kind := range []string{"ordinary", "partition"} {
		t.Run(kind, func(t *testing.T) {
			fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
				local, _ := fdwAnalyzeMixedSetup(t, p, b, r, run, kind, 1000, 3000)
				for _, column := range []string{"id", "label", "flag"} {
					run(fixture.Exec("ALTER TABLE fdw_analyze_parent ALTER "+column+" SET STATISTICS 1", 0))
				}
				run(fixture.Exec("CREATE STATISTICS fdw_analyze_joint(ndistinct,dependencies,mcv) ON id,flag FROM fdw_analyze_parent", 0))
				run(fixture.Exec("ALTER STATISTICS fdw_analyze_joint SET STATISTICS 2", 0))
				r.advanced.mu.Lock()
				r.advanced.analyzePages = func(stuntgres.ForeignRequest) float64 { return 24 }
				// Keep rare-group inclusion reproducible while sampling only
				// genuine SQL records; every acquisition owns its generator.
				r.advanced.sampleSeed = &[2]uint64{1, 2}
				r.advanced.mu.Unlock()
				for _, prepared := range []bool{false, true} {
					step := fixture.Exec("ANALYZE ONLY fdw_analyze_parent", 0)
					step.Prepared = prepared
					run(step)
					// Both branches are homogeneous: all genuine accepted samples
					// independently imply two joint groups and perfect dependencies.
					run(fixture.Select("SELECT stxdndistinct::text AS distincts,stxddependencies::text AS dependencies,stxdmcv IS NOT NULL AS mcv FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid WHERE s.stxname='fdw_analyze_joint' AND stxdinherit", []string{"distincts", "dependencies", "mcv"}, []string{"TEXT", "TEXT", "BOOL"}, [][]any{{`{"1, 3": 2}`, `{"1 => 3": 1.000000, "3 => 1": 1.000000}`, true}}))
					if !b.PG {
						fdwAnalyzeLastAllocation(t, r, 450, 450, 3000)
					}
				}
				joint := fdwAnalyzeJointPayloads(t, p, c)
				// Change the actual joint population from two groups to four before
				// disabling a required scalar key. Retention must not look like an
				// equal rebuild of the original homogeneous input.
				// Append-only writes work for native partition leaves too. Native
				// 1024 rows still weigh8, while foreign3072 keeps the 1:3 population
				// ratio and its explicit negotiation weight24.
				run(fixture.Exec("INSERT INTO "+local+" VALUES"+fdwAnalyzeValues(24, 1, "local", true), 24))
				fdwSourceExec(t, p, b, r, run, "INSERT INTO fdw_source.data VALUES"+fdwAnalyzeValues(72, 2, "foreign", false), 72)
				run(fixture.Select("SELECT id,flag,count(*) FROM fdw_analyze_parent GROUP BY id,flag ORDER BY id,flag", []string{"id", "flag", "count"}, []string{"INT4", "BOOL", "INT8"}, [][]any{{int64(1), false, int64(1000)}, {int64(1), true, int64(24)}, {int64(2), false, int64(72)}, {int64(2), true, int64(3000)}}))
				run(fixture.Exec("ALTER TABLE fdw_analyze_parent ALTER id SET STATISTICS 0", 0))
				var notices []fixture.Diagnostic
				_, noticeConn, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
				want := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "42P17", Message: `statistics object "public.fdw_analyze_joint" could not be computed for relation "public.fdw_analyze_parent"`, Schema: "public", Table: "fdw_analyze_parent"}
				warnings := 1
				if kind == "ordinary" {
					warnings = 2 // One for each actual local/inherited collection.
				}
				// The required-key lookup/warning precedes even object target0.
				// Neither ineligible object setting may raise the sample budget.
				for _, target := range []string{"2", "0"} {
					run(fixture.Exec("ALTER STATISTICS fdw_analyze_joint SET STATISTICS "+target, 0))
					for _, prepared := range []bool{false, true} {
						notices = nil
						step := fixture.Exec("ANALYZE ONLY fdw_analyze_parent", 0)
						step.Prepared = prepared
						fixture.RunStep(t, p.Ctx, noticeConn, rec, step)
						if len(notices) != warnings {
							t.Fatalf("ineligible object target%s notices=%+v want %d exact warnings", target, notices, warnings)
						}
						for _, notice := range notices {
							if notice != want {
								t.Fatalf("ineligible object notice=%+v want=%+v", notice, want)
							}
						}
						run(fixture.Select("SELECT stxdndistinct::text AS distincts FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid WHERE s.stxname='fdw_analyze_joint' AND stxdinherit", []string{"distincts"}, []string{"TEXT"}, [][]any{{`{"1, 3": 2}`}}))
						if got := fdwAnalyzeJointPayloads(t, p, c); !reflect.DeepEqual(got, joint) {
							t.Fatal("disabled required key replaced compatible joint payload", target, got, joint)
						}
						if !b.PG {
							// Skipped objects do not raise K300 to K600.
							fdwAnalyzeExtendedAllocation(t, r, 225, false)
						}
					}
				}
				run(fixture.Exec("ALTER STATISTICS fdw_analyze_joint SET STATISTICS 2", 0))
				run(fixture.Exec("ALTER TABLE fdw_analyze_parent ALTER id SET STATISTICS 1", 0))
				run(fixture.Prepared(fixture.Exec("ANALYZE ONLY fdw_analyze_parent", 0)))
				run(fixture.Select("SELECT stxdndistinct::text AS distincts FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid WHERE s.stxname='fdw_analyze_joint' AND stxdinherit", []string{"distincts"}, []string{"TEXT"}, [][]any{{`{"1, 3": 4}`}}))
				if !b.PG {
					fdwAnalyzeExtendedAllocation(t, r, 450, true)
				}
				run(fixture.Exec("DROP STATISTICS fdw_analyze_joint", 0))
				// label is genuinely enabled and selected. Expression input id need
				// not be selected, but at least one scalar must be admitted.
				run(fixture.Exec("CREATE STATISTICS fdw_analyze_expression ON (id+1) FROM fdw_analyze_parent", 0))
				run(fixture.Exec("ANALYZE ONLY fdw_analyze_parent(label)", 0))
				fdwAnalyzeExpressionValues(run, "{3,2}")
				const expressionQuery = "SELECT stxdexpr::text FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid WHERE s.stxname='fdw_analyze_expression' AND stxdinherit"
				var before string
				if err := c.QueryRowContext(p.Ctx, expressionQuery).Scan(&before); err != nil {
					t.Fatal(err)
				}
				for _, column := range []string{"id", "label", "flag"} {
					run(fixture.Exec("ALTER TABLE fdw_analyze_parent ALTER "+column+" SET STATISTICS 0", 0))
				}
				// Mutate only the independent remote source, within its partition
				// bounds: actual expression values become4/2, while old summaries
				// contain3/2. No native partition UPDATE prerequisite is needed.
				fdwSourceExec(t, p, b, r, run, "UPDATE fdw_source.data SET id=3", 3072)
				for _, prepared := range []bool{false, true} {
					notices = nil
					step := fixture.Exec("ANALYZE ONLY fdw_analyze_parent", 0)
					step.Prepared = prepared
					fixture.RunStep(t, p.Ctx, noticeConn, rec, step)
					if len(notices) != 0 {
						t.Fatal("empty scalar admission emitted extended warning", notices)
					}
					var after string
					if err := c.QueryRowContext(p.Ctx, expressionQuery).Scan(&after); err != nil {
						t.Fatal(err)
					}
					if after != before {
						t.Fatal("all-disabled collection replaced retained expression payload")
					}
					fdwAnalyzeExpressionValues(run, "{3,2}")
				}
				run(fixture.Prepared(fixture.Select("SELECT count(*) FROM fdw_analyze_parent WHERE id+1=4", []string{"count"}, []string{"INT8"}, [][]any{{int64(3072)}})))
				run(fixture.Exec("ALTER TABLE fdw_analyze_parent ALTER label SET STATISTICS 1", 0))
				run(fixture.Prepared(fixture.Exec("ANALYZE ONLY fdw_analyze_parent(label)", 0)))
				fdwAnalyzeExpressionValues(run, "{4,2}")
			})
		})
	}
}

func fdwAnalyzeExpressionValues(run func(fixture.Step), values string) {
	run(fixture.Select("SELECT (stxdexpr[1]).stanullfrac,(stxdexpr[1]).stadistinct,(stxdexpr[1]).stavalues1::text AS values,(stxdexpr[1]).stanumbers1::text AS frequencies FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid WHERE s.stxname='fdw_analyze_expression' AND stxdinherit", []string{"stanullfrac", "stadistinct", "values", "frequencies"}, []string{"FLOAT4", "FLOAT4", "TEXT", "TEXT"}, [][]any{{float64(0), float64(2), values, "{0.75,0.25}"}}))
}

func fdwAnalyzeExtendedAllocation(t *testing.T, r *fdwSQLReader, target int, requireGroups bool) {
	t.Helper()
	r.advanced.mu.Lock()
	acquisitions := append([]fdwSampleAcquisition(nil), r.advanced.acquisitions...)
	r.advanced.mu.Unlock()
	if len(acquisitions) == 0 {
		t.Fatal("missing actual extended source acquisition")
	}
	last := acquisitions[len(acquisitions)-1]
	if last.target != target || len(last.sample.Rows) != target || last.sample.TotalRows != 3072 {
		t.Fatalf("allocation=%d sample=%d population=%v want=%d/%d/3072", last.target, len(last.sample.Rows), last.sample.TotalRows, target, target)
	}
	var flags [2]int
	for _, row := range last.sample.Rows {
		if len(row) != 3 || row[0] != int64(2) || row[1] != "foreign" {
			t.Fatal("extended sample fabricated source values", row)
		}
		flag, ok := row[2].(bool)
		if !ok {
			t.Fatal("extended sample lost typed boolean", row)
		}
		index := 0
		if flag {
			index = 1
		}
		flags[index]++
	}
	if requireGroups && (flags[0] == 0 || flags[1] == 0) {
		t.Fatal("extended sample did not witness changed joint groups", flags)
	}
	if state := r.snapshot(); state.openRows != 0 || state.openPools != 0 {
		t.Fatal("extended sample retained source resources", state)
	}
}

type fdwAnalyzeJointPayload struct {
	inherited    bool
	distincts    string
	dependencies string
	mcv          string
}

func fdwAnalyzeJointPayloads(t *testing.T, p *fixture.Pair, c *sql.Conn) []fdwAnalyzeJointPayload {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, "SELECT d.stxdinherit,d.stxdndistinct::text,d.stxddependencies::text,d.stxdmcv::text FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid WHERE s.stxname='fdw_analyze_joint' ORDER BY d.stxdinherit")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []fdwAnalyzeJointPayload
	for rows.Next() {
		var payload fdwAnalyzeJointPayload
		if err := rows.Scan(&payload.inherited, &payload.distincts, &payload.dependencies, &payload.mcv); err != nil {
			t.Fatal(err)
		}
		result = append(result, payload)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(result) == 0 {
		t.Fatal("missing previously collected joint payload")
	}
	return result
}
