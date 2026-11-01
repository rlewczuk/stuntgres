package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgTrgmSettings(t *testing.T) {
	pgTrgmActivationCases(t)
	pgTrgmHistoryCases(t)
	pgTrgmRoutineSettingsCases(t)
	pgTrgmPersistedSettingsCases(t)
	t.Run("MetadataAndLiveHeldThresholds", func(t *testing.T) {
		pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
			run := pgTrgmRun(t, p, b, c)
			run(fixture.Exec("CREATE EXTENSION pg_trgm", 0))
			for _, setting := range pgTrgmSettings {
				q := "SELECT name,setting,unit,category,short_desc,extra_desc,context,vartype,source,min_val,max_val,enumvals,boot_val,reset_val,sourcefile,sourceline,pending_restart FROM pg_settings WHERE name=$1"
				s := fixture.Select(q, []string{"name", "setting", "unit", "category", "short_desc", "extra_desc", "context", "vartype", "source", "min_val", "max_val", "enumvals", "boot_val", "reset_val", "sourcefile", "sourceline", "pending_restart"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "_TEXT", "TEXT", "TEXT", "TEXT", "INT4", "BOOL"}, [][]any{{setting.name, setting.boot, nil, "Customized Options", "Sets the threshold used by the " + setting.op + " operator.", "Valid range is 0.0 .. 1.0.", "user", "real", "default", "0", "1", nil, setting.boot, setting.boot, nil, nil, false}}, setting.name)
				run(s)
				run(fixture.Prepared(s))
				run(fixture.ArrayReferenceSelect("SELECT pg_settings_get_flags($1)", []string{"pg_settings_get_flags"}, []string{"_TEXT"}, []string{"_TEXT"}, [][]any{{"{}"}}, b.PG, setting.name))
			}
			q := "SELECT $1::text % $2::text AS result"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			check := func(want bool) {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, pgTrgmScalar(b, q, "result", "BOOL", want, "a", "aa"))
			}
			check(false)
			run(fixture.Select("SELECT set_config('pg_trgm.similarity_threshold','0.25',false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{"0.25"}}))
			check(true)
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SAVEPOINT s", 0))
			run(fixture.Select("SELECT set_config('pg_trgm.similarity_threshold','0.5',true)", []string{"set_config"}, []string{"TEXT"}, [][]any{{"0.5"}}))
			check(false)
			run(fixture.Exec("ROLLBACK TO s", 0))
			check(true)
			run(fixture.Exec("SET LOCAL pg_trgm.similarity_threshold=1", 0))
			check(false)
			run(fixture.Exec("COMMIT", 0))
			check(true)
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SET pg_trgm.similarity_threshold='0.75'", 0))
			check(false)
			run(fixture.Exec("ROLLBACK", 0))
			check(true)
			run(fixture.Exec("RESET pg_trgm.similarity_threshold", 0))
			check(false)
			pgTrgmNotices(t, notices)
		})
	})
	for _, setting := range pgTrgmSettings {
		t.Run("G8CommittedPromotion/"+setting.name, func(t *testing.T) {
			pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
				pgTrgmInstalledElsewhere(t, p, b)
				run := pgTrgmRun(t, p, b, c)
				run(fixture.Exec("SET "+setting.name+"='0.2'", 0))
				pgTrgmLoaded(run, false)
				run(fixture.Select("SHOW "+setting.name, []string{setting.name}, []string{"TEXT"}, [][]any{{"0.2"}}))
				pgTrgmActivate(run, b)
				pgTrgmSettingValue(run, setting.name, "0.2")
				run(fixture.Select("SELECT setting,reset_val,boot_val,vartype,source FROM pg_settings WHERE name=$1", []string{"setting", "reset_val", "boot_val", "vartype", "source"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"0.2", setting.boot, setting.boot, "real", "session"}}, setting.name))
				run(fixture.Exec("RESET "+setting.name, 0))
				pgTrgmSettingValue(run, setting.name, setting.boot)
				run(fixture.Exec("SET "+setting.name+"=1", 0))
				run(fixture.Exec("SET "+setting.name+"=DEFAULT", 0))
				pgTrgmSettingValue(run, setting.name, setting.boot)
				pgTrgmNotices(t, notices)
			})
		})
	}
	t.Run("G9PlaceholderResetIsNotEmptyAssignment", func(t *testing.T) {
		pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
			pgTrgmInstalledElsewhere(t, p, b)
			run := pgTrgmRun(t, p, b, c)
			run(fixture.Exec("SET pg_trgm.similarity_threshold='bogus'", 0))
			run(fixture.Exec("RESET pg_trgm.similarity_threshold", 0))
			pgTrgmSettingValue(run, pgTrgmSettings[0].name, "")
			pgTrgmActivate(run, b)
			pgTrgmSettingValue(run, pgTrgmSettings[0].name, "0.3")
			pgTrgmNotices(t, notices)
		})
	})
	for _, setting := range pgTrgmSettings {
		for _, value := range []string{"bogus", "2", "NaN", "1kB", ""} {
			t.Run("G10InvalidPromotion/"+setting.name+"/"+value, func(t *testing.T) {
				pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
					pgTrgmInstalledElsewhere(t, p, b)
					run := pgTrgmRun(t, p, b, c)
					run(fixture.Exec("SET "+setting.name+"="+pgTrgmLiteral(value), 0))
					pgTrgmActivate(run, b)
					want := pgTrgmBadSetting(setting.name, value)
					if value == "2" {
						want.Message = `2 is outside the valid range for parameter "` + setting.name + `" (0 .. 1)`
					}
					pgTrgmNotices(t, notices, want)
					pgTrgmSettingValue(run, setting.name, setting.boot)
					run(fixture.ImplicitError("SET "+setting.name+"="+pgTrgmLiteral(value), want))
					pgTrgmSettingValue(run, setting.name, setting.boot)
				})
			})
		}
	}
	for _, value := range []string{"0", "1"} {
		t.Run("G10InclusivePromotion/"+value, func(t *testing.T) {
			pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
				pgTrgmInstalledElsewhere(t, p, b)
				run := pgTrgmRun(t, p, b, c)
				for _, setting := range pgTrgmSettings {
					run(fixture.Exec("SET "+setting.name+"="+value, 0))
				}
				pgTrgmActivate(run, b)
				for _, setting := range pgTrgmSettings {
					pgTrgmSettingValue(run, setting.name, value)
				}
				pgTrgmNotices(t, notices)
			})
		})
	}
	t.Run("G11ReservedPrefixSurvivesRollback", func(t *testing.T) {
		pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
			pgTrgmInstalledElsewhere(t, p, b)
			run := pgTrgmRun(t, p, b, c)
			run(fixture.Exec("SET pg_trgm.typo='x'", 0))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SAVEPOINT s", 0))
			pgTrgmActivate(run, b)
			pgTrgmNotices(t, notices, fixture.Diagnostic{Code: "42602", Message: `invalid configuration parameter name "pg_trgm.typo", removing it`, Detail: `"pg_trgm" is now a reserved prefix.`})
			run(fixture.Exec("ROLLBACK TO s", 0))
			run(fixture.Exec("COMMIT", 0))
			pgTrgmSettingValue(run, "pg_trgm.typo", nil)
			run(fixture.ImplicitError("SHOW pg_trgm.typo", fixture.Diagnostic{Code: "42704", Message: `unrecognized configuration parameter "pg_trgm.typo"`}))
			run(fixture.ImplicitError("SET pg_trgm.typo='x'", fixture.Diagnostic{Code: "42602", Message: `invalid configuration parameter name "pg_trgm.typo"`, Detail: `"pg_trgm" is a reserved prefix.`}))
			pgTrgmActivate(run, b)
			pgTrgmNotices(t, notices)
		})
	})
	for _, command := range []string{"RESET pg_trgm.similarity_threshold", "RESET ALL"} {
		for _, end := range []string{"COMMIT", "ROLLBACK"} {
			t.Run("G19/"+command+"/"+end, func(t *testing.T) {
				pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
					run := pgTrgmRun(t, p, b, c)
					run(fixture.Exec("CREATE EXTENSION pg_trgm", 0))
					run(fixture.Exec("SET pg_trgm.similarity_threshold='0.2'", 0))
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec(command, 0))
					pgTrgmSettingValue(run, pgTrgmSettings[0].name, "0.3")
					run(fixture.Exec(end, 0))
					want := "0.3"
					if end == "ROLLBACK" {
						want = "0.2"
					}
					pgTrgmSettingValue(run, pgTrgmSettings[0].name, want)
					pgTrgmLoaded(run, true)
					pgTrgmNotices(t, notices)
				})
			})
		}
	}
}
