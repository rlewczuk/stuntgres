package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func pgTrgmHistoryCases(t *testing.T) {
	for _, setting := range pgTrgmSettings {
		for _, tc := range []struct {
			name, base            string
			commands              []string
			inside, commit, abort string
			warning               bool
		}{
			{"G12Local", "0.2", []string{"SET LOCAL %s='0.8'"}, "0.8", "0.2", "0.2", false},
			{"G13SetLocal", "0.2", []string{"SET %s='0.4'", "SET LOCAL %s='0.8'"}, "0.8", "0.4", "0.2", false},
			{"G15InvalidBaseValidLocal", "bogus", []string{"SET LOCAL %s='0.8'"}, "0.8", setting.boot, setting.boot, true},
			{"G16InvalidMaskedSet", "0.2", []string{"SET %s='bogus'", "SET LOCAL %s='0.8'"}, "0.8", "0.2", "0.2", true},
			{"G17InvalidLocal", "0.2", []string{"SET LOCAL %s='bogus'"}, "0.2", "0.2", "0.2", true},
		} {
			for _, end := range []string{"COMMIT", "ROLLBACK"} {
				t.Run(tc.name+"/"+setting.name+"/"+end, func(t *testing.T) {
					pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
						pgTrgmInstalledElsewhere(t, p, b)
						run := pgTrgmRun(t, p, b, c)
						run(fixture.Exec("SET "+setting.name+"="+pgTrgmLiteral(tc.base), 0))
						run(fixture.Exec("BEGIN", 0))
						for _, command := range tc.commands {
							run(fixture.Exec(fmt.Sprintf(command, setting.name), 0))
						}
						pgTrgmLoaded(run, false)
						pgTrgmActivate(run, b)
						pgTrgmLoaded(run, true)
						if tc.warning {
							pgTrgmNotices(t, notices, pgTrgmBadSetting(setting.name, "bogus"))
						} else {
							pgTrgmNotices(t, notices)
						}
						pgTrgmSettingValue(run, setting.name, tc.inside)
						run(fixture.Exec(end, 0))
						want := tc.commit
						if end == "ROLLBACK" {
							want = tc.abort
						}
						pgTrgmSettingValue(run, setting.name, want)
						run(fixture.Select("SELECT vartype FROM pg_settings WHERE name=$1", []string{"vartype"}, []string{"TEXT"}, [][]any{{"real"}}, setting.name))
						pgTrgmActivate(run, b)
						pgTrgmNotices(t, notices)
					})
				})
			}
		}
		for _, end := range []string{"ROLLBACK TO s", "RELEASE s"} {
			t.Run("G14SavedImage/"+setting.name+"/"+end, func(t *testing.T) {
				pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
					pgTrgmInstalledElsewhere(t, p, b)
					run := pgTrgmRun(t, p, b, c)
					run(fixture.Exec("SET "+setting.name+"='0.2'", 0))
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("SAVEPOINT s", 0))
					run(fixture.Exec("SET LOCAL "+setting.name+"='0.8'", 0))
					pgTrgmActivate(run, b)
					pgTrgmSettingValue(run, setting.name, "0.8")
					run(fixture.Exec(end, 0))
					want := "0.8"
					if end == "ROLLBACK TO s" {
						want = "0.2"
					}
					pgTrgmSettingValue(run, setting.name, want)
					pgTrgmLoaded(run, true)
					run(fixture.Exec("COMMIT", 0))
					pgTrgmSettingValue(run, setting.name, "0.2")
					// The saved pre-load string must not become a new untyped
					// placeholder or bypass real validation after restoration.
					run(fixture.ImplicitError("SET "+setting.name+"='bogus'", pgTrgmBadSetting(setting.name, "bogus")))
					pgTrgmSettingValue(run, setting.name, "0.2")
					pgTrgmNotices(t, notices)
				})
			})
		}
	}
}

func pgTrgmRoutineSettingsCases(t *testing.T) {
	for _, exception := range []bool{false, true} {
		name := "G18RoutineSave"
		if exception {
			name = "G18ExceptionSavedImage"
		}
		t.Run(name, func(t *testing.T) {
			pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
				pgTrgmInstalledElsewhere(t, p, b)
				run := pgTrgmRun(t, p, b, c)
				run(fixture.Exec("SET pg_trgm.similarity_threshold='0.2'", 0))
				// application_name enters a genuine SAVE invocation image before
				// any pg_trgm demand. Dynamic SQL prevents CREATE-time folding.
				body := "BEGIN PERFORM set_config('pg_trgm.similarity_threshold','0.8',true); EXECUTE 'SELECT similarity(''cat'',''cat'')'; RETURN current_setting('pg_trgm.similarity_threshold'); END"
				want := "0.8"
				if exception {
					body = "BEGIN BEGIN PERFORM set_config('pg_trgm.similarity_threshold','0.8',true); EXECUTE 'SELECT similarity(''cat'',''cat'')'; RAISE EXCEPTION 'restore image'; EXCEPTION WHEN OTHERS THEN RETURN current_setting('pg_trgm.similarity_threshold'); END; END"
					want = "0.2"
				}
				run(fixture.Exec("CREATE FUNCTION trgm_environment() RETURNS text LANGUAGE plpgsql SET application_name='trgm_saved' AS $$ "+body+" $$", 0))
				pgTrgmLoaded(run, false)
				run(fixture.Select("SELECT trgm_environment()", []string{"trgm_environment"}, []string{"TEXT"}, [][]any{{want}}))
				pgTrgmLoaded(run, true)
				pgTrgmSettingValue(run, pgTrgmSettings[0].name, "0.2")
				run(fixture.ImplicitError("SET pg_trgm.similarity_threshold='bogus'", pgTrgmBadSetting(pgTrgmSettings[0].name, "bogus")))
				pgTrgmNotices(t, notices)
			})
		})
	}
	t.Run("G18EffectiveRoutineThresholdAndNestedSave", func(t *testing.T) {
		pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
			run := pgTrgmRun(t, p, b, c)
			run(fixture.Exec("CREATE EXTENSION pg_trgm", 0))
			run(fixture.Exec("SET pg_trgm.similarity_threshold='0.8'", 0))
			run(fixture.Exec("CREATE FUNCTION trgm_scoped(a text,b text) RETURNS boolean LANGUAGE SQL SET pg_trgm.similarity_threshold='0.25' AS $$ SELECT a % b $$", 0))
			run(pgTrgmScalar(b, "SELECT trgm_scoped($1,$2)", "trgm_scoped", "BOOL", true, "a", "aa"))
			pgTrgmSettingValue(run, pgTrgmSettings[0].name, "0.8")
			run(fixture.Exec("CREATE FUNCTION trgm_nested() RETURNS boolean LANGUAGE plpgsql SET pg_trgm.similarity_threshold='0.5' AS $$ DECLARE v boolean; BEGIN SELECT trgm_scoped('a','aa') INTO v; IF NOT v THEN RETURN false; END IF; BEGIN PERFORM set_config('pg_trgm.similarity_threshold','0',true); RAISE EXCEPTION 'discard'; EXCEPTION WHEN OTHERS THEN RETURN NOT ('a' % 'aa'); END; END $$", 0))
			run(pgTrgmScalar(b, "SELECT trgm_nested()", "trgm_nested", "BOOL", true))
			pgTrgmSettingValue(run, pgTrgmSettings[0].name, "0.8")
			pgTrgmNotices(t, notices)
		})
	})
}
