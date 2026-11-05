package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func fdwAnalyzeProgressInheritedSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, r *fdwSQLReader, run func(fixture.Step)) {
	t.Helper()
	r.advanced = &fdwAdvancedState{}
	fdwLive(t, p, b, r, run)
	fdwSourceExec(t, p, b, r, run, "CREATE TABLE fdw_source.progress(a integer,b integer,extra bigint)", 0)
	fdwSourceExec(t, p, b, r, run, "CREATE TABLE fdw_source.progress_empty(a integer,b integer,extra bigint)", 0)
	fdwSourceExec(t, p, b, r, run, "INSERT INTO fdw_source.progress VALUES"+strings.TrimSuffix(strings.Repeat("(2,2,917),", 30), ","), 30)
	run(fixture.Exec("CREATE TABLE fdw_progress_parent(a integer,b integer)", 0))
	// The empty physical contributor precedes the actual acquired contributor.
	// Its real zero-work visit is observable at that later acquisition gate.
	run(fixture.Exec("CREATE FOREIGN TABLE fdw_progress_empty(extra bigint,b integer,a integer) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'progress_empty')", 0))
	run(fixture.Exec("ALTER FOREIGN TABLE fdw_progress_empty INHERIT fdw_progress_parent", 0))
	run(fixture.Exec("CREATE FOREIGN TABLE fdw_progress_foreign(extra bigint,b integer,a integer) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'progress')", 0))
	run(fixture.Exec("ALTER FOREIGN TABLE fdw_progress_foreign INHERIT fdw_progress_parent", 0))
	run(fixture.Exec("INSERT INTO fdw_progress_parent VALUES"+strings.TrimSuffix(strings.Repeat("(1,1),", 10), ","), 10))
	run(fixture.Exec("CREATE STATISTICS fdw_progress_joint(ndistinct,dependencies,mcv) ON a,b FROM fdw_progress_parent", 0))
	run(fixture.Exec("CREATE STATISTICS fdw_progress_expression ON (a+1) FROM fdw_progress_parent", 0))
	for _, column := range []string{"a", "b"} {
		run(fixture.Exec("ALTER TABLE fdw_progress_parent ALTER "+column+" SET STATISTICS 2", 0))
	}
	for _, name := range []string{"fdw_progress_joint", "fdw_progress_expression"} {
		run(fixture.Exec("ALTER STATISTICS "+name+" SET STATISTICS 2", 0))
	}
	if !b.PG {
		r.advanced.analyzePages = func(request stuntgres.ForeignRequest) float64 {
			if request.Table == "fdw_progress_empty" {
				return 0
			}
			return 1
		}
	}
}

const fdwAnalyzeProgressInheritedData = " FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid WHERE s.stxname='fdw_progress_joint'"

func fdwAnalyzeProgressInheritedProducts(run func(fixture.Step)) {
	run(fixture.Select("SELECT stxdinherit,stxdndistinct::text AS distincts,stxddependencies::text AS dependencies"+fdwAnalyzeProgressInheritedData+" ORDER BY stxdinherit", []string{"stxdinherit", "distincts", "dependencies"}, []string{"BOOL", "TEXT", "TEXT"}, [][]any{{false, `{"1, 2": 1}`, `{"1 => 2": 1.000000, "2 => 1": 1.000000}`}, {true, `{"1, 2": 2}`, `{"1 => 2": 1.000000, "2 => 1": 1.000000}`}}))
	run(fixture.Select("SELECT m.values::text AS values,m.nulls::text AS nulls,m.frequency FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid CROSS JOIN LATERAL pg_mcv_list_items(d.stxdmcv) m WHERE s.stxname='fdw_progress_joint' AND d.stxdinherit ORDER BY m.values::text", []string{"values", "nulls", "frequency"}, []string{"TEXT", "TEXT", "FLOAT8"}, [][]any{{"{1,1}", "{f,f}", float64(.25)}, {"{2,2}", "{f,f}", float64(.75)}}))
}

