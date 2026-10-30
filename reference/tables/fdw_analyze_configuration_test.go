package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFDWAnalyzeConfiguration(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		wantRows := fdwAdvancedLive(t, p, b, r, run)
		configuration := func(target, options any) {
			t.Helper()
			if value, ok := target.(int); ok {
				target = int64(value)
			}
			run(fixture.Select("SELECT attstattarget,attoptions::text AS options FROM pg_attribute WHERE attrelid='fdw_data'::regclass AND attname='label'", []string{"attstattarget", "options"}, []string{"INT2", "TEXT"}, [][]any{{target, options}}))
		}
		set := func(query string, prepared bool, target, options any) {
			t.Helper()
			before := r.snapshot()
			step := fixture.Exec(query, 0)
			if prepared {
				step = fixture.Prepared(step)
			}
			run(step)
			if !b.PG && r.snapshot() != before {
				t.Fatal("statistics metadata action called remote provider")
			}
			configuration(target, options)
			fdwAssertSource(t, p, b, r, wantRows)
		}
		configuration(nil, nil)
		set("ALTER FOREIGN TABLE fdw_data ALTER label SET STATISTICS 7", false, 7, nil)
		set("ALTER FOREIGN TABLE fdw_data ALTER label SET STATISTICS DEFAULT", true, nil, nil)
		run(fixture.Exec("ANALYZE fdw_data(label)", 0))
		fdwWantEstimate(t, p, c, "label='common'", 14)
		set("ALTER FOREIGN TABLE fdw_data ALTER label SET STATISTICS 3", true, 3, nil)
		set("ALTER FOREIGN TABLE fdw_data ALTER label SET STATISTICS -1", false, nil, nil)
		run(fixture.Prepared(fixture.Exec("ANALYZE fdw_data(label)", 0)))
		fdwWantEstimate(t, p, c, "label='common'", 14)

		var notices []fixture.Diagnostic
		_, noticeConn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		before := r.snapshot()
		fixture.RunStep(t, p.Ctx, noticeConn, recorder, fixture.Prepared(fixture.Exec("ALTER FOREIGN TABLE fdw_data ALTER label SET STATISTICS 10001", 0)))
		warning := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "lowering statistics target to 10000"}
		if len(notices) != 1 || notices[0] != warning {
			t.Fatal("upper target clamp diagnostic", notices)
		}
		if !b.PG && r.snapshot() != before {
			t.Fatal("clamp invoked provider")
		}
		configuration(10000, nil)
		fdwAssertSource(t, p, b, r, wantRows)
		set("ALTER FOREIGN TABLE fdw_data ALTER label SET STATISTICS 7", false, 7, nil)
		set("ALTER FOREIGN TABLE fdw_data ALTER label SET (n_distinct=5)", true, 7, "{n_distinct=5}")
		for _, prepared := range []bool{false, true} {
			for _, test := range []struct{ query, message string }{
				{"ALTER FOREIGN TABLE fdw_data ALTER label SET (n_distinct=2,bogus=3)", `unrecognized parameter "bogus"`},
				{"ALTER FOREIGN TABLE fdw_data ALTER label SET (n_distinct=2,n_distinct=3)", `parameter "n_distinct" specified more than once`},
				{"ALTER FOREIGN TABLE fdw_data ALTER label SET STATISTICS 9, ALTER label SET (bogus=3)", `unrecognized parameter "bogus"`},
			} {
				before := r.snapshot()
				step := fixture.ErrorDiagnostic(test.query, fdwDiagnostic("22023", test.message))
				if prepared {
					step = fixture.Prepared(step)
				}
				run(step)
				configuration(7, "{n_distinct=5}")
				if !b.PG && r.snapshot() != before {
					t.Fatal("failed metadata action invoked provider")
				}
				fdwAssertSource(t, p, b, r, wantRows)
			}
		}
		set("ALTER FOREIGN TABLE fdw_data ALTER label RESET (n_distinct)", false, 7, nil)
		run(fixture.Exec("ANALYZE fdw_data(label)", 0))
		fdwWantEstimate(t, p, c, "label='rare'", 2)
	})
}
