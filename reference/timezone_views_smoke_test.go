package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
)

func TestReferenceTimezoneViewsFeatureSmoke(t *testing.T) {
	timezoneViewsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c referenceExecutor) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(timezoneViewsNames("SELECT * FROM pg_timezone_names WHERE name='UTC'", [][]any{{"UTC", "UTC", "00:00:00", false}}))
		run(referencePrepared(timezoneViewsNames("SELECT * FROM pg_catalog.pg_timezone_names WHERE name=$1::text", [][]any{{"Asia/Kolkata", "IST", "05:30:00", false}}, "Asia/Kolkata")))
		run(timezoneViewsNames("SELECT * FROM pg_timezone_names WHERE name='No/Such_Zone'", [][]any{}))
		for _, nulls := range []referenceStep{
			timezoneViewsNames("SELECT * FROM pg_catalog.pg_timezone_names WHERE name=$1::text", [][]any{}, nil),
			timezoneViewsAbbrevs("SELECT * FROM pg_catalog.pg_timezone_abbrevs WHERE abbrev=$1::text", [][]any{}, nil),
			timezoneViewsNames("SELECT * FROM pg_catalog.pg_timezone_names WHERE utc_offset=$1::interval", [][]any{}, nil),
			timezoneViewsAbbrevs("SELECT * FROM pg_catalog.pg_timezone_abbrevs WHERE is_dst=$1::boolean", [][]any{}, nil),
		} {
			nulls.checkMetadata = func(t *testing.T, rows *sql.Rows) { timezoneViewsMetadata(t, rows, b.pg) }
			run(nulls)
			run(referencePrepared(nulls))
		}
		run(referenceExec("SET TIME ZONE 'ABC3DEF,M3.2.0,M11.1.0'", 0))
		run(timezoneViewsNames("SELECT * FROM pg_timezone_names WHERE name='ABC3DEF,M3.2.0,M11.1.0'", [][]any{}))
		run(referenceExec("SET TIME ZONE 'UTC'", 0))
		query := "SELECT * FROM pg_timezone_abbrevs WHERE abbrev=$1::text"
		held := timezoneHeld(t, p, c, query)
		for _, tc := range []struct{ set, zone, offset string }{
			{"Default", "UTC", "02:00:00"},
			{"India", "UTC", "05:30:00"},
			{"Default", "Asia/Kolkata", "05:30:00"},
		} {
			run(referenceExec("SET timezone_abbreviations='"+tc.set+"'", 0))
			run(referenceExec("SET TIME ZONE '"+tc.zone+"'", 0))
			referenceRunStep(t, p.ctx, held, b.recorder, timezoneViewsAbbrevs(query, [][]any{{"IST", tc.offset, false}}, "IST"))
		}
		run(referenceExec("CREATE VIEW timezone_smoke_utc AS SELECT * FROM pg_timezone_names WHERE name='UTC'", 0))
		run(referenceExec("BEGIN", 0))
		defer c.ExecContext(p.ctx, "ROLLBACK")
		run(referenceExec("SET TIME ZONE 'America/New_York'", 0))
		clockQuery := "SELECT utc_offset=make_interval(secs=>date_part('timezone',CURRENT_TIMESTAMP)) AS same FROM pg_timezone_names WHERE name='America/New_York'"
		for range 2 {
			run(referenceSelect(clockQuery, []string{"same"}, []string{"BOOL"}, [][]any{{true}}))
		}
		run(referenceSelect("SELECT relname,relkind,relnatts FROM pg_catalog.pg_class WHERE relname='pg_timezone_names' OR relname='pg_timezone_abbrevs' ORDER BY relname", []string{"relname", "relkind", "relnatts"}, []string{"NAME", "CHAR", "INT2"}, [][]any{{"pg_timezone_abbrevs", "v", int64(3)}, {"pg_timezone_names", "v", int64(4)}}))
		run(timezoneViewsNames("SELECT * FROM timezone_smoke_utc", [][]any{{"UTC", "UTC", "00:00:00", false}}))
		run(referenceExec("SAVEPOINT timezone_smoke_error", 0))
		s := referenceImplicitError("SELECT bogus FROM pg_catalog.pg_timezone_names", referenceDiagnostic{Code: "42703", Message: `column "bogus" does not exist`, Position: "8"})
		s.selectRows, s.errorStage = true, "execute"
		run(s)
		run(referenceExec("ROLLBACK TO SAVEPOINT timezone_smoke_error", 0))
		s = referenceImplicitError("INSERT INTO pg_catalog.pg_timezone_abbrevs VALUES('UTC',INTERVAL '0 seconds',false)", timezoneViewsReadOnlyDiagnostic("pg_timezone_abbrevs", "insert"))
		s.errorStage = "execute"
		run(s)
		run(referenceExec("ROLLBACK TO SAVEPOINT timezone_smoke_error", 0))
		for _, tc := range []struct {
			query, token string
			want         referenceDiagnostic
		}{
			{"UPDATE pg_catalog.pg_timezone_names SET bogus=true", "bogus", referenceDiagnostic{Code: "42703", Message: `column "bogus" of relation "pg_timezone_names" does not exist`}},
			{"DELETE FROM pg_catalog.pg_timezone_abbrevs WHERE bogus=true", "bogus", referenceDiagnostic{Code: "42703", Message: `column "bogus" does not exist`}},
			{"INSERT INTO pg_catalog.pg_timezone_abbrevs(is_dst) VALUES(1)", "1", referenceDiagnostic{Code: "42804", Message: `column "is_dst" is of type boolean but expression is of type integer`, Hint: "You will need to rewrite or cast the expression."}},
		} {
			tc.want.Position = strconv.Itoa(strings.Index(tc.query, tc.token) + 1)
			failure := referenceImplicitError(tc.query, tc.want)
			failure.errorStage = "execute"
			run(failure)
			run(referenceExec("ROLLBACK TO SAVEPOINT timezone_smoke_error", 0))
			failure.prepared, failure.errorStage = true, "prepare"
			run(failure)
			run(referenceExec("ROLLBACK TO SAVEPOINT timezone_smoke_error", 0))
		}
		run(timezoneViewsNames("SELECT * FROM timezone_smoke_utc", [][]any{{"UTC", "UTC", "00:00:00", false}}))
		run(referenceExec("COMMIT", 0))
	})
}
