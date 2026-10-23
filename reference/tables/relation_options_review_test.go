package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRelationOptionsReviewMaterialized(t *testing.T) {
	relationOptionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE ro_mv_base(id INT,payload TEXT)", 0))
		run(fixture.Exec("INSERT INTO ro_mv_base VALUES(1,'sentinel')", 1))
		run(fixture.Exec("CREATE MATERIALIZED VIEW ro_mv_before AS SELECT id,payload FROM ro_mv_base", 1))
		run(fixture.Exec("CREATE MATERIALIZED VIEW ro_mv_no AS SELECT id,payload FROM ro_mv_base WITH NO DATA", 0))
		run(fixture.Exec("CREATE VIEW ro_history AS SELECT id,payload FROM ro_mv_before", 0))
		for _, name := range []string{"ro_mv_before", "ro_mv_no"} {
			run(relationOptionsState(name))
			run(fixture.Exec("ALTER TABLE "+name+" SET(fillfactor=70,autovacuum_enabled='OFF',toast.autovacuum_enabled=false,toast.vacuum_truncate='OFF')", 0))
			run(relationOptionsState(name, "fillfactor=70", "autovacuum_enabled=OFF"))
			relationOptionsToastState(t, p, b, c, prepared, name, "autovacuum_enabled=false", "vacuum_truncate=OFF")
			run(fixture.Exec("ALTER TABLE "+name+" RESET(fillfactor,toast.autovacuum_enabled)", 0))
			run(relationOptionsState(name, "autovacuum_enabled=OFF"))
			relationOptionsToastState(t, p, b, c, prepared, name, "vacuum_truncate=OFF")
			run(fixture.Exec("ALTER TABLE "+name+" RESET(autovacuum_enabled,toast.vacuum_truncate)", 0))
			run(relationOptionsState(name))
			relationOptionsToastState(t, p, b, c, prepared, name)
			run(fixture.Exec("ALTER TABLE "+name+" SET(fillfactor='070',autovacuum_vacuum_scale_factor='0.250',toast.vacuum_truncate='OFF',toast.autovacuum_enabled=false)", 0))
		}
		// Retained options belong to the actual renamed descriptor. The
		// ordinary view keeps its earlier analyzed source identity as well.
		run(fixture.Exec("ALTER MATERIALIZED VIEW ro_mv_before RENAME TO ro_mv", 0))
		run(fixture.Exec("ALTER VIEW ro_history RENAME TO ro_history_saved", 0))
		// A native snapshot must retain stale contents, not rerun its source query.
		run(fixture.Exec("UPDATE ro_mv_base SET payload='changed' WHERE id=1", 1))
		check := func() {
			for _, name := range []string{"ro_mv", "ro_mv_no"} {
				run(relationOptionsState(name, "fillfactor=070", "autovacuum_vacuum_scale_factor=0.250"))
				relationOptionsToastState(t, p, b, c, prepared, name, "vacuum_truncate=OFF", "autovacuum_enabled=false")
				run(relationOptionsReviewMaterializedDefinition(name))
			}
			run(relationOptionsRows("ro_mv"))
			run(relationOptionsRows("ro_history_saved"))
			fixture.MaterializedViewPopulation(run, "ro_mv", true)
			fixture.MaterializedViewPopulation(run, "ro_mv_no", false)
			run(fixture.MaterializedViewUnpopulated("ro_mv_no"))
		}
		check()
		if b.PG {
			return // Dump/Reset/Restore are native controls, not PostgreSQL APIs.
		}
		before := relationOptionsDump(t, p)
		if strings.Count(before, " SET (") != 2 {
			t.Fatalf("expected exactly one option statement per materialized view:\n%s", before)
		}
		for _, name := range []string{"ro_mv", "ro_mv_no"} {
			line := `ALTER TABLE "public"."` + name + `" SET ("fillfactor" = E'070', "autovacuum_vacuum_scale_factor" = E'0.250', "toast"."vacuum_truncate" = E'OFF', "toast"."autovacuum_enabled" = E'false');` + "\n"
			if strings.Count(before, line) != 1 {
				t.Fatalf("missing or repeated exact materialized option SQL %q in dump:\n%s", line, before)
			}
			create := strings.Index(before, `CREATE MATERIALIZED VIEW "public"."`+name+`"`)
			option := strings.Index(before, line)
			if create < 0 || create >= option || !strings.HasPrefix(before, "SET SESSION AUTHORIZATION 'postgres';\n") {
				t.Fatalf("materialized options not emitted after CREATE in owner context:\n%s", before)
			}
		}
		for range 2 {
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(before)); err != nil {
				t.Fatal(err)
			}
			check()
			if got := relationOptionsDump(t, p); got != before {
				t.Fatalf("materialized replay not byte-stable\ngot: %q\nwant: %q", got, before)
			}
		}
		if p.Postgres == nil {
			return
		}
		// Replay into a clean PG destination and clean it again before the
		// ordinary PG scenario starts, so neither backend inherits this setup.
		resetPG := func() {
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal("materialized emitted replay cleanup", err)
			}
		}
		resetPG()
		defer resetPG()
		if _, err := p.Postgres.ExecContext(p.Ctx, before); err != nil {
			t.Fatal("PostgreSQL materialized emitted SQL replay", err)
		}
		pg, err := p.Postgres.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer pg.Close()
		pgRun := func(s fixture.Step) {
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, pg, p.PostgresRecord, s)
		}
		for _, name := range []string{"ro_mv", "ro_mv_no"} {
			pgRun(relationOptionsState(name, "fillfactor=070", "autovacuum_vacuum_scale_factor=0.250"))
			pgRun(relationOptionsReviewMaterializedDefinition(name))
			relationOptionsToastState(t, p, fixture.Backend{PG: true, Recorder: p.PostgresRecord}, pg, prepared, name, "vacuum_truncate=OFF", "autovacuum_enabled=false")
			// Native comment snapshots are intentionally not a PG import API.
			fixture.MaterializedViewPopulation(pgRun, name, false)
			pgRun(fixture.MaterializedViewUnpopulated(name))
			pgRun(fixture.Exec("REFRESH MATERIALIZED VIEW "+name, 0))
			fixture.MaterializedViewPopulation(pgRun, name, true)
			pgRun(fixture.Select("SELECT id,payload FROM "+name+" ORDER BY id", []string{"id", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "changed"}}))
			pgRun(relationOptionsState(name, "fillfactor=070", "autovacuum_vacuum_scale_factor=0.250"))
		}
		pgRun(fixture.Select("SELECT id,payload FROM ro_history_saved ORDER BY id", []string{"id", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "changed"}}))
	})
}

