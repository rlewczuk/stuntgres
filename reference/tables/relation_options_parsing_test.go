package reference_test

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRelationOptionsParsing(t *testing.T) {
	relationOptionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE ro(id INT,payload TEXT)", 0))
		for _, tc := range []struct{ sql, stored string }{
			{"autovacuum_enabled", "autovacuum_enabled=true"},
			{"FILLFACTOR=70", "fillfactor=70"},
			{"fillfactor='070'", "fillfactor=070"},
			{"fillfactor='0x46'", "fillfactor=0x46"},
			{"fillfactor='69.5'", "fillfactor=69.5"},
			{"fillfactor='70.5'", "fillfactor=70.5"},
			{"fillfactor='7e1'", "fillfactor=7e1"},
			{"fillfactor=70.0", "fillfactor=70.0"},
			{"parallel_workers='0.5'", "parallel_workers=0.5"},
			{"parallel_workers='1.5'", "parallel_workers=1.5"},
			{"parallel_workers='2.5'", "parallel_workers=2.5"},
			{"autovacuum_vacuum_scale_factor=E'\\t0.250\\n'", "autovacuum_vacuum_scale_factor=\t0.250\n"},
			{"fillfactor=+70", "fillfactor=70"},
			{"parallel_workers=-0", "parallel_workers=0"},
			{"autovacuum_vacuum_scale_factor='0.250'", "autovacuum_vacuum_scale_factor=0.250"},
			{"autovacuum_vacuum_scale_factor='-0'", "autovacuum_vacuum_scale_factor=-0"},
			{"fillfactor=' 70 '", "fillfactor= 70 "},
		} {
			t.Run(tc.sql, func(t *testing.T) {
				x := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				name := strings.SplitN(tc.stored, "=", 2)[0]
				defer c.ExecContext(p.Ctx, "ALTER TABLE ro RESET("+name+")")
				x(fixture.Exec("ALTER TABLE ro SET("+tc.sql+")", 0))
				x(relationOptionsState("ro", tc.stored))
				x(fixture.Exec("ALTER TABLE ro RESET("+name+")", 0))
			})
		}
		for _, value := range []string{"t", "tr", "TRUE", "f", "fa", "FALSE", "y", "ye", "n", "of", "ON", "OFF"} {
			run(fixture.Exec("ALTER TABLE ro SET(autovacuum_enabled='"+value+"')", 0))
			run(relationOptionsState("ro", "autovacuum_enabled="+value))
		}
		run(fixture.Exec("ALTER TABLE ro RESET(autovacuum_enabled)", 0))
		for _, tc := range []struct{ sql, message, detail string }{
			{"bogus=1", `unrecognized parameter "bogus"`, ""},
			{"toast.autovacuum_enabled='bad',bogus=1", `unrecognized parameter "bogus"`, ""},
			{"fillfactor=9,toast.autovacuum_enabled='bad'", `value 9 out of bounds for option "fillfactor"`, `Valid values are between "10" and "100".`},
			{"bogus.fillfactor=70", `unrecognized parameter namespace "bogus"`, ""},
			{`"Fillfactor"=70`, `unrecognized parameter "Fillfactor"`, ""},
			{`"a""b"=1`, `unrecognized parameter "a"b"`, ""},
			{`"a=b"=1`, `invalid option name "a=b": must not contain "="`, ""},
			{"fillfactor=70,fillfactor=80", `parameter "fillfactor" specified more than once`, ""},
			{"toast.autovacuum_enabled=true,toast.autovacuum_enabled=false", `parameter "autovacuum_enabled" specified more than once`, ""},
			{"fillfactor", `invalid value for integer option "fillfactor": true`, ""},
			{"autovacuum_enabled='o'", `invalid value for boolean option "autovacuum_enabled": o`, ""},
			{"autovacuum_enabled='maybe'", `invalid value for boolean option "autovacuum_enabled": maybe`, ""},
			{"vacuum_index_cleanup='t'", `invalid value for enum option "vacuum_index_cleanup": t`, `Valid values are "on", "off", and "auto".`},
		} {
			t.Run(tc.sql, func(t *testing.T) {
				s := relationOptionsError("ALTER TABLE ro SET("+tc.sql+")", "22023", tc.message, tc.detail)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, relationOptionsState("ro"))
			})
		}
		for _, name := range []string{"toast.fillfactor", "toast.parallel_workers", "toast.toast_tuple_target", "toast.user_catalog_table", "toast.autovacuum_analyze_threshold", "toast.autovacuum_analyze_scale_factor"} {
			bare := strings.TrimPrefix(name, "toast.")
			run(relationOptionsError("ALTER TABLE ro SET("+name+"=70)", "22023", fmt.Sprintf("unrecognized parameter %q", bare), ""))
		}
		for _, value := range []string{"abc", "080", "10ms", "1kB", "2147483648", "-2147483649", "NaN", "1e1000", "1e-1000"} {
			run(relationOptionsError("ALTER TABLE ro SET(fillfactor='"+value+"')", "22023", `invalid value for integer option "fillfactor": `+value, ""))
			run(relationOptionsState("ro"))
		}
		for _, value := range []string{"abc", "NaN", "1e1000", "1e-1000", "10ms"} {
			run(relationOptionsError("ALTER TABLE ro SET(autovacuum_vacuum_scale_factor='"+value+"')", "22023", `invalid value for floating point option "autovacuum_vacuum_scale_factor": `+value, ""))
		}
		for _, value := range []string{"Infinity", "-Infinity"} {
			run(relationOptionsError("ALTER TABLE ro SET(autovacuum_vacuum_scale_factor='"+value+"')", "22023", `value `+value+` out of bounds for option "autovacuum_vacuum_scale_factor"`, `Valid values are between "0.000000" and "100.000000".`))
		}
		run(fixture.Exec("ALTER TABLE ro SET(autovacuum_enabled=true,toast.autovacuum_enabled=false)", 0))
		run(relationOptionsState("ro", "autovacuum_enabled=true"))
		relationOptionsToastState(t, p, b, c, prepared, "ro", "autovacuum_enabled=false")
		run(fixture.Exec("ALTER TABLE ro RESET(autovacuum_enabled,autovacuum_enabled,bogus,bogus.fillfactor,toast.autovacuum_enabled)", 0))
		run(relationOptionsState("ro"))
		run(relationOptionsError("ALTER TABLE ro RESET(fillfactor=70)", "42601", "RESET must not include values for parameters", ""))
		for _, tc := range []struct{ query, token string }{
			{"ALTER TABLE ro SET ()", ")"},
			{"ALTER TABLE IF EXISTS ro_missing SET ()", ")"},
			{"ALTER TABLE ro RESET ()", ")"},
			{"ALTER TABLE ro SET (fillfactor=$1)", "$1"},
			{"ALTER TABLE ro SET (fillfactor=70+1)", "+"},
			{"ALTER TABLE ro SET (fillfactor=)", ")"},
			{"ALTER TABLE ro SET (fillfactor=70) trailing", "trailing"},
		} {
			t.Run(tc.query, func(t *testing.T) {
				d := relationOptionsDiagnostic("42601", `syntax error at or near "`+tc.token+`"`, "")
				d.Position = strconv.Itoa(strings.Index(tc.query, tc.token) + 1)
				s := fixture.ErrorDiagnostic(tc.query, d)
				s.Prepared = prepared
				if prepared {
					s.ErrorStage = "prepare"
				} else {
					s.ErrorStage = "execute"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			})
		}
	})
}

