package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceFDWAnalyzeInheritedExpressionSnapshot(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fdwRowLockModeName(prepared), func(t *testing.T) {
			fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
				r.advanced = &fdwAdvancedState{limit: 30}
				fdwLive(t, p, b, r, run)
				fdwSourceExec(t, p, b, r, run, "DELETE FROM fdw_source.data", 3)
				values := make([]string, 990)
				for i := range values {
					values[i] = fmt.Sprintf("(%d,'foreign',true)", i+2)
				}
				fdwSourceExec(t, p, b, r, run, "INSERT INTO fdw_source.data VALUES"+strings.Join(values, ","), 990)
				run(fixture.Exec("CREATE TABLE fdw_analyze_parent(id integer,label text,flag boolean)", 0))
				run(fixture.Exec("CREATE FOREIGN TABLE fdw_analyze_foreign() INHERITS(fdw_analyze_parent) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'data')", 0))
				run(fixture.Exec("INSERT INTO fdw_analyze_parent VALUES"+fdwAnalyzeValues(10, 1, "local", false), 10))
				run(fixture.Exec("CREATE STATISTICS fdw_analyze_snapshot_expression ON (id+0) FROM fdw_analyze_parent", 0))
				analyze := fixture.Exec("ANALYZE ONLY fdw_analyze_parent", 0)
				analyze.Prepared = prepared
				run(analyze)
				fdwAnalyzeRelationPopulation(run, "fdw_analyze_parent", 10)
				fdwAnalyzeRelationPopulation(run, "fdw_analyze_foreign", -1)
				run(fixture.Select("SELECT count(*),min(id),max(id) FROM fdw_analyze_parent", []string{"count", "min", "max"}, []string{"INT8", "INT4", "INT4"}, [][]any{{int64(1000), int64(1), int64(991)}}))
				rows := append(fdwAnalyzeRepeatedRows(10, 1, "local", false), []any{int64(2), "foreign", true}, []any{int64(500), "foreign", true}, []any{int64(991), "foreign", true})
				queryRows := fixture.Select("SELECT id,label,flag FROM fdw_analyze_parent WHERE id IN(1,2,500,991) ORDER BY id", []string{"id", "label", "flag"}, []string{"INT4", "TEXT", "BOOL"}, rows)
				queryRows.Prepared = prepared
				run(queryRows)
				if b.PG {
					// PostgreSQL's target100 exceeds this complete1000-row fixture:
					// D991/N1000, MCV1 has10/1000, unlike the native controlled sample.
					fdwAnalyzeSnapshotExpressionFacts(run, "0.991", "{0.01}")
					return
				}
				// Native-only sampling/roundtrip control. All thirty remote records
				// are genuine distinct source rows. Together with ten native1s:
				// n40,d31,f1=30,N1000 =>round(40*31/(10+30*40/1000))=111.
				// Signed distinct is-.111, not the raw measured31/40=-.775.
				histogram := fdwAnalyzeSnapshotAcceptedHistogram(t, r)
				fdwAnalyzeSnapshotExpressionFacts(run, "0.111", "{0.25}")
				fdwAnalyzeSnapshotHistogram(run, histogram)
				const retainedQuery = "SELECT (stxdexpr[1]).stavalues2::text" + fdwAnalyzeSnapshotExpressionFrom
				held, err := c.QueryContext(p.Ctx, retainedQuery)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				// Make retained facts deliberately stale before dumping: imported
				// local data has11 rows and the real external source now has one
				// repeated value, not the old thirty accepted singleton values.
				run(fixture.Exec("INSERT INTO fdw_analyze_parent VALUES(1,'local',false)", 1))
				fdwSourceExec(t, p, b, r, run, "UPDATE fdw_source.data SET id=9999", 990)
				r.advanced.mu.Lock()
				negotiations, acquisitions := len(r.advanced.negotiations), len(r.advanced.acquisitions)
				r.advanced.mu.Unlock()
				before := r.snapshot()
				var saved bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
					t.Fatal(err)
				}
				if saved.Len() == 0 {
					t.Fatal("empty sampled expression dump")
				}
				// Restore into a genuinely different registered driver/provider.
				// Public semantic import must reown the sample, not retain source
				// callbacks or reconstruct estimates by sampling the changed data.
				destinationReader := &fdwSQLReader{driver: r.driver, source: r.source, dsn: r.dsn, advanced: &fdwAdvancedState{}}
				driver := stuntgres.NewDriver()
				if err := destinationReader.configure(driver); err != nil {
					t.Fatal(err)
				}
				connector, err := driver.OpenConnector("fdw-expression-snapshot/" + t.Name())
				if err != nil {
					t.Fatal(err)
				}
				destination := sql.OpenDB(connector)
				defer destination.Close()
				recorded, recorder := fixture.RecordedNativePool(t, connector)
				defer recorded.Close()
				if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(saved.Bytes())); err != nil {
					t.Fatal(err)
				}
				check := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, recorded, recorder, step) }
				fdwAnalyzeSnapshotExpressionFacts(check, "0.111", "{0.25}")
				fdwAnalyzeSnapshotHistogram(check, histogram)
				check(fixture.Select("SELECT (stxdexpr[1]).stadistinct,(stxdexpr[1]).stavalues1::text AS values,(stxdexpr[1]).stanumbers1::text AS frequencies FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid WHERE s.stxname='fdw_analyze_snapshot_expression' AND NOT d.stxdinherit", []string{"stadistinct", "values", "frequencies"}, []string{"FLOAT4", "TEXT", "TEXT"}, [][]any{{float64(1), "{1}", "{1}"}}))
				// Typed snapshot transport is not an ANALYZE completion: canonical
				// inherited/local population, scan/count/time are not serialized.
				check(fixture.Select("SELECT relname,reltuples FROM pg_class WHERE relname IN('fdw_analyze_parent','fdw_analyze_foreign') ORDER BY relname", []string{"relname", "reltuples"}, []string{"NAME", "FLOAT4"}, [][]any{{"fdw_analyze_foreign", float64(-1)}, {"fdw_analyze_parent", float64(-1)}}))
				check(fixture.Select("SELECT analyze_count,last_analyze FROM pg_stat_all_tables WHERE relid='fdw_analyze_parent'::regclass", []string{"analyze_count", "last_analyze"}, []string{"INT8", "TIMESTAMPTZ"}, [][]any{{int64(0), nil}}))
				if !held.Next() {
					t.Fatal("retained source expression row disappeared", held.Err())
				}
				var oldHistogram string
				if err := held.Scan(&oldHistogram); err != nil || oldHistogram != histogram {
					t.Fatal("old expression result changed during destination restore", oldHistogram, err)
				}
				if held.Next() || held.Err() != nil {
					t.Fatal("unexpected retained expression rows", held.Err())
				}
				held.Close()
				r.advanced.mu.Lock()
				if len(r.advanced.negotiations) != negotiations || len(r.advanced.acquisitions) != acquisitions {
					r.advanced.mu.Unlock()
					t.Fatal("Dump/Restore reacquired source ANALYZE sample")
				}
				r.advanced.mu.Unlock()
				if r.snapshot() != before || len(destinationReader.advanced.negotiations) != 0 || len(destinationReader.advanced.acquisitions) != 0 || destinationReader.snapshot().queries != 0 {
					t.Fatal("typed snapshot import/reader invoked source or destination provider work")
				}
				// Actual queries use the newly registered destination reader and
				// current source rows, while the restored statistics stay stale.
				check(fixture.Prepared(fixture.Select("SELECT count(*) FROM ONLY fdw_analyze_parent", []string{"count"}, []string{"INT8"}, [][]any{{int64(11)}})))
				check(fixture.Select("SELECT count(*) FROM fdw_analyze_parent WHERE id=9999", []string{"count"}, []string{"INT8"}, [][]any{{int64(990)}}))
				if r.snapshot() != before || destinationReader.snapshot().queries == 0 {
					t.Fatal("restored query borrowed source provider authority")
				}
				fdwAnalyzeSnapshotExpressionFacts(check, "0.111", "{0.25}")
				fdwAnalyzeSnapshotHistogram(check, histogram)
				if state := destinationReader.snapshot(); state.openRows != 0 || state.openPools != 0 {
					t.Fatal("destination query retained source resources", state)
				}
			})
		})
	}
}

