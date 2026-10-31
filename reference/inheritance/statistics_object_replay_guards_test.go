package reference_test

import (
	"bytes"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceStatisticsAlterRestoreOwnership(t *testing.T) {
	const prefix = "CREATE TABLE public.replay_input(a INT,b INT); "
	const directives = "SET SESSION AUTHORIZATION 'statistics_replay_owner'; SET SESSION AUTHORIZATION DEFAULT; "
	const create = "CREATE STATISTICS public.replay_s (mcv,dependencies) ON b,a FROM public.replay_input; "
	const owner = "ALTER STATISTICS public.replay_s OWNER TO statistics_replay_owner; "
	for _, tc := range []struct {
		name, script string
		valid        bool
	}{
		{"exact", prefix + directives + create + owner, true},
		{"missing_directives", prefix + create + owner, true},
		{"missing_default", prefix + "SET SESSION AUTHORIZATION 'statistics_replay_owner'; " + create + owner, false},
		{"wrong_recipient", prefix + directives + create + "ALTER STATISTICS public.replay_s OWNER TO statistics_replay_other;", true},
		{"identity_recipient", prefix + directives + create + "ALTER STATISTICS public.replay_s OWNER TO CURRENT_USER;", true},
		{"unqualified", prefix + directives + create + "ALTER STATISTICS replay_s OWNER TO statistics_replay_owner;", true},
		{"wrong_namespace", prefix + directives + create + "ALTER STATISTICS pg_catalog.replay_s OWNER TO statistics_replay_owner;", false},
		{"wrong_name", prefix + directives + create + "ALTER STATISTICS public.other_s OWNER TO statistics_replay_owner;", false},
		{"missing_recipient", prefix + create + "ALTER STATISTICS public.replay_s OWNER TO statistics_missing_role;", false},
		{"unauthorized", prefix + create + "SET SESSION AUTHORIZATION 'statistics_replay_other'; " + owner, false},
		{"wrong_kind", prefix + directives + create + "ALTER TABLE public.replay_input OWNER TO statistics_replay_owner;", false},
		{"existing_other", prefix + "CREATE STATISTICS public.other_s ON a,b FROM public.replay_input; " + directives + create + "ALTER STATISTICS public.other_s OWNER TO statistics_replay_owner;", true},
		{"intervening_statement", prefix + directives + create + "CREATE TABLE public.gap(a INT); " + owner, true},
		{"intervening_directive", prefix + directives + create + "SET SESSION AUTHORIZATION DEFAULT; " + owner, true},
		{"reversed_pair", prefix + create + directives + owner, true},
		{"consumed_pair", prefix + directives + create + owner + owner, true},
		{"no_constructor", prefix + directives + owner, false},
		{"duplicate_constructor", prefix + create + directives + create + owner, false},
		{"conditional_skip", prefix + create + directives + "CREATE STATISTICS IF NOT EXISTS public.replay_s ON a,b FROM public.replay_input; " + owner, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"statistics_replay_owner", "statistics_replay_other"})
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					conn, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
					for _, q := range []string{"CREATE ROLE statistics_replay_owner", "CREATE ROLE statistics_replay_other", "GRANT CREATE ON SCHEMA public TO statistics_replay_owner", "CREATE TABLE stats_t(a INT,b INT)", "INSERT INTO stats_t VALUES(41,42)", "CREATE STATISTICS sentinel_s (ndistinct) ON a,b FROM stats_t"} {
						n := int64(0)
						if strings.HasPrefix(q, "INSERT") {
							n = 1
						}
						run(fixture.Exec(q, n))
					}
					run(fixture.Exec("GRANT USAGE ON SCHEMA public TO statistics_replay_owner,statistics_replay_other", 0))
					oid := fixture.DeparseDefinitionID(t, conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='sentinel_s'")
					want := ptrStatisticsDiagnostic("42704", `statistics object "public.replay_s" does not exist`)
					switch tc.name {
					case "missing_default":
						want = ptrStatisticsDiagnostic("42501", "must be owner of table replay_input")
					case "wrong_namespace":
						want = ptrStatisticsDiagnostic("42704", `statistics object "pg_catalog.replay_s" does not exist`)
					case "wrong_name":
						want = ptrStatisticsDiagnostic("42704", `statistics object "public.other_s" does not exist`)
					case "wrong_kind":
						want = ptrStatisticsDiagnostic("0A000", "restore accepts only immediately paired table declaration ownership")
					case "duplicate_constructor":
						want = ptrStatisticsDiagnostic("42710", `statistics object "replay_s" already exists`)
					case "missing_recipient":
						want = ptrStatisticsDiagnostic("42704", `role "statistics_missing_role" does not exist`)
					case "unauthorized":
						want = ptrStatisticsDiagnostic("42501", "must be owner of statistics object replay_s")
					}
					if !b.PG {
						var before, after bytes.Buffer
						if err = stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
							t.Fatal(err)
						}
						err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(tc.script))
						if !tc.valid {
							if err == nil {
								t.Fatal("invalid replay accepted")
							}
							fixture.AssertDiagnostic(t, err, want)
							if err = stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
								t.Fatal(err)
							}
							if !bytes.Equal(before.Bytes(), after.Bytes()) {
								t.Fatal("failed replay changed destination")
							}
						} else if err != nil {
							t.Fatal(err)
						}
					} else if tc.valid {
						run(fixture.Exec("DROP STATISTICS sentinel_s", 0))
						run(fixture.Exec("DROP TABLE stats_t", 0))
						// Replay the ordinary valid recipe on the release oracle.
						for _, q := range strings.Split(tc.script, ";") {
							if strings.TrimSpace(q) != "" {
								run(fixture.Exec(q, 0))
							}
						}
						run(fixture.Exec("SET SESSION AUTHORIZATION DEFAULT", 0))
					} else if tc.name != "wrong_kind" {
						// Native Restore's atomic replacement has a SQL analogue for
						// ordinary errors: failed replay inside a real transaction.
						run(fixture.Exec("BEGIN", 0))
						_, replayErr := conn.ExecContext(p.Ctx, tc.script)
						fixture.AssertDiagnostic(t, replayErr, want)
						run(fixture.Exec("ROLLBACK", 0))
						run(fixture.Exec("SET SESSION AUTHORIZATION DEFAULT", 0))
					}
					if tc.valid {
						expectedOwner := "statistics_replay_owner"
						if tc.name == "wrong_recipient" {
							expectedOwner = "statistics_replay_other"
						}
						if tc.name == "identity_recipient" || tc.name == "existing_other" {
							if err := conn.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&expectedOwner); err != nil {
								t.Fatal(err)
							}
						}
						run(fixture.Select("SELECT s.stxname,s.stxkeys,s.stxkind,r.rolname FROM pg_statistic_ext s JOIN pg_roles r ON r.oid=s.stxowner WHERE s.stxname='replay_s'", []string{"stxname", "stxkeys", "stxkind", "rolname"}, []string{"NAME", "INT2VECTOR", "_CHAR", "NAME"}, [][]any{{"replay_s", "1 2", "{f,m}", expectedOwner}}))
					} else {
						run(statisticsRows([]any{"sentinel_s", "1 2", "{d}", nil, nil}))
						if got := fixture.DeparseDefinitionID(t, conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='sentinel_s'"); got != oid {
							t.Fatal("failed replay changed sentinel identity")
						}
						run(fixture.Select("SELECT a,b FROM stats_t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(41), int64(42)}}))
					}
				})
			}
		})
	}
}

func ptrStatisticsDiagnostic(code, message string) *fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic(code, message)
	return &d
}
