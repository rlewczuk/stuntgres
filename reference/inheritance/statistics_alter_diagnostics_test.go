package reference_test

import (
	"reflect"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceStatisticsAlterDiagnostics(t *testing.T) {
	withStatisticsAlter(t, func(t *testing.T, s ndistinctScenario) {
		statisticsAlterSetup(s)
		oid := fixture.DeparseDefinitionID(t, s.conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='sa_before'")
		for _, q := range []string{"CREATE ROLE sa_actor", "GRANT USAGE ON SCHEMA public,sa_destination TO sa_actor", "CREATE STATISTICS sa_collision ON a,b FROM sa_input", "CREATE STATISTICS sa_destination.sa_before ON a,b FROM sa_input"} {
			s.run(fixture.Exec(q, 0))
		}
		s.run(fixture.Exec("REVOKE CREATE ON SCHEMA public FROM PUBLIC", 0))
		cases := []struct{ q, code, message string }{
			{"ALTER STATISTICS absent_sa RENAME TO x", "42704", `statistics object "absent_sa" does not exist`},
			{"ALTER STATISTICS absent_sa SET SCHEMA missing_sa_schema", "42704", `statistics object "absent_sa" does not exist`},
			{"ALTER STATISTICS missing_sa_schema.absent_sa RENAME TO x", "3F000", `schema "missing_sa_schema" does not exist`},
			{"ALTER STATISTICS sa_before SET SCHEMA missing_sa_schema", "3F000", `schema "missing_sa_schema" does not exist`},
			{"ALTER STATISTICS absent_sa OWNER TO missing_sa_role", "42704", `role "missing_sa_role" does not exist`},
			{"ALTER STATISTICS sa_before RENAME TO sa_before", "42710", `statistics object "sa_before" already exists in schema "public"`},
			{"ALTER STATISTICS sa_before RENAME TO sa_collision", "42710", `statistics object "sa_collision" already exists in schema "public"`},
			{"ALTER STATISTICS sa_before SET SCHEMA sa_destination", "42710", `statistics object "sa_before" already exists in schema "sa_destination"`},
		}
		for _, tc := range cases {
			s.run(statisticsError(tc.q, tc.code, tc.message))
			statisticsAlterFacts(t, s, "sa_before")
		}
		var owner string
		if err := s.conn.QueryRowContext(s.pair.Ctx, "SELECT current_user").Scan(&owner); err != nil {
			t.Fatal(err)
		}
		s.run(fixture.Exec("SET ROLE sa_actor", 0))
		defer s.conn.ExecContext(s.pair.Ctx, "RESET ROLE")
		for _, tc := range []struct{ q, message string }{
			{"ALTER STATISTICS public.sa_before RENAME TO sa_collision", "must be owner of statistics object sa_before"},
			{"ALTER STATISTICS public.sa_before SET SCHEMA sa_destination", "permission denied for schema sa_destination"},
			{"ALTER STATISTICS public.sa_before OWNER TO sa_recipient", "must be owner of statistics object sa_before"},
			{"ALTER STATISTICS public.sa_before SET STATISTICS 0", "must be owner of statistics object public.sa_before"},
		} {
			s.run(statisticsError(tc.q, "42501", tc.message))
		}
		s.run(statisticsError("ALTER STATISTICS public.sa_before SET SCHEMA public", "42501", "permission denied for schema public"))
		s.run(fixture.Exec("RESET ROLE", 0))
		s.run(fixture.Exec("GRANT CREATE ON SCHEMA public,sa_destination TO sa_actor", 0))
		s.run(fixture.Exec("SET ROLE sa_actor", 0))
		s.run(statisticsError("ALTER STATISTICS public.sa_before SET SCHEMA sa_destination", "42501", "must be owner of statistics object sa_before"))
		s.run(fixture.Exec("ALTER STATISTICS public.sa_before SET SCHEMA public", 0))
		s.run(fixture.Exec("ALTER STATISTICS public.sa_before OWNER TO "+owner, 0))
		s.run(statisticsError("ALTER STATISTICS sa_before SET STATISTICS -2", "22023", "statistics target -2 is too low"))
		s.run(fixture.Exec("RESET ROLE", 0))
		// CREATE denial precedes an existing destination collision.
		s.run(fixture.Exec("ALTER STATISTICS sa_before OWNER TO sa_actor", 0))
		s.run(fixture.Exec("REVOKE CREATE ON SCHEMA public,sa_destination FROM sa_actor", 0))
		s.run(fixture.Exec("SET ROLE sa_actor", 0))
		s.run(statisticsError("ALTER STATISTICS sa_before RENAME TO sa_collision", "42501", "permission denied for schema public"))
		s.run(statisticsError("ALTER STATISTICS sa_before SET SCHEMA sa_destination", "42501", "permission denied for schema sa_destination"))
		s.run(fixture.Exec("RESET ROLE", 0))
		s.run(fixture.Exec("GRANT USAGE ON SCHEMA public TO sa_recipient", 0))
		var notices []fixture.Diagnostic
		_, actor, rec := fixture.NoticeReferencePool(t, s.pair, s.pg, &notices)
		fixture.RunStep(t, s.pair.Ctx, actor, rec, fixture.Exec("SET ROLE sa_recipient", 0))
		defer actor.ExecContext(s.pair.Ctx, "RESET ROLE")
		step := statisticsError("ALTER STATISTICS public.sa_before SET STATISTICS 10001", "42501", "must be owner of statistics object public.sa_before")
		step.Prepared = s.prepared
		fixture.RunStep(t, s.pair.Ctx, actor, rec, step)
		if !reflect.DeepEqual(notices, []fixture.Diagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "lowering statistics target to 10000"}}) {
			t.Fatal(notices)
		}
		// Whole-input and Unicode character positions are independent of byte offsets.
		for _, tc := range []struct{ q, token string }{
			{"ALTER STATISTICS IF EXISTS sa_before RENAME TO x", "RENAME"},
			{"ALTER STATISTICS sa_before SET SCHEMA", ""},
			{"ALTER STATISTICS sa_before RENAME TO x trailing", "trailing"},
			{"ALTER STATISTICS sa_before SET STATISTICS 1.5", "1.5"},
			{"ALTER STATISTICS sa_before SET STATISTICS 2147483648", "2147483648"},
		} {
			q := "/* é界 */ " + tc.q
			pos := len(q)
			message := "syntax error at end of input"
			if tc.token != "" {
				pos = strings.LastIndex(q, tc.token)
				message = `syntax error at or near "` + tc.token + `"`
			}
			want := fixture.ControlsReferenceDiagnostic("42601", message)
			want.Position = strconv.Itoa(utf8.RuneCountInString(q[:pos]) + 1)
			step := fixture.ErrorDiagnostic(q, want)
			step.ErrorStage = "execute"
			if s.prepared {
				step.ErrorStage = "prepare"
			}
			s.run(step)
		}
		if got := fixture.DeparseDefinitionID(t, s.conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='sa_before' AND stxnamespace='public'::regnamespace"); got != oid {
			t.Fatal("failure changed identity")
		}
		statisticsAlterFacts(t, s, "sa_before")
		s.run(fixture.Select("SELECT n.nspname,s.stxname,s.stxkeys,s.stxkind,s.stxstattarget FROM pg_statistic_ext s JOIN pg_namespace n ON n.oid=s.stxnamespace ORDER BY n.nspname,s.stxname", []string{"nspname", "stxname", "stxkeys", "stxkind", "stxstattarget"}, []string{"NAME", "NAME", "INT2VECTOR", "_CHAR", "INT2"}, [][]any{{"public", "sa_before", "1 2", "{d,f,m}", nil}, {"public", "sa_collision", "1 2", "{d,f,m}", nil}, {"sa_destination", "sa_before", "1 2", "{d,f,m}", nil}}))
		for _, q := range []string{`CREATE SCHEMA sa_hidden`, `CREATE STATISTICS sa_hidden."é odd" (ndistinct) ON a,b FROM sa_input`, `SET search_path=sa_hidden,public`, `ALTER STATISTICS "é odd" RENAME TO "界 new"`, `ALTER STATISTICS "界 new" SET SCHEMA sa_destination`, `RESET search_path`} {
			s.run(fixture.Exec(q, 0))
		}
		s.run(fixture.Select(`SELECT s.stxname,n.nspname FROM pg_statistic_ext s JOIN pg_namespace n ON n.oid=s.stxnamespace WHERE s.stxname='界 new'`, []string{"stxname", "nspname"}, []string{"NAME", "NAME"}, [][]any{{"界 new", "sa_destination"}}))
		s.run(fixture.Exec("CREATE TEMP TABLE sa_temp_input(a INT,b INT)", 0))
		s.run(fixture.Exec("CREATE STATISTICS pg_temp.sa_temp (ndistinct) ON a,b FROM sa_temp_input", 0))
		s.run(statisticsError("ALTER STATISTICS sa_temp SET STATISTICS 0", "42704", `statistics object "sa_temp" does not exist`))
		s.run(fixture.Exec("ALTER STATISTICS pg_temp.sa_temp RENAME TO sa_temp_after", 0))
	})
}
