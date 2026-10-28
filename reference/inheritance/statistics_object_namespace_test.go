package reference_test

import (
	"fmt"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceStatisticsObjectNamespaceLookup(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "statistics_path_a")
			fixture.SchemaReferenceCleanup(t, "statistics_path_b")
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					var notices []fixture.Diagnostic
					_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					defer conn.Close()
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
					utility := func(q string) {
						s := fixture.Exec(q, 0)
						if prepared {
							s = fixture.Prepared(s)
						}
						run(s)
					}
					for _, q := range []string{"CREATE SCHEMA statistics_path_a", "CREATE SCHEMA statistics_path_b", "CREATE TABLE stats_t(a INT,b INT)", "CREATE TEMP TABLE statistics_temp_input(a INT,b INT)", "CREATE STATISTICS statistics_path_a.path_s (mcv) ON a,b FROM stats_t", "CREATE STATISTICS statistics_path_b.path_s (dependencies) ON a,b FROM stats_t", "CREATE STATISTICS pg_temp.path_s (ndistinct) ON a,b FROM statistics_temp_input", "SET search_path=statistics_path_b,statistics_path_a,public"} {
						utility(q)
					}
					utility("DROP STATISTICS path_s RESTRICT")
					run(fixture.Select("SELECT n.nspname,s.stxkind FROM pg_statistic_ext s JOIN pg_namespace n ON n.oid=s.stxnamespace WHERE s.stxname='path_s' AND n.nspname IN ('statistics_path_a','statistics_path_b') ORDER BY n.nspname", []string{"nspname", "stxkind"}, []string{"NAME", "_CHAR"}, [][]any{{"statistics_path_a", "{m}"}}))
					utility("SET search_path=public")
					s := statisticsError("DROP STATISTICS path_s", "42704", `statistics object "path_s" does not exist`)
					if prepared {
						s = fixture.Prepared(s)
					}
					run(s)
					notices = nil
					utility("DROP STATISTICS IF EXISTS path_s")
					want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `statistics object "path_s" does not exist, skipping`}}
					if !reflect.DeepEqual(notices, want) {
						t.Fatal(notices)
					}
					// Implicit pg_temp never shadows statistics lookup; explicit pg_temp does.
					utility("DROP STATISTICS pg_temp.path_s CASCADE")
					run(fixture.Select("SELECT stxname FROM pg_statistic_ext WHERE stxrelid='statistics_temp_input'::regclass", []string{"stxname"}, []string{"NAME"}, nil))
					utility("SET search_path=statistics_path_a,public")
					utility("DROP STATISTICS path_s")
					run(fixture.Select("SELECT a,b FROM statistics_temp_input", []string{"a", "b"}, []string{"INT4", "INT4"}, nil))
					utility("DROP TABLE statistics_temp_input")
					utility("RESET search_path")
				})
			}
		})
	}
}

func TestReferenceStatisticsObjectNamespaceDependency(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "statistics_normal")
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					conn, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
					utility := func(q string) {
						s := fixture.Exec(q, 0)
						if prepared {
							s = fixture.Prepared(s)
						}
						run(s)
					}
					for _, q := range []string{"CREATE SCHEMA statistics_normal", "CREATE TABLE stats_t(a INT,b INT)", "CREATE STATISTICS statistics_normal.normal_s (mcv) ON b,a FROM stats_t"} {
						utility(q)
					}
					run(fixture.Exec("INSERT INTO stats_t VALUES(7,8)", 1))
					oid := fixture.DeparseDefinitionID(t, conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='normal_s'")
					edge := fixture.Select("SELECT d.refobjid=n.oid AS namespace_link,d.refobjsubid,d.deptype FROM pg_depend d JOIN pg_statistic_ext s ON s.oid=d.objid JOIN pg_namespace n ON n.oid=s.stxnamespace WHERE s.stxname='normal_s' AND d.classid=3381 AND d.refclassid=2615", []string{"namespace_link", "refobjsubid", "deptype"}, []string{"BOOL", "INT4", "CHAR"}, [][]any{{true, int64(0), "n"}})
					run(edge)
					want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop schema statistics_normal because other objects depend on it")
					want.Detail = "statistics object statistics_normal.normal_s depends on schema statistics_normal"
					want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
					failure := fixture.ErrorDiagnostic("DROP SCHEMA statistics_normal RESTRICT", want)
					failure.ErrorStage = "execute"
					if prepared {
						failure = fixture.Prepared(failure)
					}
					run(failure)
					if got := fixture.DeparseDefinitionID(t, conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='normal_s'"); got != oid {
						t.Fatal("RESTRICT changed identity")
					}
					run(edge)
					run(statisticsRows([]any{"normal_s", "1 2", "{m}", nil, nil}))
					utility("DROP SCHEMA statistics_normal CASCADE")
					run(statisticsRows())
					run(fixture.Select("SELECT d.objid FROM pg_depend d WHERE d.classid=3381 AND d.objid=$1", []string{"objid"}, []string{"OID"}, nil, oid))
					run(fixture.Select("SELECT a,b FROM stats_t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(8)}}))
				})
			}
		})
	}
}