const fdwAnalyzeSnapshotExpressionFrom = " FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid WHERE s.stxname='fdw_analyze_snapshot_expression' AND d.stxdinherit"

func fdwAnalyzeSnapshotExpressionFacts(run func(fixture.Step), fraction, frequencies string) {
	run(fixture.Select("SELECT (stxdexpr[1]).stanullfrac,abs((stxdexpr[1]).stadistinct+"+fraction+")<0.000001 AS distinct_fraction,(stxdexpr[1]).stakind1,(stxdexpr[1]).stavalues1::text AS values,(stxdexpr[1]).stanumbers1::text AS frequencies,(stxdexpr[1]).stakind2"+fdwAnalyzeSnapshotExpressionFrom, []string{"stanullfrac", "distinct_fraction", "stakind1", "values", "frequencies", "stakind2"}, []string{"FLOAT4", "BOOL", "INT2", "TEXT", "TEXT", "INT2"}, [][]any{{float64(0), true, int64(1), "{1}", frequencies, int64(2)}}))
}

func fdwAnalyzeSnapshotHistogram(run func(fixture.Step), histogram string) {
	run(fixture.Select("SELECT (stxdexpr[1]).stavalues2::text AS histogram"+fdwAnalyzeSnapshotExpressionFrom, []string{"histogram"}, []string{"TEXT"}, [][]any{{histogram}}))
}

func fdwAnalyzeSnapshotAcceptedHistogram(t *testing.T, r *fdwSQLReader) string {
	t.Helper()
	r.advanced.mu.Lock()
	sample := r.advanced.sample
	r.advanced.mu.Unlock()
	if len(sample.Rows) != 30 || sample.TotalRows != 990 {
		t.Fatal("source sample/population mismatch", len(sample.Rows), sample.TotalRows)
	}
	ids := make([]int64, 0, 30)
	for _, row := range sample.Rows {
		if len(row) != 3 {
			t.Fatal("source sample lost real descriptor shape", row)
		}
		id, ok := row[0].(int64)
		if !ok || id < 2 || id > 991 || row[1] != "foreign" || row[2] != true {
			t.Fatal("sample not a genuine typed source record", row)
		}
		ids = append(ids, id)
	}
	slices.Sort(ids)
	values := make([]string, len(ids))
	for i, id := range ids {
		if i != 0 && id == ids[i-1] {
			t.Fatal("remote singleton sample duplicated a source record", id)
		}
		values[i] = strconv.FormatInt(id, 10)
	}
	if state := r.snapshot(); state.openRows != 0 || state.openPools != 0 {
		t.Fatal("sample retained source resources", state)
	}
	return "{" + strings.Join(values, ",") + "}"
}
