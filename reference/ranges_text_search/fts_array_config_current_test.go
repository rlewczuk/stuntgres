package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSArrayConfigCurrent(t *testing.T) {
	t.Run("builtin-live-held", func(t *testing.T) {
		ftsArrayConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			englishOID := ftsArrayConfigOID(t, p, c, "pg_catalog", "english")
			h := textSearchSettingHold(t, p, c, ftsArrayConfigCurrent("simple", 3748).Query)
			defer h.Statement.Close()
			for _, tc := range []struct {
				name string
				oid  int64
			}{{"simple", 3748}, {"english", englishOID}, {"simple", 3748}} {
				ftsArrayConfigRun(t, p, b, c, fixture.Exec("SET default_text_search_config='"+tc.name+"'", 0))
				s := ftsArrayConfigCurrent(tc.name, tc.oid)
				ftsArrayConfigBoth(t, p, b, c, s)
				ftsArrayConfigRun(t, p, b, h, s)
			}
			d := fixture.ControlsReferenceDiagnostic("22023", `invalid value for parameter "default_text_search_config": "array_config_missing"`)
			ftsArrayConfigErrorRoutes(t, p, b, c, fixture.ErrorDiagnostic("SET default_text_search_config='array_config_missing'", d), false)
			ftsArrayConfigRun(t, p, b, h, ftsArrayConfigCurrent("simple", 3748))
		})
	})
	t.Run("qualified-custom-identity", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "array_cfg_first")
		fixture.SchemaReferenceCleanup(t, "array_cfg_other")
		fixture.SchemaReferenceCleanup(t, "Array.Config")
		ftsArrayConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { ftsArrayConfigRun(t, p, b, c, s) }
			defer c.ExecContext(p.Ctx, "RESET search_path")
			run(fixture.Exec("CREATE SCHEMA array_cfg_first", 0))
			run(fixture.Exec("CREATE SCHEMA array_cfg_other", 0))
			run(fixture.Exec(`CREATE SCHEMA "Array.Config"`, 0))
			run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION array_cfg_first.cfg (COPY=pg_catalog.simple)", 0))
			run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION array_cfg_other.cfg (COPY=pg_catalog.english)", 0))
			run(fixture.Exec(`CREATE TEXT SEARCH CONFIGURATION "Array.Config"."Quoted.Cfg" (COPY=pg_catalog.simple)`, 0))
			first := ftsArrayConfigOID(t, p, c, "array_cfg_first", "cfg")
			other := ftsArrayConfigOID(t, p, c, "array_cfg_other", "cfg")
			if first == other {
				t.Fatal("different configuration descriptors share identity")
			}
			h := textSearchSettingHold(t, p, c, ftsArrayConfigCurrent("", 0).Query)
			defer h.Statement.Close()
			run(fixture.Exec("SET search_path=array_cfg_first,pg_catalog", 0))
			run(fixture.Exec("SET default_text_search_config='cfg'", 0))
			check := func(name string, oid int64) {
				s := ftsArrayConfigCurrent(name, oid)
				ftsArrayConfigBoth(t, p, b, c, s)
				ftsArrayConfigRun(t, p, b, h, s)
			}
			check("cfg", first)
			run(fixture.Exec("SET search_path=array_cfg_other,pg_catalog", 0))
			check("array_cfg_first.cfg", first)
			run(fixture.Select("SELECT get_current_ts_config()::oid='array_cfg_first.cfg'::regconfig::oid AS same", []string{"same"}, []string{"BOOL"}, [][]any{{true}}))
			run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION array_cfg_first.cfg ALTER MAPPING FOR asciiword WITH pg_catalog.english_stem", 0))
			check("array_cfg_first.cfg", first)
			run(fixture.Exec("SET default_text_search_config='cfg'", 0))
			check("cfg", other)
			run(fixture.Exec(`SET default_text_search_config='"Array.Config"."Quoted.Cfg"'`, 0))
			quoted := ftsArrayConfigOID(t, p, c, `"Array.Config"`, "Quoted.Cfg")
			check(`"Array.Config"."Quoted.Cfg"`, quoted)
			run(fixture.Exec("SET search_path=public,pg_catalog", 0))
			check(`"Array.Config"."Quoted.Cfg"`, quoted)
		})
	})
	for _, mutation := range []string{"drop", "rename", "move", "schema"} {
		t.Run("missing/"+mutation, func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "array_cfg_schema")
			fixture.SchemaReferenceCleanup(t, "array_cfg_moved")
			ftsArrayConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { ftsArrayConfigRun(t, p, b, c, s) }
				run(fixture.Exec("CREATE SCHEMA array_cfg_schema", 0))
				run(fixture.Exec("CREATE SCHEMA array_cfg_moved", 0))
				run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION array_cfg_schema.cfg (COPY=pg_catalog.simple)", 0))
				old := ftsArrayConfigOID(t, p, c, "array_cfg_schema", "cfg")
				run(fixture.Exec("SET default_text_search_config='array_cfg_schema.cfg'", 0))
				h := textSearchSettingHold(t, p, c, ftsArrayConfigCurrent("array_cfg_schema.cfg", old).Query)
				defer h.Statement.Close()
				ftsArrayConfigRun(t, p, b, h, ftsArrayConfigCurrent("array_cfg_schema.cfg", old))
				run(fixture.Exec(map[string]string{
					"drop":   "DROP TEXT SEARCH CONFIGURATION array_cfg_schema.cfg",
					"rename": "ALTER TEXT SEARCH CONFIGURATION array_cfg_schema.cfg RENAME TO renamed",
					"move":   "ALTER TEXT SEARCH CONFIGURATION array_cfg_schema.cfg SET SCHEMA array_cfg_moved",
					"schema": "DROP SCHEMA array_cfg_schema CASCADE",
				}[mutation], 0))
				d := fixture.ControlsReferenceDiagnostic("42704", `text search configuration "array_cfg_schema.cfg" does not exist`)
				if mutation == "schema" {
					d = fixture.ControlsReferenceDiagnostic("3F000", `schema "array_cfg_schema" does not exist`)
				}
				s := fixture.ErrorDiagnostic(h.SQL, d)
				ftsArrayConfigErrorRoutes(t, p, b, c, s, false)
				for _, query := range []bool{false, true} {
					s.SelectRows, s.ErrorStage = query, "execute"
					ftsArrayConfigRun(t, p, b, h, s)
				}
				if mutation == "schema" {
					run(fixture.Exec("CREATE SCHEMA array_cfg_schema", 0))
				}
				run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION array_cfg_schema.cfg (COPY=pg_catalog.english)", 0))
				replacement := ftsArrayConfigOID(t, p, c, "array_cfg_schema", "cfg")
				if replacement == old {
					t.Fatal("recreated object reused old descriptor identity")
				}
				ftsArrayConfigBoth(t, p, b, c, ftsArrayConfigCurrent("array_cfg_schema.cfg", replacement))
				ftsArrayConfigRun(t, p, b, h, ftsArrayConfigCurrent("array_cfg_schema.cfg", replacement))
			})
		})
	}
}

