package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatXactUserTablesMembership(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xactUserCreate(t, p, b, c, "xut_members")
		xactSysCreate(t, p, b, c, "xut_system")
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
			defer cancel()
			for _, q := range []string{"DROP SERVER IF EXISTS xut_server CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS xut_fdw"} {
				if _, err := b.DB.ExecContext(ctx, q); err != nil {
					t.Error(err)
				}
			}
		})
		for _, q := range []string{
			"CREATE VIEW public.xut_view AS SELECT n FROM public.xut_members",
			"CREATE SEQUENCE public.xut_sequence",
			"CREATE INDEX xut_index ON public.xut_members(n)",
			"CREATE FOREIGN DATA WRAPPER xut_fdw",
			"CREATE SERVER xut_server FOREIGN DATA WRAPPER xut_fdw",
			"CREATE FOREIGN TABLE public.xut_foreign(n integer) SERVER xut_server",
			"CREATE TEMP TABLE xut_temp(n integer)",
		} {
			fixture.StatisticsExec(t, p, b, c, q, 0)
		}
		for _, tc := range []struct {
			name string
			count int64
		}{{"xut_members", 1}, {"xut_system", 0}, {"pg_class", 0}, {"pg_attribute", 0}, {"xut_view", 0}, {"xut_sequence", 0}, {"xut_index", 0}, {"xut_foreign", 0}, {"pg_stat_xact_user_tables", 0}, {"xut_temp", 1}} {
			for _, prepared := range []bool{false, true} {
				step := fixture.Select("SELECT count(*) FROM pg_stat_xact_user_tables WHERE relname=$1", []string{"count"}, []string{"INT8"}, [][]any{{tc.count}}, tc.name)
				step.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			}
		}
		for _, tc := range []struct {
			name string
			user, sys int64
		}{{"xut_members", 1, 0}, {"xut_system", 0, 1}} {
			for _, view := range []string{"pg_stat_xact_all_tables", "pg_stat_xact_user_tables", "pg_stat_xact_sys_tables"} {
				want := int64(1)
				if view == "pg_stat_xact_user_tables" { want = tc.user }
				if view == "pg_stat_xact_sys_tables" { want = tc.sys }
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM "+view+" WHERE relname=$1", []string{"count"}, []string{"INT8"}, [][]any{{want}}, tc.name))
			}
		}
		query := func(view, filter string) []int64 {
			rows, err := c.QueryContext(p.Ctx, "SELECT relid FROM "+view+" WHERE relname IN ('xut_members','xut_system','pg_class','pg_attribute','xut_view','xut_sequence','xut_index','xut_foreign','xut_temp')"+filter+" ORDER BY relid")
			if err != nil { t.Fatal(err) }
			defer rows.Close()
			var ids []int64
			for rows.Next() {
				var id int64
				if err := rows.Scan(&id); err != nil { t.Fatal(err) }
				ids = append(ids, id)
			}
			if err := rows.Err(); err != nil { t.Fatal(err) }
			return ids
		}
		all := query("pg_stat_xact_all_tables", " AND schemaname NOT IN ('pg_catalog','information_schema') AND schemaname !~ '^pg_toast'")
		user := query("pg_stat_xact_user_tables", "")
		if len(user) != 2 || !reflect.DeepEqual(user, all) {
			t.Fatal("exact represented USER complement bag", user, all)
		}
		if temp := xactUserGet(t, p, c, "xut_temp"); !strings.HasPrefix(temp.Schema, "pg_temp_") {
			t.Fatal("TEMP must be a USER namespace", temp)
		}
		old := xactUserGet(t, p, c, "xut_members")
		membership := func(user, sys int64) {
			t.Helper()
			for _, tc := range []struct { view string; count int64 }{{"pg_stat_xact_all_tables", 1}, {"pg_stat_xact_user_tables", user}, {"pg_stat_xact_sys_tables", sys}} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM "+tc.view+" WHERE relid=$1", []string{"count"}, []string{"INT8"}, [][]any{{tc.count}}, old.ID))
			}
		}
		fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
		fixture.StatisticsExec(t, p, b, c, "ALTER TABLE public.xut_members SET SCHEMA information_schema", 0)
		membership(0, 1)
		fixture.StatisticsExec(t, p, b, c, "ALTER TABLE information_schema.xut_members SET SCHEMA public", 0)
		membership(1, 0)
		fixture.StatisticsExec(t, p, b, c, "SAVEPOINT moved", 0)
		fixture.StatisticsExec(t, p, b, c, "ALTER TABLE public.xut_members SET SCHEMA information_schema", 0)
		membership(0, 1)
		fixture.StatisticsExec(t, p, b, c, "ROLLBACK TO moved", 0)
		membership(1, 0)
		if restored := xactUserGet(t, p, c, "xut_members"); restored.ID != old.ID || restored.Schema != "public" {
			t.Fatal("move rollback reminted identity", restored, old)
		}
		fixture.StatisticsExec(t, p, b, c, "ROLLBACK", 0)
	})
}
