package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func pgTrgmActivationCases(t *testing.T) {
	t.Run("G1FreshAndHiddenPlaceholders", func(t *testing.T) {
		pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
			run := pgTrgmRun(t, p, b, c)
			for _, setting := range pgTrgmSettings {
				pgTrgmSettingValue(run, setting.name, nil)
				run(fixture.ImplicitError("SHOW "+setting.name, fixture.Diagnostic{Code: "42704", Message: `unrecognized configuration parameter "` + setting.name + `"`}))
			}
			pgTrgmLoaded(run, false)
			run(fixture.Exec("SET pg_trgm.similarity_threshold='0.2'", 0))
			pgTrgmSettingValue(run, pgTrgmSettings[0].name, "0.2")
			pgTrgmLoaded(run, false)
			pgTrgmShowAll(t, p, b, c, nil)
			pgTrgmNotices(t, notices)
		})
	})
	t.Run("G2CreateRollbackAndIsolation", func(t *testing.T) {
		pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
			run := pgTrgmRun(t, p, b, c)
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("CREATE EXTENSION pg_trgm", 0))
			pgTrgmLoaded(run, true)
			run(fixture.Exec("ROLLBACK", 0))
			pgTrgmLoaded(run, true)
			for _, setting := range pgTrgmSettings {
				pgTrgmSettingValue(run, setting.name, setting.boot)
			}
			var otherNotices []fixture.Diagnostic
			_, other, r := fixture.NoticeReferencePool(t, p, b.PG, &otherNotices)
			otherBackend := b
			otherBackend.Recorder = r
			pgTrgmLoaded(pgTrgmRun(t, p, otherBackend, other), false)
			if fixture.SettingsPhysical(t, c) == fixture.SettingsPhysical(t, other) {
				t.Fatal("activation isolation reused a physical connection")
			}
			pgTrgmNotices(t, notices)
			pgTrgmNotices(t, &otherNotices)
		})
	})
	t.Run("G3SkippedCreateDoesNotLoad", func(t *testing.T) {
		pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
			pgTrgmInstalledElsewhere(t, p, b)
			run := pgTrgmRun(t, p, b, c)
			pgTrgmLoaded(run, false)
			run(fixture.Exec("CREATE EXTENSION IF NOT EXISTS pg_trgm", 0))
			pgTrgmLoaded(run, false)
			pgTrgmNotices(t, notices, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: `extension "pg_trgm" already exists, skipping`})
		})
	})
	for _, protocol := range []bool{false, true} {
		name := "G4SQLPrepare"
		if protocol {
			name = "G4ProtocolPrepareDescribe"
		}
		t.Run(name, func(t *testing.T) {
			pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
				pgTrgmInstalledElsewhere(t, p, b)
				run := pgTrgmRun(t, p, b, c)
				pgTrgmLoaded(run, false)
				if protocol {
					q := "SELECT similarity('cat','cat')"
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					pgTrgmLoaded(run, false)
					held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					fixture.RunStep(t, p.Ctx, held, b.Recorder, pgTrgmScalar(b, q, "similarity", "FLOAT4", float64(1)))
				} else {
					run(fixture.Exec("PREPARE trgm_plan AS SELECT similarity('cat','cat')", 0))
					pgTrgmLoaded(run, false)
					run(pgTrgmScalar(b, "EXECUTE trgm_plan", "similarity", "FLOAT4", float64(1)))
					run(fixture.Exec("DEALLOCATE trgm_plan", 0))
				}
				pgTrgmLoaded(run, true)
				pgTrgmNotices(t, notices)
			})
		})
	}
	for _, tc := range []struct {
		name, query string
		rows        [][]any
		loaded      bool
	}{
		{"G5ConstantNull", "SELECT similarity(NULL::text,'cat')", [][]any{{nil}}, false},
		{"G5DynamicNull", "SELECT similarity(v,'cat') FROM trgm_activation_input", [][]any{{nil}}, true},
		{"G5ZeroRows", "SELECT similarity(v,'cat') FROM trgm_activation_input", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
				pgTrgmInstalledElsewhere(t, p, b)
				run := pgTrgmRun(t, p, b, c)
				run(fixture.Exec("CREATE TABLE trgm_activation_input(v text)", 0))
				if tc.name == "G5DynamicNull" {
					run(fixture.Exec("INSERT INTO trgm_activation_input VALUES(NULL)", 1))
				}
				pgTrgmLoaded(run, false)
				run(pgTrgmSelect(b, tc.query, []string{"similarity"}, []string{"FLOAT4"}, tc.rows))
				pgTrgmLoaded(run, tc.loaded)
				pgTrgmNotices(t, notices)
			})
		})
	}
	t.Run("G6DetachedRenamedMovedOriginal", func(t *testing.T) {
		pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
			pgTrgmInstalledElsewhere(t, p, b)
			elsewhere := pgTrgmRun(t, p, b, b.DB)
			for _, q := range []string{"CREATE SCHEMA trgm_moved", "ALTER FUNCTION similarity(text,text) RENAME TO original_similarity", "ALTER FUNCTION original_similarity(text,text) SET SCHEMA trgm_moved", "ALTER EXTENSION pg_trgm DROP FUNCTION trgm_moved.original_similarity(text,text)", "DROP EXTENSION pg_trgm"} {
				elsewhere(fixture.Exec(q, 0))
			}
			run := pgTrgmRun(t, p, b, c)
			pgTrgmLoaded(run, false)
			run(pgTrgmScalar(b, "SELECT trgm_moved.original_similarity('cat','cat')", "original_similarity", "FLOAT4", float64(1)))
			pgTrgmLoaded(run, true)
			pgTrgmNotices(t, notices)
		})
	})
	t.Run("G7DropDiscardRecreateLifetime", func(t *testing.T) {
		pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
			pool, physicalConn, record := fixture.NoticeReferencePool(t, p, b.PG, notices)
			c = physicalConn
			b.Recorder = record
			run := pgTrgmRun(t, p, b, c)
			run(fixture.Exec("CREATE EXTENSION pg_trgm", 0))
			run(fixture.Exec("SET pg_trgm.similarity_threshold='0.2'", 0))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("DROP EXTENSION pg_trgm", 0))
			run(fixture.Exec("ROLLBACK", 0))
			pgTrgmLoaded(run, true)
			pgTrgmSettingValue(run, pgTrgmSettings[0].name, "0.2")
			run(fixture.Exec("DROP EXTENSION pg_trgm", 0))
			pgTrgmLoaded(run, true)
			run(fixture.Exec("CREATE EXTENSION pg_trgm", 0))
			pgTrgmSettingValue(run, pgTrgmSettings[0].name, "0.2")
			run(fixture.Exec("DISCARD ALL", 0))
			pgTrgmLoaded(run, true)
			pgTrgmSettingValue(run, pgTrgmSettings[0].name, "0.3")
			pgTrgmShowAll(t, p, b, c, map[string]string{"pg_trgm.similarity_threshold": "0.3", "pg_trgm.word_similarity_threshold": "0.6", "pg_trgm.strict_word_similarity_threshold": "0.5"})
			physical := fixture.SettingsPhysical(t, c)
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			if err := pool.Close(); err != nil {
				t.Fatal(err)
			}
			var freshNotices []fixture.Diagnostic
			_, fresh, recorder := fixture.NoticeReferencePool(t, p, b.PG, &freshNotices)
			freshBackend := b
			freshBackend.Recorder = recorder
			if fixture.SettingsPhysical(t, fresh) == physical {
				t.Fatal("physical-close witness reused owner")
			}
			pgTrgmLoaded(pgTrgmRun(t, p, freshBackend, fresh), false)
			pgTrgmNotices(t, &freshNotices)
			pgTrgmNotices(t, notices)
		})
	})
}
