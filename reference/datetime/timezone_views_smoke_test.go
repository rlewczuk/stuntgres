package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTimezoneViewsFeatureSmoke(t *testing.T) {
	timezoneViewsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(timezoneViewsNames("SELECT * FROM pg_timezone_names WHERE name='UTC'", [][]any{{"UTC", "UTC", "00:00:00", false}}))
		run(fixture.Prepared(timezoneViewsNames("SELECT * FROM pg_catalog.pg_timezone_names WHERE name=$1::text", [][]any{{"Asia/Kolkata", "IST", "05:30:00", false}}, "Asia/Kolkata")))
		run(timezoneViewsNames("SELECT * FROM pg_timezone_names WHERE name='No/Such_Zone'", [][]any{}))
		for _, nulls := range []fixture.Step{
			timezoneViewsNames("SELECT * FROM pg_catalog.pg_timezone_names WHERE name=$1::text", [][]any{}, nil),
			timezoneViewsAbbrevs("SELECT * FROM pg_catalog.pg_timezone_abbrevs WHERE abbrev=$1::text", [][]any{}, nil),
			timezoneViewsNames("SELECT * FROM pg_catalog.pg_timezone_names WHERE utc_offset=$1::interval", [][]any{}, nil),
			timezoneViewsAbbrevs("SELECT * FROM pg_catalog.pg_timezone_abbrevs WHERE is_dst=$1::boolean", [][]any{}, nil),
		} {
			nulls.CheckMetadata = func(t *testing.T, rows *sql.Rows) { timezoneViewsMetadata(t, rows, b.PG) }
			run(nulls)
			run(fixture.Prepared(nulls))
		}
		run(fixture.Exec("SET TIME ZONE 'ABC3DEF,M3.2.0,M11.1.0'", 0))
		run(timezoneViewsNames("SELECT * FROM pg_timezone_names WHERE name='ABC3DEF,M3.2.0,M11.1.0'", [][]any{}))
		run(fixture.Exec("SET TIME ZONE 'UTC'", 0))
		query := "SELECT * FROM pg_timezone_abbrevs WHERE abbrev=$1::text"
		held := timezoneHeld(t, p, c, query)
		for _, tc := range []struct{ set, zone, offset string }{
			{"Default", "UTC", "02:00:00"},
			{"India", "UTC", "05:30:00"},
			{"Default", "Asia/Kolkata", "05:30:00"},
		} {
			run(fixture.Exec("SET timezone_abbreviations='"+tc.set+"'", 0))
			run(fixture.Exec("SET TIME ZONE '"+tc.zone+"'", 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, timezoneViewsAbbrevs(query, [][]any{{"IST", tc.offset, false}}, "IST"))
		}
		run(fixture.Exec("CREATE VIEW timezone_smoke_utc AS SELECT * FROM pg_timezone_names WHERE name='UTC'", 0))
		run(fixture.Exec("BEGIN", 0))
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		run(fixture.Exec("SET TIME ZONE 'America/New_York'", 0))
		clockQuery := "SELECT utc_offset=make_interval(secs=>date_part('timezone',CURRENT_TIMESTAMP)) AS same FROM pg_timezone_names WHERE name='America/New_York'"
		for range 2 {
			run(fixture.Select(clockQuery, []string{"same"}, []string{"BOOL"}, [][]any{{true}}))
		}
		run(fixture.Select("SELECT relname,relkind,relnatts FROM pg_catalog.pg_class WHERE relname='pg_timezone_names' OR relname='pg_timezone_abbrevs' ORDER BY relname", []string{"relname", "relkind", "relnatts"}, []string{"NAME", "CHAR", "INT2"}, [][]any{{"pg_timezone_abbrevs", "v", int64(3)}, {"pg_timezone_names", "v", int64(4)}}))
		run(timezoneViewsNames("SELECT * FROM timezone_smoke_utc", [][]any{{"UTC", "UTC", "00:00:00", false}}))
		run(fixture.Exec("SAVEPOINT timezone_smoke_error", 0))
		s := fixture.ImplicitError("SELECT bogus FROM pg_catalog.pg_timezone_names", fixture.Diagnostic{Code: "42703", Message: `column "bogus" does not exist`, Position: "8"})
		s.SelectRows, s.ErrorStage = true, "execute"
		run(s)
		run(fixture.Exec("ROLLBACK TO SAVEPOINT timezone_smoke_error", 0))
		s = fixture.ImplicitError("INSERT INTO pg_catalog.pg_timezone_abbrevs VALUES('UTC',INTERVAL '0 seconds',false)", timezoneViewsReadOnlyDiagnostic("pg_timezone_abbrevs", "insert"))
		s.ErrorStage = "execute"
		run(s)
		run(fixture.Exec("ROLLBACK TO SAVEPOINT timezone_smoke_error", 0))
		for _, tc := range []struct {
			query, token string
			want         fixture.Diagnostic
		}{
			{"UPDATE pg_catalog.pg_timezone_names SET bogus=true", "bogus", fixture.Diagnostic{Code: "42703", Message: `column "bogus" of relation "pg_timezone_names" does not exist`}},
			{"DELETE FROM pg_catalog.pg_timezone_abbrevs WHERE bogus=true", "bogus", fixture.Diagnostic{Code: "42703", Message: `column "bogus" does not exist`}},
			{"INSERT INTO pg_catalog.pg_timezone_abbrevs(is_dst) VALUES(1)", "1", fixture.Diagnostic{Code: "42804", Message: `column "is_dst" is of type boolean but expression is of type integer`, Hint: "You will need to rewrite or cast the expression."}},
		} {
			tc.want.Position = strconv.Itoa(strings.Index(tc.query, tc.token) + 1)
			failure := fixture.ImplicitError(tc.query, tc.want)
			failure.ErrorStage = "execute"
			run(failure)
			run(fixture.Exec("ROLLBACK TO SAVEPOINT timezone_smoke_error", 0))
			failure.Prepared, failure.ErrorStage = true, "prepare"
			run(failure)
			run(fixture.Exec("ROLLBACK TO SAVEPOINT timezone_smoke_error", 0))
		}
		run(timezoneViewsNames("SELECT * FROM timezone_smoke_utc", [][]any{{"UTC", "UTC", "00:00:00", false}}))
		run(fixture.Exec("COMMIT", 0))
	})
}
