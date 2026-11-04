package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatUserTablesIdentityAndMetadata(t *testing.T) {
	tableStatIdentityAndMetadata(t, tableStatFamily{user: true})
}

func TestReferencePGStatUserTablesViewDefinition(t *testing.T) {
	tableStatViewDefinition(t, tableStatFamily{user: true})
}

func TestReferencePGStatUserTablesQueryComposition(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		f := tableStatFamily{user: true}
		f.create(t, p, b, c, "sut_identity")
		id := f.get(t, p, c, "sut_identity").ID
		q := "SELECT s.relname,c.relname,s.seq_scan,s.last_seq_scan FROM pg_catalog.pg_stat_user_tables s JOIN pg_catalog.pg_class c ON c.oid=s.relid WHERE s.relid=$1 AND s.schemaname='public' ORDER BY s.relname"
		for _, prepared := range []bool{false, true} {
			for _, oid := range []int64{id, 4294967295} {
				var want [][]any
				if oid == id {
					want = [][]any{{"sut_identity", "sut_identity", int64(0), nil}}
				}
				step := fixture.Select(q, []string{"relname", "relname", "seq_scan", "last_seq_scan"}, []string{"NAME", "NAME", "INT8", "TIMESTAMPTZ"}, want, oid)
				step.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			}
			step := fixture.ErrorDiagnostic("SELECT missing FROM pg_catalog.pg_stat_user_tables", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: `column "missing" does not exist`, Position: "8"})
			step.Prepared = prepared
			step.SelectRows = true
			if prepared {
				step.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
		}
	})
}

func TestReferencePGStatUserTablesMembership(t *testing.T) {
	for _, schema := range []string{"information_schema_extra", "xpg_toast", "PG_TOAST"} {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
			defer cancel()
			for _, q := range []string{"DROP TABLE IF EXISTS information_schema.sut_members CASCADE", "DROP SERVER IF EXISTS sut_server CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS sut_fdw"} {
				if _, err := c.ExecContext(ctx, q); err != nil {
					t.Error("membership cleanup", err)
				}
			}
		}()
		f := tableStatFamily{user: true}
		f.create(t, p, b, c, "sut_members")
		f.seed(t, p, b, "sut_members")
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		fixture.StatisticsExec(t, p, b, c, "SET track_counts=off", 0)
		for _, setup := range []struct {
			query string
			count int64
		}{
			{"CREATE VIEW sut_view AS SELECT n FROM sut_members", 0},
			{"CREATE SEQUENCE sut_sequence", 0},
			{"CREATE INDEX sut_index ON sut_members(n)", 0},
			{"CREATE MATERIALIZED VIEW sut_mat AS SELECT n FROM sut_members", 3},
			{"CREATE TABLE sut_partition(n integer) PARTITION BY RANGE(n)", 0},
			{"CREATE TABLE sut_leaf PARTITION OF sut_partition FOR VALUES FROM(0) TO(10)", 0},
			{"CREATE FOREIGN DATA WRAPPER sut_fdw", 0},
			{"CREATE SERVER sut_server FOREIGN DATA WRAPPER sut_fdw", 0},
			{"CREATE FOREIGN TABLE sut_foreign(n integer) SERVER sut_server", 0},
			{"CREATE TEMP TABLE sut_temp(n integer)", 0},
		} {
			fixture.StatisticsExec(t, p, b, c, setup.query, setup.count)
		}
		fixture.StatisticsExec(t, p, b, c, "SET track_counts=on", 0)
		for _, schema := range []string{"information_schema_extra", "xpg_toast", "PG_TOAST"} {
			fixture.StatisticsExec(t, p, b, c, `CREATE SCHEMA "`+schema+`"`, 0)
			f.create(t, p, b, c, `"`+schema+`".sut_boundary`)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_stat_user_tables WHERE schemaname=$1 AND relname='sut_boundary'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, schema))
		}
		for _, tc := range []struct{ name string; present int64 }{{"sut_members", 1}, {"sut_mat", 1}, {"sut_partition", 1}, {"sut_leaf", 1}, {"sut_temp", 1}, {"sut_view", 0}, {"sut_sequence", 0}, {"sut_index", 0}, {"sut_foreign", 0}} {
			for _, prepared := range []bool{false, true} {
				step := fixture.Select("SELECT count(*) FROM pg_stat_user_tables WHERE relname=$1", []string{"count"}, []string{"INT8"}, [][]any{{tc.present}}, tc.name)
				step.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			}
		}
		type identity struct { ID int64; Schema, Name string }
		bag := func(view string) map[identity]int {
			rows, err := c.QueryContext(p.Ctx, "SELECT relid,schemaname,relname FROM pg_catalog."+view)
			if err != nil { t.Fatal(err) }
			defer rows.Close()
			out := make(map[identity]int)
			for rows.Next() {
				var id identity
				if err := rows.Scan(&id.ID, &id.Schema, &id.Name); err != nil { t.Fatal(err) }
				out[id]++
			}
			if err := rows.Err(); err != nil { t.Fatal(err) }
			return out
		}
		all, sys, user := bag("pg_stat_all_tables"), bag("pg_stat_sys_tables"), bag("pg_stat_user_tables")
		wantSys, wantUser := make(map[identity]int), make(map[identity]int)
		for id, n := range all {
			if id.Schema == "pg_catalog" || id.Schema == "information_schema" || strings.HasPrefix(id.Schema, "pg_toast") { wantSys[id] = n } else { wantUser[id] = n }
		}
		if !reflect.DeepEqual(sys, wantSys) || !reflect.DeepEqual(user, wantUser) { t.Fatal("ALL partition identity bags", sys, wantSys, user, wantUser) }
		before := f.get(t, p, c, "sut_members")
		for _, tc := range []struct{ view string; count int64 }{{"all", 1}, {"user", 1}, {"sys", 0}} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_stat_"+tc.view+"_tables WHERE relid=$1", []string{"count"}, []string{"INT8"}, [][]any{{tc.count}}, before.ID))
		}
		var toast int64
		if err := c.QueryRowContext(p.Ctx, "SELECT reltoastrelid FROM pg_class WHERE oid=$1", before.ID).Scan(&toast); err != nil { t.Fatal(err) }
		if toast > 0 {
			for _, tc := range []struct{ view string; count int64 }{{"all", 1}, {"sys", 1}, {"user", 0}} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM pg_stat_"+tc.view+"_tables WHERE relid=$1", []string{"count"}, []string{"INT8"}, [][]any{{tc.count}}, toast))
			}
		}
		fixture.StatisticsExec(t, p, b, c, "ALTER TABLE sut_members SET SCHEMA information_schema", 0)
		if _, err := f.read(p.Ctx, c, "sut_members"); err != sql.ErrNoRows { t.Fatal("system move retained USER", err) }
		fixture.StatisticsExec(t, p, b, c, "ALTER TABLE information_schema.sut_members SET SCHEMA public", 0)
		if got := f.get(t, p, c, "sut_members"); !got.equal(before) { t.Fatal("move changed identity/history", before, got) }
	})
}