func TestReferenceFDWAnalyzeProgressInherited(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fdwRowLockModeName(prepared), func(t *testing.T) {
			fdwAnalyzeProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
				fdwAnalyzeProgressInheritedSetup(t, p, b, r, run)
				id := fdwAnalyzeProgressIdentify(t, p, c, "fdw_progress_parent")
				query := "ANALYZE ONLY fdw_progress_parent"
				step := fixture.Exec(query, 0)
				step.Prepared = prepared
				if b.PG {
					run(step)
				} else {
					gate := fdwAnalyzeProgressHold(t, p, b, c, r, query, "AcquireSampleRows", prepared, nil)
					var child int64
					if err := b.DB.QueryRowContext(p.Ctx, "SELECT 'fdw_progress_foreign'::regclass::oid").Scan(&child); err != nil {
						t.Fatal(err)
					}
					// Local pass built TWO definitions. Entering inherited work
					// retains them; root+zero-work child have completed real visits.
					fdwAnalyzeProgressObserve(t, p, b, id, "acquiring inherited sample rows", 0, 0, 2, 2, 3, 2, child, gate.finished)
					gate.release()
					fdwAnalyzeProgressSuccess(t, gate.join())
				}
				fdwAnalyzeProgressAbsent(t, p, b, id)
				fdwAnalyzeRelationPopulation(run, "fdw_progress_parent", 10)
				fdwAnalyzeRelationPopulation(run, "fdw_progress_foreign", -1)
				fdwAnalyzeProgressInheritedProducts(run)
				rows := fixture.Select("SELECT a,b,count(*) FROM fdw_progress_parent GROUP BY a,b ORDER BY a,b", []string{"a", "b", "count"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(1), int64(10)}, {int64(2), int64(2), int64(30)}})
				rows.Prepared = prepared
				run(rows)
				run(fixture.Select("SELECT extra,b,a,count(*) FROM fdw_progress_foreign GROUP BY extra,b,a ORDER BY extra,b,a", []string{"extra", "b", "a", "count"}, []string{"INT8", "INT4", "INT4", "INT8"}, [][]any{{int64(917), int64(2), int64(2), int64(30)}}))
				if b.PG {
					return
				}
				for _, mode := range []string{"unsupported", "zero-weight", "rounded-zero", "underfill"} {
					r.advanced.mu.Lock()
					r.advanced.analyzeSupported = func(request stuntgres.ForeignRequest) bool {
						return mode != "unsupported" || request.Table != "fdw_progress_empty"
					}
					r.advanced.analyzePages = func(request stuntgres.ForeignRequest) float64 {
						if request.Table != "fdw_progress_empty" {
							return 1
						}
						if mode == "rounded-zero" {
							// Expression demand raises K to 30000; this weight
							// still rounds its genuine allocation quota to zero.
							return 1e-9
						}
						return 0
					}
					r.advanced.limit = 0
					if mode == "underfill" {
						r.advanced.limit = 7
					}
					before := len(r.advanced.acquisitions)
					r.advanced.mu.Unlock()
					gate := fdwAnalyzeProgressHold(t, p, b, c, r, query, "AcquireSampleRows", prepared, nil)
					childCount, childDone := int64(3), int64(2)
					if mode == "unsupported" {
						childCount, childDone = 2, 1
					}
					var child int64
					if err := b.DB.QueryRowContext(p.Ctx, "SELECT 'fdw_progress_foreign'::regclass::oid").Scan(&child); err != nil {
						t.Fatal(err)
					}
					fdwAnalyzeProgressObserve(t, p, b, id, "acquiring inherited sample rows", 0, 0, 2, 2, childCount, childDone, child, gate.finished)
					gate.release()
					fdwAnalyzeProgressSuccess(t, gate.join())
					fdwAnalyzeProgressAbsent(t, p, b, id)
					r.advanced.mu.Lock()
					acquisitions := append([]fdwSampleAcquisition(nil), r.advanced.acquisitions[before:]...)
					r.advanced.mu.Unlock()
					accepted := 30
					if mode == "underfill" {
						accepted = 7
					}
					if len(acquisitions) != 1 || len(acquisitions[0].sample.Rows) != accepted || acquisitions[0].sample.TotalRows != 30 {
						t.Fatal("actual accepted sample versus population", mode, acquisitions)
					}
					request := acquisitions[0].request
					for _, row := range acquisitions[0].sample.Rows {
						if len(row) != len(request.RequiredAttributes) {
							t.Fatal("sample descriptor width", row, request.RequiredAttributes)
						}
						for slot, attribute := range request.RequiredAttributes {
							found := false
							for _, column := range request.Columns {
								if column.Attribute == attribute {
									found = true
									want := int64(2)
									if column.Name == "extra" {
										want = 917
									}
									if row[slot] != want {
										t.Fatal("sample did not use genuine descriptor values", column.Name, row)
									}
								}
							}
							if !found {
								t.Fatal("missing requested sample attribute", attribute)
							}
						}
					}
					if mode != "underfill" {
						fdwAnalyzeProgressInheritedProducts(run)
					} else {
						// Public typed masses describe the 10+7 accepted records,
						// not 10+30 invented representatives. The actual SQL source
						// and its separately returned population still contain40.
						run(fixture.Select("SELECT stxdndistinct::text AS distincts"+fdwAnalyzeProgressInheritedData+" AND d.stxdinherit", []string{"distincts"}, []string{"TEXT"}, [][]any{{`{"1, 2": 2}`}}))
						run(fixture.Select("SELECT m.values::text AS values,m.frequency FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid CROSS JOIN LATERAL pg_mcv_list_items(d.stxdmcv) m WHERE s.stxname='fdw_progress_joint' AND d.stxdinherit ORDER BY m.values::text", []string{"values", "frequency"}, []string{"TEXT", "FLOAT8"}, [][]any{{"{1,1}", float64(10.0 / 17.0)}, {"{2,2}", float64(7.0 / 17.0)}}))
						run(rows)
					}
					fdwAnalyzeProgressResources(t, r)
				}
			})
		})
	}
}