func TestReferenceRelationOptionsReviewDiagnostics(t *testing.T) {
	relationOptionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		relationOptionsSeed(run)
		run(fixture.Exec("ALTER TABLE ro SET(toast.autovacuum_enabled=false)", 0))
		var before string
		if !b.PG {
			before = relationOptionsDump(t, p)
		}
		// Numeric parsing/rounding determines validity; the input text, not
		// the parsed number, determines the out-of-bounds primary message.
		for _, tc := range []struct{ option, text, detail string }{
			{"fillfactor", "9.4", `Valid values are between "10" and "100".`},
			{"fillfactor", "100.6", `Valid values are between "10" and "100".`},
			{"fillfactor", "0x09", `Valid values are between "10" and "100".`},
			{"fillfactor", "9e0", `Valid values are between "10" and "100".`},
			{"fillfactor", " 9 ", `Valid values are between "10" and "100".`},
			{"autovacuum_vacuum_scale_factor", "101.000", `Valid values are between "0.000000" and "100.000000".`},
			{"autovacuum_vacuum_scale_factor", "1.01e2", `Valid values are between "0.000000" and "100.000000".`},
			{"autovacuum_vacuum_scale_factor", " 101.000 ", `Valid values are between "0.000000" and "100.000000".`},
		} {
			t.Run(tc.option+"="+tc.text, func(t *testing.T) {
				x := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				x(relationOptionsError("ALTER TABLE ro SET("+tc.option+"='"+tc.text+"')", "22023", fmt.Sprintf("value %s out of bounds for option %q", tc.text, tc.option), tc.detail))
				x(relationOptionsState("ro", "fillfactor=70"))
				relationOptionsToastState(t, p, b, c, prepared, "ro", "autovacuum_enabled=false")
				x(relationOptionsRows("ro"))
				if !b.PG && relationOptionsDump(t, p) != before {
					t.Fatal("bounds rejection changed native dump")
				}
			})
		}
	})
}

func TestReferenceRelationOptionsReviewPrecedence(t *testing.T) {
	relationOptionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		relationOptionsSeed(run)
		run(fixture.Exec("ALTER TABLE ro SET(toast.autovacuum_enabled=false)", 0))
		var before string
		if !b.PG {
			before = relationOptionsDump(t, p)
		}
		for _, tc := range []struct{ options, message, detail string }{
			{`bogus."a=b"=1`, `unrecognized parameter namespace "bogus"`, ""},
			{`bogus."a=b"=1,fillfactor=9`, `unrecognized parameter namespace "bogus"`, ""},
			{`fillfactor=9,bogus."a=b"=1`, `unrecognized parameter namespace "bogus"`, ""},
			{`bogus.x=1,fillfactor=9`, `unrecognized parameter namespace "bogus"`, ""},
			{`fillfactor=9,bogus.x=1`, `unrecognized parameter namespace "bogus"`, ""},
			{`"a=b"=1,fillfactor=9`, `invalid option name "a=b": must not contain "="`, ""},
			{`fillfactor=9,"a=b"=1`, `invalid option name "a=b": must not contain "="`, ""},
			{`fillfactor=9,toast."a=b"=1`, `value 9 out of bounds for option "fillfactor"`, `Valid values are between "10" and "100".`},
			{`toast."a=b"=1,fillfactor=9`, `value 9 out of bounds for option "fillfactor"`, `Valid values are between "10" and "100".`},
			{`fillfactor=70,toast."a=b"=1`, `invalid option name "a=b": must not contain "="`, ""},
			{`toast."a=b"=1,fillfactor=70`, `invalid option name "a=b": must not contain "="`, ""},
			{`fillfactor=80,toast."a=b"=1`, `invalid option name "a=b": must not contain "="`, ""},
			{`toast."a=b"=1,fillfactor=80`, `invalid option name "a=b": must not contain "="`, ""},
		} {
			t.Run(tc.options, func(t *testing.T) {
				x := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				x(relationOptionsError("ALTER TABLE ro SET("+tc.options+")", "22023", tc.message, tc.detail))
				x(relationOptionsState("ro", "fillfactor=70"))
				relationOptionsToastState(t, p, b, c, prepared, "ro", "autovacuum_enabled=false")
				x(relationOptionsRows("ro"))
				if !b.PG && relationOptionsDump(t, p) != before {
					t.Fatal("precedence rejection changed native dump")
				}
			})
		}
	})
}

func relationOptionsReviewMaterializedDefinition(name string) fixture.Step {
	return fixture.Select("SELECT r.rolname=current_user AS owned,pg_get_viewdef(c.oid,true) AS definition FROM pg_class c JOIN pg_roles r ON r.oid=c.relowner WHERE c.oid='"+name+"'::regclass", []string{"owned", "definition"}, []string{"BOOL", "TEXT"}, [][]any{{true, " SELECT id,\n    payload\n   FROM ro_mv_base;"}})
}
