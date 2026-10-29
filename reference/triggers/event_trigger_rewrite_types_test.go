package reference_test

import (
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// These expectations follow REL_18_6 ATColumnChangeRequiresRewrite,
// varchar_support, numeric_support and TimestampTimestampTzRequiresRewrite.
// The public callbacks, converted values and installed types are all witnesses;
// a native representation copy alone does not imply a PostgreSQL table rewrite.
// Catalog typmods encode varchar length + 4 and numeric precision<<16 | scale,
// plus 4; unmodified integer, domain and timestamp attributes use -1.
func TestReferenceEventTriggerRewriteTypes(t *testing.T) {
	wall := func(year, hour, minute, second int) time.Time {
		return time.Date(year, time.January, 1, hour, minute, second, 0, time.UTC)
	}
	integers := [][]any{{int64(1)}, {int64(7)}}
	text := [][]any{{"xy"}, {"abcd"}}
	decimals := [][]any{{"1.25"}, {"7.75"}}
	for _, tc := range []struct {
		name, source, target, values, typ, installed, zone string
		rows                                               [][]any
		typmod                                             int64
		rewrite                                            bool
	}{
		{
			name: "varchar_widen", source: "varchar(4)", target: "varchar(8)",
			values: "(1,'xy'),(2,'abcd')", typ: "VARCHAR", installed: "varchar",
			rows: text, typmod: 12,
		},
		{
			name: "varchar_narrow", source: "varchar(8)", target: "varchar(4)",
			values: "(1,'xy'),(2,'abcd')", typ: "VARCHAR", installed: "varchar",
			rows: text, typmod: 8, rewrite: true,
		},
		{
			name: "numeric_precision_widen", source: "numeric(4,2)", target: "numeric(6,2)",
			values: "(1,1.25),(2,7.75)", typ: "NUMERIC", installed: "numeric",
			rows: decimals, typmod: 393222,
		},
		{
			name: "numeric_scale_change", source: "numeric(4,2)", target: "numeric(6,1)",
			values: "(1,1.25),(2,7.75)", typ: "NUMERIC", installed: "numeric",
			rows: [][]any{{"1.3"}, {"7.8"}}, typmod: 393221, rewrite: true,
		},
		{
			name: "domain_unconstrained_add", source: "integer", target: "et_rewrite_plain",
			values: "(1,1),(2,7)", typ: "INT4", installed: "et_rewrite_plain", rows: integers, typmod: -1,
		},
		{
			name: "domain_unconstrained_remove", source: "et_rewrite_plain", target: "integer",
			values: "(1,1),(2,7)", typ: "INT4", installed: "int4", rows: integers, typmod: -1,
		},
		{
			name: "domain_constrained_add", source: "integer", target: "et_rewrite_positive",
			values: "(1,1),(2,7)", typ: "INT4", installed: "et_rewrite_positive",
			rows: integers, typmod: -1, rewrite: true,
		},
		{
			name: "domain_constrained_remove", source: "et_rewrite_positive", target: "integer",
			values: "(1,1),(2,7)", typ: "INT4", installed: "int4", rows: integers, typmod: -1,
		},
		{
			name: "domain_constrained_to_unconstrained", source: "et_rewrite_positive", target: "et_rewrite_plain",
			values: "(1,1),(2,7)", typ: "INT4", installed: "et_rewrite_plain", rows: integers, typmod: -1,
		},
		{
			name: "utc_timestamp_to_timestamptz", source: "timestamp", target: "timestamptz", zone: "UTC",
			values: "(1,'1900-01-01 12:00:00'),(2,'2000-01-01 12:00:00')",
			typ:    "TIMESTAMPTZ", installed: "timestamptz", typmod: -1,
			rows: [][]any{{wall(1900, 12, 0, 0)}, {wall(2000, 12, 0, 0)}},
		},
		{
			name: "utc_timestamptz_to_timestamp", source: "timestamptz", target: "timestamp", zone: "UTC",
			values: "(1,'1900-01-01 12:00:00+00'),(2,'2000-01-01 12:00:00+00')",
			typ:    "TIMESTAMP", installed: "timestamp", typmod: -1,
			rows: [][]any{{wall(1900, 12, 0, 0)}, {wall(2000, 12, 0, 0)}},
		},
		// Abidjan is GMT now, but used -00:16:08 before 1912. Both rows
		// participate in one rewrite even though the 2000 value is unchanged.
		{
			name: "historical_timestamp_to_timestamptz", source: "timestamp", target: "timestamptz", zone: "Africa/Abidjan",
			values: "(1,'1900-01-01 12:00:00'),(2,'2000-01-01 12:00:00')",
			typ:    "TIMESTAMPTZ", installed: "timestamptz", typmod: -1, rewrite: true,
			rows: [][]any{{wall(1900, 12, 16, 8)}, {wall(2000, 12, 0, 0)}},
		},
		{
			name: "historical_timestamptz_to_timestamp", source: "timestamptz", target: "timestamp", zone: "Africa/Abidjan",
			values: "(1,'1900-01-01 12:00:00+00'),(2,'2000-01-01 12:00:00+00')",
			typ:    "TIMESTAMP", installed: "timestamp", typmod: -1, rewrite: true,
			rows: [][]any{{wall(1900, 11, 43, 52)}, {wall(2000, 12, 0, 0)}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, route := range []struct {
				name     string
				prepared bool
			}{{"direct", false}, {"prepared", true}} {
				t.Run(route.name, func(t *testing.T) {
					eventTriggerScenario(t, eventTriggerPhaseNames, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
						zone := tc.zone
						if zone == "" {
							zone = "UTC"
						}
						run(fixture.Exec("SET TIME ZONE '"+zone+"'", 0))
						for _, q := range []string{
							"CREATE DOMAIN et_rewrite_plain AS integer",
							"CREATE DOMAIN et_rewrite_positive AS integer CHECK (VALUE>0)",
							"CREATE TABLE et_rewrite_types(id integer,v " + tc.source + ")",
						} {
							run(fixture.Exec(q, 0))
						}
						run(fixture.Exec("INSERT INTO et_rewrite_types VALUES"+tc.values, 2))
						eventTriggerPhases(t, p, b, c, "'ALTER TABLE'")
						run(fixture.Exec("CREATE TABLE et_rewrite_pair(target oid,reason integer)", 0))
						eventTriggerFunction(t, p, b, c, "et_phase_3", 3, "INSERT INTO et_rewrite_pair VALUES(pg_event_trigger_table_rewrite_oid(),pg_event_trigger_table_rewrite_reason()); RETURN;")
						step := fixture.Exec("ALTER TABLE et_rewrite_types ALTER COLUMN v TYPE "+tc.target, 0)
						step.Prepared = route.prepared
						run(step)
						audit := [][]any{eventTriggerRow(1, "et_phase_1", "ddl_command_start", "ALTER TABLE")}
						var count int64
						if tc.rewrite {
							count = 1
							audit = append(audit, eventTriggerRow(3, "et_phase_3", "table_rewrite", "ALTER TABLE"))
						}
						audit = append(audit, eventTriggerRow(4, "et_phase_4", "ddl_command_end", "ALTER TABLE"))
						run(eventTriggerAudit(audit...))
						run(eventTriggerCount("SELECT count(*) AS n FROM et_audit WHERE event='table_rewrite'", count))
						pairs := [][]any{}
						if tc.rewrite {
							pairs = [][]any{{true, int64(4)}}
						}
						run(fixture.Select("SELECT target='et_rewrite_types'::regclass AS correct_target,reason FROM et_rewrite_pair", []string{"correct_target", "reason"}, []string{"BOOL", "INT4"}, pairs))
						run(fixture.Select("SELECT v FROM et_rewrite_types ORDER BY id", []string{"v"}, []string{tc.typ}, tc.rows))
						// Driver domain metadata exposes the base type. The installed
						// descriptor independently distinguishes the domain and typmod.
						kind := "b"
						var base any
						if tc.installed == "et_rewrite_plain" || tc.installed == "et_rewrite_positive" {
							kind, base = "d", "int4"
						}
						run(fixture.Select("SELECT t.typname,t.typtype,a.atttypmod,base.typname AS base_type FROM pg_attribute a JOIN pg_type t ON t.oid=a.atttypid LEFT JOIN pg_type base ON base.oid=t.typbasetype WHERE a.attrelid='et_rewrite_types'::regclass AND a.attname='v'", []string{"typname", "typtype", "atttypmod", "base_type"}, []string{"NAME", "CHAR", "INT4", "NAME"}, [][]any{{tc.installed, kind, tc.typmod, base}}))
					})
				})
			}
		})
	}
}