func TestReferenceFTSArrayConfigSession(t *testing.T) {
	ftsArrayConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a *sql.Conn) {
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		defer ftsConvertRestore(t, p, peer)()
		run := func(s fixture.Step) { ftsArrayConfigRun(t, p, b, a, s) }
		englishOID := ftsArrayConfigOID(t, p, a, "pg_catalog", "english")
		var baseline string
		if err := a.QueryRowContext(p.Ctx, "SHOW default_text_search_config").Scan(&baseline); err != nil {
			t.Fatal(err)
		}
		if baseline != "pg_catalog.simple" && baseline != "pg_catalog.english" {
			t.Fatal("unaccounted fixture baseline", baseline)
		}
		run(fixture.Exec("SET default_text_search_config='simple'", 0))
		ftsArrayConfigRun(t, p, b, peer, fixture.Exec("SET default_text_search_config='english'", 0))
		h := textSearchSettingHold(t, p, a, ftsArrayConfigCurrent("", 0).Query)
		defer h.Statement.Close()
		ph := textSearchSettingHold(t, p, peer, h.SQL)
		defer ph.Statement.Close()
		check := func(english bool) {
			s := ftsArrayConfigCurrent("simple", 3748)
			if english {
				s = ftsArrayConfigCurrent("english", englishOID)
			}
			ftsArrayConfigBoth(t, p, b, a, s)
			ftsArrayConfigRun(t, p, b, h, s)
			ftsArrayConfigBoth(t, p, b, peer, ftsArrayConfigCurrent("english", englishOID))
			ftsArrayConfigRun(t, p, b, ph, ftsArrayConfigCurrent("english", englishOID))
		}
		check(false)
		for _, end := range []string{"COMMIT", "ROLLBACK"} {
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SET LOCAL default_text_search_config='english'", 0))
			check(true)
			run(fixture.Exec(end, 0))
			check(false)
		}
		for _, local := range []string{"", "LOCAL "} {
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SET LOCAL default_text_search_config='english'", 0))
			run(fixture.Exec("SAVEPOINT array_cfg_selection", 0))
			run(fixture.Exec("SET "+local+"default_text_search_config='simple'", 0))
			check(false)
			run(fixture.Exec("ROLLBACK TO SAVEPOINT array_cfg_selection", 0))
			check(true)
			run(fixture.Exec("ROLLBACK", 0))
			check(false)
		}
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Select("SELECT set_config('default_text_search_config','english',true) AS setting", []string{"setting"}, []string{"TEXT"}, [][]any{{"pg_catalog.english"}}))
		check(true)
		run(fixture.Exec("COMMIT", 0))
		check(false)
		run(fixture.Select("SELECT set_config('default_text_search_config','english',false) AS setting", []string{"setting"}, []string{"TEXT"}, [][]any{{"pg_catalog.english"}}))
		check(true)
		run(fixture.Exec("RESET default_text_search_config", 0))
		check(baseline == "pg_catalog.english")
		run(textSearchSettingShow(baseline))
	})
}
