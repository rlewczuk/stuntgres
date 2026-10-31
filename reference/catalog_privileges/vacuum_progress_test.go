package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func vacuumProgressEmpty(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	vacuumRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_stat_progress_vacuum", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
}

func TestReferenceVacuumProgress(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"vacuum_observer"})
	vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		var notices []fixture.Diagnostic
		_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, rec, s) }
		columns := []string{"pid", "datid", "datname", "relid", "phase", "heap_blks_total", "heap_blks_scanned", "heap_blks_vacuumed", "index_vacuum_count", "max_dead_tuple_bytes", "dead_tuple_bytes", "num_dead_item_ids", "indexes_total", "indexes_processed", "delay_time"}
		types := []string{"INT4", "OID", "NAME", "OID", "TEXT", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "FLOAT8"}
		s := fixture.Select("SELECT * FROM pg_stat_progress_vacuum", columns, types, nil)
		run(s)
		run(fixture.Prepared(s))
		statColumns := []string{"relid", "schemaname", "relname", "seq_scan", "last_seq_scan", "seq_tup_read", "idx_scan", "last_idx_scan", "idx_tup_fetch", "n_tup_ins", "n_tup_upd", "n_tup_del", "n_tup_hot_upd", "n_tup_newpage_upd", "n_live_tup", "n_dead_tup", "n_mod_since_analyze", "n_ins_since_vacuum", "last_vacuum", "last_autovacuum", "last_analyze", "last_autoanalyze", "vacuum_count", "autovacuum_count", "analyze_count", "autoanalyze_count", "total_vacuum_time", "total_autovacuum_time", "total_analyze_time", "total_autoanalyze_time"}
		statTypes := []string{"OID", "NAME", "NAME", "INT8", "TIMESTAMPTZ", "INT8", "INT8", "TIMESTAMPTZ", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "TIMESTAMPTZ", "TIMESTAMPTZ", "TIMESTAMPTZ", "TIMESTAMPTZ", "INT8", "INT8", "INT8", "INT8", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8"}
		for _, view := range []string{"pg_stat_all_tables", "pg_stat_user_tables", "pg_stat_sys_tables"} {
			shape := fixture.Select("SELECT * FROM "+view+" WHERE false", statColumns, statTypes, nil)
			run(shape)
			run(fixture.Prepared(shape))
		}
		run(fixture.Exec("CREATE TABLE vacuum_progress(id INT)", 0))
		run(fixture.Exec("INSERT INTO vacuum_progress VALUES(1),(2),(3)", 3))
		run(fixture.Exec("DELETE FROM vacuum_progress WHERE id=2", 1))
		run(fixture.Exec("VACUUM (FREEZE) vacuum_progress", 0))
		vacuumProgressEmpty(t, p, b, c)
		vacuumCount(t, p.Ctx, c, "vacuum_progress", 1)
		run(fixture.Select("SELECT relname,n_live_tup,n_dead_tup,vacuum_count FROM pg_stat_user_tables WHERE relid='vacuum_progress'::regclass", []string{"relname", "n_live_tup", "n_dead_tup", "vacuum_count"}, []string{"NAME", "INT8", "INT8", "INT8"}, [][]any{{"vacuum_progress", int64(2), int64(0), int64(1)}}))
		run(fixture.Select("SELECT relname FROM pg_stat_sys_tables WHERE relid='vacuum_progress'::regclass", []string{"relname"}, []string{"NAME"}, nil))
		// A second unchanged pass makes native logical counters deterministic.
		notices = nil
		run(fixture.Exec("VACUUM (VERBOSE) vacuum_progress", 0))
		if b.PG {
			// Physical details/durations are volatile. Assert genuine INFO records,
			// not equality with native logical reporting or fabricated page totals.
			matched := false
			for _, n := range notices {
				if n.Severity != "INFO" || n.SeverityUnlocalized != "INFO" || n.Code != "00000" {
					t.Fatalf("VERBOSE diagnostic %#v", n)
				}
				if n.Position != "" || n.InternalPosition != "" || n.InternalQuery != "" || n.Schema != "" || n.Table != "" || n.Column != "" || n.DataType != "" || n.Constraint != "" {
					t.Fatalf("unexpected VERBOSE fields %#v", n)
				}
				if strings.Contains(n.Message, "vacuum_progress") && strings.Contains(n.Message, "vacuum") {
					matched = true
				}
			}
			if !matched {
				t.Fatalf("missing real PostgreSQL vacuum INFO %#v", notices)
			}
		} else {
			want := []fixture.Diagnostic{{Severity: "INFO", SeverityUnlocalized: "INFO", Code: "00000", Message: `vacuuming "public"."vacuum_progress" (native logical storage): 2 live rows, 0 retained dead versions, 0 dead versions removed, 0 versions frozen`}}
			if !reflect.DeepEqual(notices, want) {
				t.Fatalf("native INFO got %#v want %#v", notices, want)
			}
		}
		vacuumProgressEmpty(t, p, b, c)
		run(fixture.Exec("CREATE ROLE vacuum_observer", 0))
		run(fixture.Exec("SET ROLE vacuum_observer", 0))
		run(s)
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT pg_read_all_stats TO vacuum_observer", 0))
		run(fixture.Exec("SET ROLE vacuum_observer", 0))
		run(s)
		run(fixture.Exec("RESET ROLE", 0))
	})
}
