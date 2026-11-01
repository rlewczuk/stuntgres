package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func pgTrgmPersistedSettingsCases(t *testing.T) {
	for _, setting := range pgTrgmSettings {
		t.Run("G20CapturedUserReset/"+setting.name, func(t *testing.T) {
			pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
				run := pgTrgmRun(t, p, b, c)
				run(fixture.Exec("CREATE ROLE trgm_reader LOGIN PASSWORD 'role-attributes-fixture'", 0))
				run(fixture.Exec("CREATE EXTENSION pg_trgm", 0))
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO trgm_reader", 0))
				run(fixture.Exec("ALTER ROLE trgm_reader SET "+setting.name+"='0.2'", 0))
				var loginNotices []fixture.Diagnostic
				_, fresh, recorder := fixture.NoticeReferencePool(t, p, b.PG, &loginNotices, fixture.NoticeReferenceOption{AuthenticatedRole: "trgm_reader"})
				login := b
				login.Recorder = recorder
				loginRun := pgTrgmRun(t, p, login, fresh)
				pgTrgmLoaded(loginRun, false)
				pgTrgmSettingValue(loginRun, setting.name, "0.2")
				loginRun(fixture.Exec("SET "+setting.name+"='0.8'", 0))
				pgTrgmActivate(loginRun, login)
				loginRun(fixture.Select("SELECT setting,reset_val,boot_val,source,vartype FROM pg_settings WHERE name=$1", []string{"setting", "reset_val", "boot_val", "source", "vartype"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"0.8", "0.2", setting.boot, "session", "real"}}, setting.name))
				// Editing the persisted source must not rewrite an existing
				// physical session's captured reset baseline during promotion.
				run(fixture.Exec("ALTER ROLE trgm_reader SET "+setting.name+"='0.4'", 0))
				loginRun(fixture.Exec("RESET "+setting.name, 0))
				pgTrgmSettingValue(loginRun, setting.name, "0.2")
				loginRun(fixture.Select("SELECT reset_val,source FROM pg_settings WHERE name=$1", []string{"reset_val", "source"}, []string{"TEXT", "TEXT"}, [][]any{{"0.2", "user"}}, setting.name))
				loginRun(fixture.Exec("SET "+setting.name+"=1", 0))
				loginRun(fixture.Exec("SET "+setting.name+"=DEFAULT", 0))
				pgTrgmSettingValue(loginRun, setting.name, "0.2")
				pgTrgmNotices(t, &loginNotices)
				pgTrgmNotices(t, notices)
			})
		})
		t.Run("G20InvalidCapturedResetValidCurrent/"+setting.name, func(t *testing.T) {
			pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
				run := pgTrgmRun(t, p, b, c)
				run(fixture.Exec("CREATE ROLE trgm_reader LOGIN PASSWORD 'role-attributes-fixture'", 0))
				// Persist a genuine unknown STRING source before this issuer
				// loads pg_trgm; installing in a different backend is deliberate.
				run(fixture.Exec("ALTER ROLE trgm_reader SET "+setting.name+"='bogus'", 0))
				pgTrgmInstalledElsewhere(t, p, b)
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO trgm_reader", 0))
				var loginNotices []fixture.Diagnostic
				_, fresh, recorder := fixture.NoticeReferencePool(t, p, b.PG, &loginNotices, fixture.NoticeReferenceOption{AuthenticatedRole: "trgm_reader"})
				login := b
				login.Recorder = recorder
				loginRun := pgTrgmRun(t, p, login, fresh)
				pgTrgmLoaded(loginRun, false)
				pgTrgmSettingValue(loginRun, setting.name, "bogus")
				loginRun(fixture.Exec("SET "+setting.name+"='0.8'", 0))
				pgTrgmActivate(loginRun, login)
				pgTrgmNotices(t, &loginNotices, pgTrgmBadSetting(setting.name, "bogus"))
				loginRun(fixture.Select("SELECT setting,reset_val,boot_val,source,vartype FROM pg_settings WHERE name=$1", []string{"setting", "reset_val", "boot_val", "source", "vartype"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"0.8", setting.boot, setting.boot, "session", "real"}}, setting.name))
				loginRun(fixture.Exec("RESET "+setting.name, 0))
				pgTrgmSettingValue(loginRun, setting.name, setting.boot)
				loginRun(fixture.Select("SELECT source FROM pg_settings WHERE name=$1", []string{"source"}, []string{"TEXT"}, [][]any{{"default"}}, setting.name))
				pgTrgmActivate(loginRun, login)
				pgTrgmNotices(t, &loginNotices)
				pgTrgmNotices(t, notices)
			})
		})
	}
}
