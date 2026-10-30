package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func fdwAnalyzeNotices(t *testing.T, p *fixture.Pair, b fixture.Backend, r *fdwSQLReader, run func(fixture.Step)) {
	t.Helper()
	var notices []fixture.Diagnostic
	_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
	execute := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, recorder, step) }
	execute(fixture.Exec("ANALYZE VERBOSE fdw_data", 0))
	want := fixture.Diagnostic{Severity: "INFO", SeverityUnlocalized: "INFO", Code: "00000", Message: `analyzing "public.fdw_data"`}
	if len(notices) == 0 || notices[0] != want {
		t.Fatal("foreign VERBOSE notice", notices)
	}
	execute(fixture.Exec("SET ROLE fdw_reader", 0))
	defer func() { _, _ = c.ExecContext(p.Ctx, "RESET ROLE") }()
	notices = nil
	before := r.snapshot()
	execute(fixture.Exec("ANALYZE fdw_data", 0))
	want = fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: `permission denied to analyze "fdw_data", skipping it`}
	if len(notices) != 1 || notices[0] != want {
		t.Fatal("local maintenance denial", notices)
	}
	if !b.PG && r.snapshot() != before {
		t.Fatal("denied ANALYZE entered provider")
	}
	execute(fixture.Exec("RESET ROLE", 0))
	if b.PG {
		return
	}
	// Genuine optional absence and false negotiation each warn without a mapping.
	for _, handler := range []string{"fdw_scan_only_handler", "fdw_sql_handler"} {
		run(fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_unsupported_wrapper HANDLER "+handler, 0))
		run(fixture.Exec("CREATE SERVER fdw_unsupported_server FOREIGN DATA WRAPPER fdw_unsupported_wrapper", 0))
		run(fixture.Exec("CREATE FOREIGN TABLE fdw_unsupported(id integer) SERVER fdw_unsupported_server", 0))
		advanced := r.advanced
		r.advanced = nil
		want = fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: `skipping "fdw_unsupported" --- cannot analyze this foreign table`}
		for _, query := range []string{"ANALYZE fdw_unsupported", "ANALYZE fdw_unsupported(missing)", "ANALYZE fdw_unsupported(id,id)"} {
			check := func(executor fixture.Executor, step fixture.Step) {
				t.Helper()
				before := r.snapshot()
				notices = nil
				fixture.RunStep(t, p.Ctx, executor, recorder, step)
				if len(notices) != 1 || notices[0] != want {
					t.Fatal("unsupported capability/column precedence", notices)
				}
				after := r.snapshot()
				if handler == "fdw_sql_handler" {
					after.calls--
				} // One false metadata-only negotiation.
				if after != before {
					t.Fatal("unsupported analysis acquired/scanned data or repeated negotiation", before, after)
				}
			}
			check(c, fixture.Exec(query, 0))
			check(c, fixture.Prepared(fixture.Exec(query, 0)))
			prepareState := r.snapshot()
			notices = nil
			statement, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			if r.snapshot() != prepareState || len(notices) != 0 {
				statement.Close()
				t.Fatal("unsupported Prepare ran capability/column admission")
			}
			held := fixture.HeldStatement{Executor: c, Statement: statement, SQL: query}
			for range 2 {
				check(held, fixture.Exec(query, 0))
			}
			if err := statement.Close(); err != nil {
				t.Fatal(err)
			}
		}
		run(fixture.Select("SELECT count(*) FROM pg_user_mapping m JOIN pg_foreign_server s ON m.umserver=s.oid WHERE s.srvname='fdw_unsupported_server'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select("SELECT reltuples FROM pg_class WHERE relname='fdw_unsupported'", []string{"reltuples"}, []string{"FLOAT4"}, [][]any{{float64(-1)}}))
		r.advanced = advanced
		run(fixture.Exec("DROP SERVER fdw_unsupported_server CASCADE", 0))
		run(fixture.Exec("DROP FOREIGN DATA WRAPPER fdw_unsupported_wrapper", 0))
	}
}