func TestReferenceRelationOptionsCatalogOrder(t *testing.T) {
	relationOptionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE ro(id INT,payload TEXT)", 0))
		run(relationOptionsState("ro"))
		run(fixture.Exec("ALTER TABLE ro SET(fillfactor=70,autovacuum_enabled='OFF')", 0))
		run(relationOptionsState("ro", "fillfactor=70", "autovacuum_enabled=OFF"))
		run(fixture.Exec("ALTER TABLE ro SET(fillfactor='80')", 0))
		run(relationOptionsState("ro", "autovacuum_enabled=OFF", "fillfactor=80"))
		run(fixture.Exec("ALTER TABLE ro RESET(autovacuum_enabled)", 0))
		run(relationOptionsState("ro", "fillfactor=80"))
		run(fixture.Exec("ALTER TABLE ro RESET(fillfactor)", 0))
		run(relationOptionsState("ro"))
		run(fixture.Exec("ALTER TABLE ro SET(fillfactor=70),SET(fillfactor=80),SET(autovacuum_vacuum_scale_factor='0.250')", 0))
		run(relationOptionsState("ro", "fillfactor=80", "autovacuum_vacuum_scale_factor=0.250"))
	})
}

func TestReferenceRelationOptionsToastBoundary(t *testing.T) {
	relationOptionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE ro(id INT,payload TEXT)", 0))
		run(fixture.Exec("ALTER TABLE ro SET(fillfactor=70,toast.autovacuum_enabled=false,toast.vacuum_truncate='OFF')", 0))
		run(relationOptionsState("ro", "fillfactor=70"))
		relationOptionsToastState(t, p, b, c, prepared, "ro", "autovacuum_enabled=false", "vacuum_truncate=OFF")
		run(fixture.Exec("ALTER TABLE ro RESET(toast.autovacuum_enabled)", 0))
		run(relationOptionsState("ro", "fillfactor=70"))
		relationOptionsToastState(t, p, b, c, prepared, "ro", "vacuum_truncate=OFF")
		run(fixture.Exec("CREATE TABLE ro_integer(id INT)", 0))
		run(fixture.Select("SELECT reltoastrelid=0 AS absent FROM pg_class WHERE oid='ro_integer'::regclass", []string{"absent"}, []string{"BOOL"}, [][]any{{true}}))
		// PostgreSQL has no physical TOAST validator pass for this table. Native
		// deliberately validates retained logical intent instead of inventing TOAST.
		if b.PG {
			run(fixture.Exec("ALTER TABLE ro_integer SET(toast.autovacuum_enabled='bad')", 0))
		} else {
			run(relationOptionsError("ALTER TABLE ro_integer SET(toast.autovacuum_enabled='bad')", "22023", `invalid value for boolean option "autovacuum_enabled": bad`, ""))
		}
		run(relationOptionsState("ro_integer"))
		run(fixture.Exec("ALTER TABLE ro_integer SET(toast.autovacuum_enabled=false)", 0))
		run(relationOptionsState("ro_integer"))
		if !b.PG {
			dump := relationOptionsDump(t, p)
			if !strings.Contains(dump, "ALTER TABLE \"public\".\"ro_integer\" SET (\"toast\".\"autovacuum_enabled\" = E'false');\n") {
				t.Fatal("retained TOAST intent absent", dump)
			}
		}
	})
}
