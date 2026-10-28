package reference_test

import (
	"context"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceStatisticsObjectBulkOwnership(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"statistics_bulk_old", "statistics_bulk_new"})
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					conn, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					defer conn.ExecContext(context.Background(), "RESET ROLE")
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
					for _, q := range []string{
						"CREATE ROLE statistics_bulk_old", "CREATE ROLE statistics_bulk_new",
						"GRANT USAGE,CREATE ON SCHEMA public TO statistics_bulk_old",
						"GRANT USAGE ON SCHEMA public TO statistics_bulk_new",
						"CREATE TABLE stats_t(a INT,b INT)", "ALTER TABLE stats_t OWNER TO statistics_bulk_old",
						"SET ROLE statistics_bulk_old", "CREATE STATISTICS reassigned_s (dependencies) ON b,a FROM stats_t",
						"CREATE STATISTICS removed_s (ndistinct) ON a,b FROM stats_t", "RESET ROLE",
						"ALTER TABLE stats_t OWNER TO CURRENT_USER", "CREATE STATISTICS retained_s (mcv) ON a,b FROM stats_t",
					} {
						run(fixture.Exec(q, 0))
					}
					run(fixture.Exec("INSERT INTO stats_t VALUES(11,12)", 1))
					ids := map[string]int64{}
					for _, name := range []string{"reassigned_s", "removed_s"} {
						ids[name] = fixture.DeparseDefinitionID(t, conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='"+name+"'")
					}
					reassign := fixture.Exec("REASSIGN OWNED BY statistics_bulk_old TO statistics_bulk_new", 0)
					if prepared {
						reassign = fixture.Prepared(reassign)
					}
					run(reassign)
					for name, id := range ids {
						if got := fixture.DeparseDefinitionID(t, conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='"+name+"'"); got != id {
							t.Fatal("reassignment replaced identity", name, id, got)
						}
					}
					run(fixture.Select("SELECT s.stxname,r.rolname,s.stxkeys,s.stxkind FROM pg_statistic_ext s JOIN pg_roles r ON r.oid=s.stxowner WHERE s.stxname IN ('reassigned_s','removed_s') ORDER BY s.stxname", []string{"stxname", "rolname", "stxkeys", "stxkind"}, []string{"NAME", "NAME", "INT2VECTOR", "_CHAR"}, [][]any{{"reassigned_s", "statistics_bulk_new", "1 2", "{f}"}, {"removed_s", "statistics_bulk_new", "1 2", "{d}"}}))
					run(fixture.Exec("SET ROLE statistics_bulk_old", 0))
					run(statisticsError("DROP STATISTICS reassigned_s", "42501", "must be owner of statistics object reassigned_s"))
					run(fixture.Exec("RESET ROLE", 0))
					drop := fixture.Exec("DROP OWNED BY statistics_bulk_new RESTRICT", 0)
					if prepared {
						drop = fixture.Prepared(drop)
					}
					run(drop)
					run(statisticsRows([]any{"retained_s", "1 2", "{m}", nil, nil}))
					run(fixture.Select("SELECT d.objid::bigint AS objid FROM pg_depend d WHERE d.classid=3381 AND d.objid IN ($1,$2)", []string{"objid"}, []string{"INT8"}, nil, ids["reassigned_s"], ids["removed_s"]))
					run(fixture.Select("SELECT a,b FROM stats_t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(11), int64(12)}}))
				})
			}
		})
	}
}
